package controller

import (
	"context"
	"reflect"
	"time"

	v1alpha1 "dkhalife.dev/schedule-autoscaler/api/v1alpha1"
	"dkhalife.dev/schedule-autoscaler/internal/schedule"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type ScaleScheduleReconciler struct {
	client.Client
	Now Clock
}

func (r *ScaleScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	resource := &v1alpha1.ScaleSchedule{}
	if err := r.Get(ctx, req.NamespacedName, resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	original := resource.DeepCopy().Status
	now := scheduleNow(r.Now)
	updateScheduleStatus(&resource.Status, resource.Generation, resource.Spec, now)
	if reflect.DeepEqual(original, resource.Status) {
		return scheduleRequeue(resource.Status.NextTransitionTime, now), nil
	}
	if err := r.Status().Update(ctx, resource); err != nil {
		return ctrl.Result{}, err
	}
	return scheduleRequeue(resource.Status.NextTransitionTime, now), nil
}

func (r *ScaleScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ScaleSchedule{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

type ClusterScaleScheduleReconciler struct {
	client.Client
	Now Clock
}

func (r *ClusterScaleScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	resource := &v1alpha1.ClusterScaleSchedule{}
	if err := r.Get(ctx, req.NamespacedName, resource); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	original := resource.DeepCopy().Status
	now := scheduleNow(r.Now)
	updateScheduleStatus(&resource.Status, resource.Generation, resource.Spec, now)
	if reflect.DeepEqual(original, resource.Status) {
		return scheduleRequeue(resource.Status.NextTransitionTime, now), nil
	}
	if err := r.Status().Update(ctx, resource); err != nil {
		return ctrl.Result{}, err
	}
	return scheduleRequeue(resource.Status.NextTransitionTime, now), nil
}

func (r *ClusterScaleScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterScaleSchedule{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

func updateScheduleStatus(status *v1alpha1.ScheduleStatus, generation int64, spec v1alpha1.ScheduleSpec, now time.Time) {
	status.ObservedGeneration = generation
	status.NextTransitionTime = nil
	result, err := schedule.Evaluate(spec, now)
	if err != nil {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: v1alpha1.ConditionValid, Status: metav1.ConditionFalse,
			Reason: "InvalidSchedule", Message: err.Error(),
			ObservedGeneration: generation, LastTransitionTime: metav1.NewTime(now),
		})
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: v1alpha1.ConditionReady, Status: metav1.ConditionFalse,
			Reason: "Invalid", Message: err.Error(),
			ObservedGeneration: generation, LastTransitionTime: metav1.NewTime(now),
		})
		return
	}
	if result.NextTransition != nil {
		value := metav1.NewTime(*result.NextTransition)
		status.NextTransitionTime = &value
	}
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionValid, Status: metav1.ConditionTrue,
		Reason: "Valid", Message: "Schedule specification is valid",
		ObservedGeneration: generation, LastTransitionTime: metav1.NewTime(now),
	})
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: v1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: "Ready", Message: "Schedule is available for plan evaluation",
		ObservedGeneration: generation, LastTransitionTime: metav1.NewTime(now),
	})
}

func scheduleNow(clock Clock) time.Time {
	if clock != nil {
		return clock().UTC()
	}
	return time.Now().UTC()
}

func scheduleRequeue(next *metav1.Time, now time.Time) ctrl.Result {
	if next == nil {
		return ctrl.Result{}
	}
	value := next.Time.UTC()
	return requeueFor(&value, now)
}
