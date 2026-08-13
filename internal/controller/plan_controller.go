package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	v1alpha1 "dkhalife.dev/schedule-autoscaler/api/v1alpha1"
	"dkhalife.dev/schedule-autoscaler/internal/schedule"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	NamespaceEnabledLabel = "scaling.dkhalife.dev/enabled"

	targetNameIndex      = "scalePlan.targetName"
	namespacedRefIndex   = "scalePlan.namespacedSchedule"
	clusterRefIndex      = "scalePlan.clusterSchedule"
	missingRefRetry      = 30 * time.Second
	maximumTimerDuration = 24 * time.Hour
)

type Clock func() time.Time

type DeploymentScalePlanReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Scaler            DeploymentScaler
	Now               Clock
	NamespaceSelector labels.Selector
}

type evaluatedRule struct {
	index  int
	rule   v1alpha1.DeploymentScaleRule
	result schedule.Result
}

func (r *DeploymentScalePlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	plan := &v1alpha1.DeploymentScalePlan{}
	if err := r.Get(ctx, req.NamespacedName, plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	now := scheduleNow(r.Now)
	restoreComplete := plan.Status.ObservedGeneration == plan.Generation &&
		meta.IsStatusConditionTrue(plan.Status.Conditions, v1alpha1.ConditionSuspended) &&
		meta.IsStatusConditionTrue(plan.Status.Conditions, v1alpha1.ConditionReady)
	resetPlanStatus(plan, now)

	if err := validatePlan(plan); err != nil {
		setPlanCondition(plan, v1alpha1.ConditionTargetFound, metav1.ConditionUnknown, "NotChecked", "Target was not checked", now)
		setPlanCondition(plan, v1alpha1.ConditionScalingAllowed, metav1.ConditionUnknown, "NotChecked", "Namespace was not checked", now)
		setPlanCondition(plan, v1alpha1.ConditionSchedulesResolved, metav1.ConditionFalse, "InvalidPlan", err.Error(), now)
		setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionFalse, "InvalidPlan", err.Error(), now)
		return r.finish(ctx, plan, ctrl.Result{})
	}

	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: plan.Namespace}, namespace); err != nil {
		setPlanCondition(plan, v1alpha1.ConditionTargetFound, metav1.ConditionUnknown, "NotChecked", "Target was not checked", now)
		setPlanCondition(plan, v1alpha1.ConditionScalingAllowed, metav1.ConditionFalse, "NamespaceReadFailed", err.Error(), now)
		setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionFalse, "NamespaceReadFailed", err.Error(), now)
		result, statusErr := r.finish(ctx, plan, ctrl.Result{RequeueAfter: missingRefRetry})
		if statusErr != nil {
			return result, statusErr
		}
		return result, err
	}
	if !namespaceSelected(namespace, r.NamespaceSelector) {
		message := fmt.Sprintf("namespace %q does not match the configured namespace selector", plan.Namespace)
		setPlanCondition(plan, v1alpha1.ConditionTargetFound, metav1.ConditionUnknown, "NotChecked", "Target was not checked", now)
		setPlanCondition(plan, v1alpha1.ConditionScalingAllowed, metav1.ConditionFalse, "NamespaceNotEnabled", message, now)
		setPlanCondition(plan, v1alpha1.ConditionSchedulesResolved, metav1.ConditionUnknown, "NotChecked", "Schedules were not checked", now)
		setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionFalse, "NamespaceNotEnabled", message, now)
		return r.finish(ctx, plan, ctrl.Result{})
	}
	setPlanCondition(plan, v1alpha1.ConditionScalingAllowed, metav1.ConditionTrue, "NamespaceEnabled", "Namespace is enabled for scaling", now)

	evaluated, missing, evalErr := r.resolveAndEvaluate(ctx, plan, now)
	if evalErr != nil {
		setPlanCondition(plan, v1alpha1.ConditionTargetFound, metav1.ConditionUnknown, "NotChecked", "Target was not checked", now)
		setPlanCondition(plan, v1alpha1.ConditionSchedulesResolved, metav1.ConditionFalse, "ScheduleInvalid", evalErr.Error(), now)
		setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionFalse, "InvalidSchedule", evalErr.Error(), now)
		return r.finish(ctx, plan, ctrl.Result{})
	}
	if len(missing) > 0 {
		message := "missing schedule references: " + strings.Join(missing, ", ")
		setPlanCondition(plan, v1alpha1.ConditionTargetFound, metav1.ConditionUnknown, "NotChecked", "Target was not checked", now)
		setPlanCondition(plan, v1alpha1.ConditionSchedulesResolved, metav1.ConditionFalse, "ScheduleNotFound", message, now)
		setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionFalse, "ScheduleNotFound", message, now)
		return r.finish(ctx, plan, ctrl.Result{RequeueAfter: missingRefRetry})
	}
	setPlanCondition(plan, v1alpha1.ConditionSchedulesResolved, metav1.ConditionTrue, "Resolved", "All schedule references resolved", now)

	next := nextRuleTransition(evaluated)
	if next != nil {
		value := metav1.NewTime(*next)
		plan.Status.NextEvaluationTime = &value
	}
	desired := plan.Spec.Baseline
	if winner := winningRule(evaluated); winner != nil {
		desired = winner.rule.Replicas
		plan.Status.ActiveRule = winner.rule.Name
	}

	suspension := plan.Spec.Suspension
	if suspension != nil && suspension.Enabled {
		switch suspension.Behavior {
		case "", v1alpha1.SuspensionHoldCurrent:
			setPlanCondition(plan, v1alpha1.ConditionSuspended, metav1.ConditionTrue, "HoldCurrent", suspensionMessage(*suspension), now)
		case v1alpha1.SuspensionRestoreBaseline:
			desired = plan.Spec.Baseline
			plan.Status.ActiveRule = ""
		default:
			message := fmt.Sprintf("unsupported suspension behavior %q", suspension.Behavior)
			setPlanCondition(plan, v1alpha1.ConditionSuspended, metav1.ConditionFalse, "InvalidBehavior", message, now)
			setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionFalse, "InvalidSuspension", message, now)
			return r.finish(ctx, plan, requeueFor(next, now))
		}
	}

	scale, err := r.Scaler.GetScale(ctx, plan.Namespace, plan.Spec.TargetRef.Name)
	if err != nil {
		reason := "ReadFailed"
		readyReason := "TargetNotFound"
		if apierrors.IsNotFound(err) {
			reason = "NotFound"
		}
		if apierrors.IsForbidden(err) {
			readyReason = "PermissionDenied"
			setPlanCondition(plan, v1alpha1.ConditionScalingAllowed, metav1.ConditionFalse, "PermissionDenied", err.Error(), now)
		}
		setPlanCondition(plan, v1alpha1.ConditionTargetFound, metav1.ConditionFalse, reason, err.Error(), now)
		setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionFalse, readyReason, err.Error(), now)
		result, statusErr := r.finish(ctx, plan, ctrl.Result{RequeueAfter: missingRefRetry})
		if statusErr != nil {
			return result, statusErr
		}
		return result, err
	}
	plan.Status.CurrentReplicas = int32ptr(scale.Spec.Replicas)
	setPlanCondition(plan, v1alpha1.ConditionTargetFound, metav1.ConditionTrue, "Found", "Deployment scale subresource is available", now)

	if suspension != nil && suspension.Enabled &&
		(suspension.Behavior == "" || suspension.Behavior == v1alpha1.SuspensionHoldCurrent) {
		plan.Status.DesiredReplicas = int32ptr(scale.Spec.Replicas)
		setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionTrue, "Suspended", "Current scale is held by suspension", now)
		return r.finish(ctx, plan, requeueFor(next, now))
	}

	plan.Status.DesiredReplicas = int32ptr(desired)
	if suspension != nil && suspension.Enabled &&
		suspension.Behavior == v1alpha1.SuspensionRestoreBaseline && restoreComplete {
		setPlanCondition(plan, v1alpha1.ConditionSuspended, metav1.ConditionTrue, "RestoreBaseline", suspensionMessage(*suspension), now)
		setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionTrue, "Suspended", "Baseline was restored; further scale writes are held", now)
		return r.finish(ctx, plan, requeueFor(next, now))
	}
	if scale.Spec.Replicas != desired {
		scale.Spec.Replicas = desired
		updated, err := r.Scaler.UpdateScale(ctx, plan.Namespace, scale)
		if err != nil {
			reason := "ScaleFailed"
			if apierrors.IsForbidden(err) {
				reason = "PermissionDenied"
				setPlanCondition(plan, v1alpha1.ConditionScalingAllowed, metav1.ConditionFalse, reason, err.Error(), now)
			}
			setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionFalse, reason, err.Error(), now)
			result, statusErr := r.finish(ctx, plan, ctrl.Result{RequeueAfter: missingRefRetry})
			if statusErr != nil {
				return result, statusErr
			}
			return result, err
		}
		plan.Status.CurrentReplicas = int32ptr(updated.Spec.Replicas)
		plan.Status.LastAppliedReplicas = int32ptr(desired)
		scaledAt := metav1.NewTime(now)
		plan.Status.LastScaleTime = &scaledAt
	}
	if suspension != nil && suspension.Enabled && suspension.Behavior == v1alpha1.SuspensionRestoreBaseline {
		setPlanCondition(plan, v1alpha1.ConditionSuspended, metav1.ConditionTrue, "RestoreBaseline", suspensionMessage(*suspension), now)
	}
	setPlanCondition(plan, v1alpha1.ConditionReady, metav1.ConditionTrue, "Reconciled", "Deployment scale matches the evaluated plan", now)
	return r.finish(ctx, plan, requeueFor(next, now))
}

func resetPlanStatus(plan *v1alpha1.DeploymentScalePlan, now time.Time) {
	plan.Status.ObservedGeneration = plan.Generation
	plan.Status.ActiveRule = ""
	plan.Status.DesiredReplicas = nil
	plan.Status.CurrentReplicas = nil
	plan.Status.NextEvaluationTime = nil
	evaluatedAt := metav1.NewTime(now)
	plan.Status.LastEvaluationTime = &evaluatedAt
	setPlanCondition(plan, v1alpha1.ConditionSchedulesResolved, metav1.ConditionUnknown, "NotChecked", "Schedules have not been checked", now)
	if plan.Spec.Suspension == nil || !plan.Spec.Suspension.Enabled {
		setPlanCondition(plan, v1alpha1.ConditionSuspended, metav1.ConditionFalse, "NotSuspended", "Plan is not suspended", now)
	}
}

func validatePlan(plan *v1alpha1.DeploymentScalePlan) error {
	if plan.Spec.TargetRef.Name == "" {
		return fmt.Errorf("target name is required")
	}
	if plan.Spec.TargetRef.Kind != "" && plan.Spec.TargetRef.Kind != "Deployment" {
		return fmt.Errorf("unsupported target kind %q", plan.Spec.TargetRef.Kind)
	}
	if plan.Spec.TargetRef.APIVersion != "" && plan.Spec.TargetRef.APIVersion != "apps/v1" {
		return fmt.Errorf("unsupported target apiVersion %q", plan.Spec.TargetRef.APIVersion)
	}
	if plan.Spec.Baseline < 0 || plan.Spec.Baseline > 1_000_000 {
		return fmt.Errorf("baseline must be between 0 and 1000000")
	}
	if len(plan.Spec.Rules) == 0 {
		return fmt.Errorf("at least one rule is required")
	}
	seen := map[string]struct{}{}
	for i, rule := range plan.Spec.Rules {
		if rule.Name == "" {
			return fmt.Errorf("rule %d has an empty name", i)
		}
		if _, exists := seen[rule.Name]; exists {
			return fmt.Errorf("duplicate rule name %q", rule.Name)
		}
		seen[rule.Name] = struct{}{}
		if rule.Replicas < 0 || rule.Replicas > 1_000_000 {
			return fmt.Errorf("rule %q replicas must be between 0 and 1000000", rule.Name)
		}
		if rule.Priority < -1_000_000 || rule.Priority > 1_000_000 {
			return fmt.Errorf("rule %q priority must be between -1000000 and 1000000", rule.Name)
		}
		if rule.ScheduleRef.Name == "" {
			return fmt.Errorf("rule %q schedule name is required", rule.Name)
		}
		if rule.ScheduleRef.APIGroup != "" && rule.ScheduleRef.APIGroup != v1alpha1.GroupVersion.Group {
			return fmt.Errorf("rule %q has unsupported apiGroup %q", rule.Name, rule.ScheduleRef.APIGroup)
		}
		if rule.ScheduleRef.Namespace != "" {
			return fmt.Errorf("rule %q sets unsupported schedule namespace", rule.Name)
		}
	}
	return nil
}

func (r *DeploymentScalePlanReconciler) resolveAndEvaluate(ctx context.Context, plan *v1alpha1.DeploymentScalePlan, now time.Time) ([]evaluatedRule, []string, error) {
	evaluated := make([]evaluatedRule, 0, len(plan.Spec.Rules))
	var missing []string
	for index, rule := range plan.Spec.Rules {
		ref := rule.ScheduleRef
		key := ref.Kind + "/" + ref.Name
		var spec v1alpha1.ScheduleSpec
		switch ref.Kind {
		case v1alpha1.ScaleScheduleKind:
			resource := &v1alpha1.ScaleSchedule{}
			err := r.Get(ctx, types.NamespacedName{Namespace: plan.Namespace, Name: ref.Name}, resource)
			if apierrors.IsNotFound(err) {
				missing = append(missing, key)
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			spec = resource.Spec
		case v1alpha1.ClusterScaleScheduleKind:
			resource := &v1alpha1.ClusterScaleSchedule{}
			err := r.Get(ctx, types.NamespacedName{Name: ref.Name}, resource)
			if apierrors.IsNotFound(err) {
				missing = append(missing, key)
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			spec = resource.Spec
		default:
			return nil, nil, fmt.Errorf("rule %q has unsupported schedule kind %q", rule.Name, ref.Kind)
		}
		result, err := schedule.Evaluate(spec, now)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		evaluated = append(evaluated, evaluatedRule{index: index, rule: rule, result: result})
	}
	sort.Strings(missing)
	return evaluated, missing, nil
}

func winningRule(evaluated []evaluatedRule) *evaluatedRule {
	var winner *evaluatedRule
	for i := range evaluated {
		candidate := &evaluated[i]
		if !candidate.result.Active {
			continue
		}
		if winner == nil || candidate.rule.Priority > winner.rule.Priority {
			winner = candidate
		}
	}
	return winner
}

func nextRuleTransition(evaluated []evaluatedRule) *time.Time {
	var result *time.Time
	for _, item := range evaluated {
		candidate := item.result.NextTransition
		if candidate != nil && (result == nil || candidate.Before(*result)) {
			value := candidate.UTC()
			result = &value
		}
	}
	return result
}

func suspensionMessage(suspension v1alpha1.Suspension) string {
	if suspension.Reason == "" {
		return "Plan suspension is active"
	}
	return "Plan suspension is active: " + suspension.Reason
}

func (r *DeploymentScalePlanReconciler) finish(ctx context.Context, plan *v1alpha1.DeploymentScalePlan, result ctrl.Result) (ctrl.Result, error) {
	latest := &v1alpha1.DeploymentScalePlan{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(plan), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if latest.Generation != plan.Generation {
		return ctrl.Result{Requeue: true}, nil
	}
	if reflect.DeepEqual(latest.Status, plan.Status) {
		return result, nil
	}
	plan.ResourceVersion = latest.ResourceVersion
	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

func setPlanCondition(plan *v1alpha1.DeploymentScalePlan, conditionType string, status metav1.ConditionStatus, reason, message string, now time.Time) {
	meta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
		Type: conditionType, Status: status, Reason: reason, Message: message,
		ObservedGeneration: plan.Generation, LastTransitionTime: metav1.NewTime(now),
	})
}

func requeueFor(next *time.Time, now time.Time) ctrl.Result {
	if next == nil {
		return ctrl.Result{}
	}
	delay := next.Sub(now)
	if delay < time.Second {
		delay = time.Second
	}
	if delay > maximumTimerDuration {
		delay = maximumTimerDuration
	}
	return ctrl.Result{RequeueAfter: delay}
}

func int32ptr(value int32) *int32 { return &value }

func namespaceSelected(namespace *corev1.Namespace, selector labels.Selector) bool {
	if selector == nil {
		return namespace.Labels[NamespaceEnabledLabel] == "true"
	}
	return selector.Matches(labels.Set(namespace.Labels))
}

func (r *DeploymentScalePlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.DeploymentScalePlan{}, targetNameIndex, func(obj client.Object) []string {
		return []string{obj.(*v1alpha1.DeploymentScalePlan).Spec.TargetRef.Name}
	}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.DeploymentScalePlan{}, namespacedRefIndex, func(obj client.Object) []string {
		var result []string
		for _, rule := range obj.(*v1alpha1.DeploymentScalePlan).Spec.Rules {
			if rule.ScheduleRef.Kind == v1alpha1.ScaleScheduleKind {
				result = append(result, rule.ScheduleRef.Name)
			}
		}
		return result
	}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.DeploymentScalePlan{}, clusterRefIndex, func(obj client.Object) []string {
		var result []string
		for _, rule := range obj.(*v1alpha1.DeploymentScalePlan).Spec.Rules {
			if rule.ScheduleRef.Kind == v1alpha1.ClusterScaleScheduleKind {
				result = append(result, rule.ScheduleRef.Name)
			}
		}
		return result
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DeploymentScalePlan{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(r.mapDeployment)).
		Watches(&v1alpha1.ScaleSchedule{}, handler.EnqueueRequestsFromMapFunc(r.mapNamespacedSchedule)).
		Watches(&v1alpha1.ClusterScaleSchedule{}, handler.EnqueueRequestsFromMapFunc(r.mapClusterSchedule)).
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.mapNamespace)).
		Complete(r)
}

func (r *DeploymentScalePlanReconciler) mapDeployment(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.listPlans(ctx, obj.GetNamespace(), targetNameIndex, obj.GetName())
}
func (r *DeploymentScalePlanReconciler) mapNamespacedSchedule(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.listPlans(ctx, obj.GetNamespace(), namespacedRefIndex, obj.GetName())
}
func (r *DeploymentScalePlanReconciler) mapClusterSchedule(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.listPlans(ctx, "", clusterRefIndex, obj.GetName())
}
func (r *DeploymentScalePlanReconciler) mapNamespace(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.listPlans(ctx, obj.GetName(), "", "")
}
func (r *DeploymentScalePlanReconciler) listPlans(ctx context.Context, namespace, field, value string) []reconcile.Request {
	list := &v1alpha1.DeploymentScalePlanList{}
	options := []client.ListOption{}
	if namespace != "" {
		options = append(options, client.InNamespace(namespace))
	}
	if field != "" {
		options = append(options, client.MatchingFields{field: value})
	}
	if err := r.List(ctx, list, options...); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return requests
}
