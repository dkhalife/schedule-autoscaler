package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ManagedRoleBindingName  = "schedule-autoscaler"
	ManagedRoleBindingLabel = "scaling.dkhalife.dev/managed"
	DefaultScalerRoleName   = "deployment-scale-editor"
)

// NamespaceRoleBindingReconciler grants the controller service account the
// deployment scale role in explicitly enabled namespaces.
type NamespaceRoleBindingReconciler struct {
	client.Client
	Scheme                   *runtime.Scheme
	ControllerServiceAccount types.NamespacedName
	ClusterRoleName          string
	NamespaceSelector        labels.Selector
}

func (r *NamespaceRoleBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: req.Name}, namespace); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	key := types.NamespacedName{Namespace: namespace.Name, Name: ManagedRoleBindingName}
	existing := &rbacv1.RoleBinding{}
	enabled := namespaceSelected(namespace, r.NamespaceSelector)
	if !enabled {
		if err := r.Get(ctx, key, existing); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		if existing.Labels[ManagedRoleBindingLabel] != "true" {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, existing))
	}
	if r.ControllerServiceAccount.Name == "" || r.ControllerServiceAccount.Namespace == "" {
		return ctrl.Result{}, nil
	}
	roleName := r.ClusterRoleName
	if roleName == "" {
		roleName = DefaultScalerRoleName
	}
	desired := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: ManagedRoleBindingName, Namespace: namespace.Name,
			Labels: map[string]string{ManagedRoleBindingLabel: "true"},
		},
		RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: roleName},
		Subjects: []rbacv1.Subject{{
			Kind: "ServiceAccount", Name: r.ControllerServiceAccount.Name,
			Namespace: r.ControllerServiceAccount.Namespace,
		}},
	}
	if err := r.Get(ctx, key, existing); apierrors.IsNotFound(err) {
		return ctrl.Result{}, r.Create(ctx, desired)
	} else if err != nil {
		return ctrl.Result{}, err
	}
	if existing.RoleRef == desired.RoleRef && subjectsEqual(existing.Subjects, desired.Subjects) &&
		existing.Labels[ManagedRoleBindingLabel] == "true" {
		return ctrl.Result{}, nil
	}
	// RoleRef is immutable, so a role configuration change requires replacement.
	if existing.RoleRef != desired.RoleRef {
		if err := r.Delete(ctx, existing); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	existing.Subjects = desired.Subjects
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	existing.Labels[ManagedRoleBindingLabel] = "true"
	return ctrl.Result{}, r.Update(ctx, existing)
}

func subjectsEqual(a, b []rbacv1.Subject) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *NamespaceRoleBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Watches(&rbacv1.RoleBinding{}, handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
			if obj.GetName() != ManagedRoleBindingName {
				return nil
			}
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: obj.GetNamespace()}}}
		})).
		Complete(r)
}
