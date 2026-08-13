package controller

import (
	"context"
	"testing"
	"time"

	v1alpha1 "dkhalife.dev/schedule-autoscaler/api/v1alpha1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeScaler struct {
	scale   *autoscalingv1.Scale
	updates int
	err     error
}

func (f *fakeScaler) GetScale(context.Context, string, string) (*autoscalingv1.Scale, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.scale.DeepCopy(), nil
}
func (f *fakeScaler) UpdateScale(_ context.Context, _ string, scale *autoscalingv1.Scale) (*autoscalingv1.Scale, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.updates++
	f.scale = scale.DeepCopy()
	return f.scale.DeepCopy(), nil
}

func TestPlanReconcilerScalesUsingHighestPriorityRule(t *testing.T) {
	namespace := enabledNamespace()
	first := alwaysSchedule("first")
	second := alwaysSchedule("second")
	third := alwaysSchedule("third")
	plan := testPlan(
		rule("low", "first", 3, 1),
		rule("high", "second", 7, 10),
		rule("equal-priority-later", "third", 9, 10),
	)
	scaler := &fakeScaler{scale: deploymentScale(1)}
	reconciler, c := testReconciler(t, scaler, namespace, first, second, third, plan)
	reconciler.Now = func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: clientKey(plan)}); err != nil {
		t.Fatal(err)
	}
	if scaler.updates != 1 || scaler.scale.Spec.Replicas != 7 {
		t.Fatalf("updates=%d replicas=%d", scaler.updates, scaler.scale.Spec.Replicas)
	}
	got := getPlan(t, c, plan)
	if got.Status.ActiveRule != "high" || got.Status.DesiredReplicas == nil ||
		*got.Status.DesiredReplicas != 7 || !conditionTrue(got, v1alpha1.ConditionReady) {
		t.Fatalf("unexpected status: %#v", got.Status)
	}
}

func TestPlanReconcilerUsesBaselineWhenInactive(t *testing.T) {
	namespace := enabledNamespace()
	schedule := &v1alpha1.ScaleSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "future", Namespace: "team"},
		Spec: v1alpha1.ScheduleSpec{
			TimeZone: "UTC",
			Schedule: v1alpha1.ScheduleWindow{Type: v1alpha1.WindowMonthly, Monthly: &v1alpha1.MonthlyWindow{
				Months: []int32{12}, Days: []int32{31}, StartTime: "23:00", DurationMinutes: 30,
			}},
		},
	}
	plan := testPlan(rule("future", "future", 8, 0))
	scaler := &fakeScaler{scale: deploymentScale(9)}
	reconciler, _ := testReconciler(t, scaler, namespace, schedule, plan)
	reconciler.Now = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: clientKey(plan)}); err != nil {
		t.Fatal(err)
	}
	if scaler.scale.Spec.Replicas != plan.Spec.Baseline {
		t.Fatalf("replicas=%d want baseline=%d", scaler.scale.Spec.Replicas, plan.Spec.Baseline)
	}
}

func TestPlanReconcilerSuspensionBehaviors(t *testing.T) {
	tests := []struct {
		name     string
		behavior v1alpha1.SuspensionBehavior
		want     int32
		updates  int
	}{
		{name: "hold", behavior: v1alpha1.SuspensionHoldCurrent, want: 9, updates: 0},
		{name: "restore", behavior: v1alpha1.SuspensionRestoreBaseline, want: 2, updates: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := testPlan(rule("active", "always", 8, 0))
			plan.Spec.Suspension = &v1alpha1.Suspension{Enabled: true, Behavior: tt.behavior, Reason: "operator"}
			scaler := &fakeScaler{scale: deploymentScale(9)}
			reconciler, c := testReconciler(t, scaler, enabledNamespace(), alwaysSchedule("always"), plan)
			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: clientKey(plan)}); err != nil {
				t.Fatal(err)
			}
			if scaler.scale.Spec.Replicas != tt.want || scaler.updates != tt.updates {
				t.Fatalf("replicas=%d updates=%d", scaler.scale.Spec.Replicas, scaler.updates)
			}
			if !conditionTrue(getPlan(t, c, plan), v1alpha1.ConditionSuspended) {
				t.Fatal("expected Suspended=True")
			}
			if tt.behavior == v1alpha1.SuspensionRestoreBaseline {
				scaler.scale.Spec.Replicas = 9
				if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: clientKey(plan)}); err != nil {
					t.Fatal(err)
				}
				if scaler.updates != 1 {
					t.Fatalf("RestoreBaseline wrote more than once: updates=%d", scaler.updates)
				}
			}
		})
	}
}

func TestPlanReconcilerReportsMissingAndDisabledNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace *corev1.Namespace
		objects   []runtime.Object
		condition string
	}{
		{
			name: "missing", namespace: enabledNamespace(),
			condition: v1alpha1.ConditionSchedulesResolved,
		},
		{
			name: "disabled", namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team"}},
			condition: v1alpha1.ConditionScalingAllowed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := testPlan(rule("missing", "absent", 3, 0))
			objects := append([]runtime.Object{tt.namespace, plan}, tt.objects...)
			reconciler, c := testReconciler(t, &fakeScaler{scale: deploymentScale(1)}, objects...)
			if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: clientKey(plan)}); err != nil {
				t.Fatal(err)
			}
			if conditionTrue(getPlan(t, c, plan), tt.condition) {
				t.Fatalf("expected %s=False", tt.condition)
			}
		})
	}
}

func TestPlanReconcilerUsesClusterSchedule(t *testing.T) {
	cluster := &v1alpha1.ClusterScaleSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "global"},
		Spec:       alwaysSpec(),
	}
	plan := testPlan(v1alpha1.DeploymentScaleRule{
		Name: "global", Replicas: 6,
		ScheduleRef: v1alpha1.ScheduleReference{Kind: v1alpha1.ClusterScaleScheduleKind, Name: "global"},
	})
	scaler := &fakeScaler{scale: deploymentScale(1)}
	reconciler, _ := testReconciler(t, scaler, enabledNamespace(), cluster, plan)
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: clientKey(plan)}); err != nil {
		t.Fatal(err)
	}
	if scaler.scale.Spec.Replicas != 6 {
		t.Fatalf("replicas=%d", scaler.scale.Spec.Replicas)
	}
}

func TestPlanReconcilerReportsMissingTarget(t *testing.T) {
	plan := testPlan(rule("active", "one", 2, 0))
	scaler := &fakeScaler{err: apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web")}
	reconciler, _ := testReconciler(t, scaler, enabledNamespace(), alwaysSchedule("one"), plan)
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: clientKey(plan)})
	if err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("error = %v", err)
	}
}

func testReconciler(t *testing.T, scaler DeploymentScaler, objects ...runtime.Object) (*DeploymentScalePlanReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(objects...).
		WithStatusSubresource(&v1alpha1.DeploymentScalePlan{}).
		Build()
	return &DeploymentScalePlanReconciler{Client: c, Scheme: scheme, Scaler: scaler}, c
}

func testPlan(rules ...v1alpha1.DeploymentScaleRule) *v1alpha1.DeploymentScalePlan {
	return &v1alpha1.DeploymentScalePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "plan", Namespace: "team"},
		Spec: v1alpha1.DeploymentScalePlanSpec{
			TargetRef: v1alpha1.DeploymentReference{Name: "web"},
			Baseline:  2, Rules: rules,
		},
	}
}

func rule(name, scheduleName string, replicas, priority int32) v1alpha1.DeploymentScaleRule {
	return v1alpha1.DeploymentScaleRule{
		Name: name, Replicas: replicas, Priority: priority,
		ScheduleRef: v1alpha1.ScheduleReference{Kind: v1alpha1.ScaleScheduleKind, Name: scheduleName},
	}
}

func alwaysSpec() v1alpha1.ScheduleSpec {
	return v1alpha1.ScheduleSpec{TimeZone: "UTC", Schedule: v1alpha1.ScheduleWindow{Type: v1alpha1.WindowAlways}}
}

func alwaysSchedule(name string) *v1alpha1.ScaleSchedule {
	return &v1alpha1.ScaleSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team"},
		Spec:       alwaysSpec(),
	}
}

func enabledNamespace() *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "team", Labels: map[string]string{NamespaceEnabledLabel: "true"},
	}}
}

func deploymentScale(replicas int32) *autoscalingv1.Scale {
	return &autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "team"},
		Spec:       autoscalingv1.ScaleSpec{Replicas: replicas},
	}
}

func clientKey(object metav1.Object) types.NamespacedName {
	return types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}
}

func getPlan(t *testing.T, c client.Client, plan *v1alpha1.DeploymentScalePlan) *v1alpha1.DeploymentScalePlan {
	t.Helper()
	got := &v1alpha1.DeploymentScalePlan{}
	if err := c.Get(context.Background(), clientKey(plan), got); err != nil {
		t.Fatal(err)
	}
	return got
}

func conditionTrue(plan *v1alpha1.DeploymentScalePlan, conditionType string) bool {
	condition := meta.FindStatusCondition(plan.Status.Conditions, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}
