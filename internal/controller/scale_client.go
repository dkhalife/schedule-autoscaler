package controller

import (
	"context"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DeploymentScaler isolates the Deployment scale subresource for testing.
type DeploymentScaler interface {
	GetScale(context.Context, string, string) (*autoscalingv1.Scale, error)
	UpdateScale(context.Context, string, *autoscalingv1.Scale) (*autoscalingv1.Scale, error)
}

type KubernetesDeploymentScaler struct {
	Client kubernetes.Interface
}

func (s KubernetesDeploymentScaler) GetScale(ctx context.Context, namespace, name string) (*autoscalingv1.Scale, error) {
	return s.Client.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
}

func (s KubernetesDeploymentScaler) UpdateScale(ctx context.Context, namespace string, scale *autoscalingv1.Scale) (*autoscalingv1.Scale, error) {
	return s.Client.AppsV1().Deployments(namespace).UpdateScale(ctx, scale.Name, scale, metav1.UpdateOptions{})
}
