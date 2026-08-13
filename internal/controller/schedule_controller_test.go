package controller

import (
	"context"
	"testing"
	"time"

	v1alpha1 "dkhalife.dev/schedule-autoscaler/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestScaleScheduleReconcilerUpdatesStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	resource := &v1alpha1.ScaleSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "office", Namespace: "team", Generation: 3},
		Spec: v1alpha1.ScheduleSpec{
			TimeZone: "UTC", Schedule: v1alpha1.ScheduleWindow{Type: v1alpha1.WindowAlways},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(resource).
		WithStatusSubresource(&v1alpha1.ScaleSchedule{}).Build()
	reconciler := &ScaleScheduleReconciler{
		Client: client,
		Now:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "team", Name: "office"}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	got := &v1alpha1.ScaleSchedule{}
	if err := client.Get(context.Background(), request.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.ObservedGeneration != 3 || !statusConditionTrue(got.Status.Conditions, v1alpha1.ConditionValid) ||
		!statusConditionTrue(got.Status.Conditions, v1alpha1.ConditionReady) {
		t.Fatalf("unexpected status: %#v", got.Status)
	}
}

func statusConditionTrue(conditions []metav1.Condition, conditionType string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}
