package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNamespaceRoleBindingLifecycle(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "team", Labels: map[string]string{NamespaceEnabledLabel: "true"},
	}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace).Build()
	reconciler := &NamespaceRoleBindingReconciler{
		Client: client, Scheme: scheme,
		ControllerServiceAccount: types.NamespacedName{Namespace: "system", Name: "controller"},
	}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Name: "team"}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	binding := &rbacv1.RoleBinding{}
	key := types.NamespacedName{Namespace: "team", Name: ManagedRoleBindingName}
	if err := client.Get(context.Background(), key, binding); err != nil {
		t.Fatal(err)
	}
	if binding.RoleRef.Name != DefaultScalerRoleName || len(binding.Subjects) != 1 || binding.Subjects[0].Name != "controller" {
		t.Fatalf("unexpected binding: %#v", binding)
	}

	namespace.Labels[NamespaceEnabledLabel] = "false"
	if err := client.Update(context.Background(), namespace); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(context.Background(), key, binding); err == nil {
		t.Fatal("expected managed binding to be deleted")
	}
}
