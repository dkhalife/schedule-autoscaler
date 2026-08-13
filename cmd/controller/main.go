package main

import (
	"flag"
	"os"

	v1alpha1 "dkhalife.dev/schedule-autoscaler/api/v1alpha1"
	"dkhalife.dev/schedule-autoscaler/internal/controller"
	"go.uber.org/zap/zapcore"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var metricsAddress, probeAddress, serviceAccountName, serviceAccountNamespace, scalerRole string
	var namespaceSelector, logLevel string
	var leaderElect bool
	flag.StringVar(&metricsAddress, "metrics-bind-address", ":8080", "Metrics endpoint address.")
	flag.StringVar(&probeAddress, "health-probe-bind-address", ":8081", "Health probe address.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election.")
	flag.StringVar(&serviceAccountName, "controller-service-account", "schedule-autoscaler", "Controller service account name.")
	flag.StringVar(&serviceAccountNamespace, "controller-namespace", "schedule-autoscaler-system", "Controller service account namespace.")
	flag.StringVar(&scalerRole, "scaler-cluster-role", controller.DefaultScalerRoleName, "ClusterRole bound in enabled namespaces.")
	flag.StringVar(&namespaceSelector, "namespace-selector", controller.NamespaceEnabledLabel+"=true", "Label selector for namespaces managed by the controller.")
	flag.StringVar(&logLevel, "log-level", "", "Log level alias (debug, info, warn, error). Overrides --zap-log-level.")
	zapOptions := zap.Options{Development: false}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	if logLevel != "" {
		var level zapcore.Level
		must(level.UnmarshalText([]byte(logLevel)))
		zapOptions.Level = level
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	selector, err := labels.Parse(namespaceSelector)
	must(err)

	scheme := runtime.NewScheme()
	must(clientgoscheme.AddToScheme(scheme))
	must(appsv1.AddToScheme(scheme))
	must(autoscalingv1.AddToScheme(scheme))
	must(corev1.AddToScheme(scheme))
	must(rbacv1.AddToScheme(scheme))
	must(v1alpha1.AddToScheme(scheme))

	config := ctrl.GetConfigOrDie()
	manager, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddress},
		HealthProbeBindAddress: probeAddress,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "schedule-autoscaler.dkhalife.dev",
	})
	must(err)
	clientset, err := kubernetes.NewForConfig(config)
	must(err)

	plan := &controller.DeploymentScalePlanReconciler{
		Client: manager.GetClient(), Scheme: manager.GetScheme(),
		Scaler: controller.KubernetesDeploymentScaler{Client: clientset}, NamespaceSelector: selector,
	}
	must(plan.SetupWithManager(manager))
	must((&controller.ScaleScheduleReconciler{Client: manager.GetClient()}).SetupWithManager(manager))
	must((&controller.ClusterScaleScheduleReconciler{Client: manager.GetClient()}).SetupWithManager(manager))
	namespace := &controller.NamespaceRoleBindingReconciler{
		Client: manager.GetClient(), Scheme: manager.GetScheme(),
		ControllerServiceAccount: types.NamespacedName{Namespace: serviceAccountNamespace, Name: serviceAccountName},
		ClusterRoleName:          scalerRole,
		NamespaceSelector:        selector,
	}
	must(namespace.SetupWithManager(manager))
	must(manager.AddHealthzCheck("healthz", healthz.Ping))
	must(manager.AddReadyzCheck("readyz", healthz.Ping))
	must(manager.Start(ctrl.SetupSignalHandler()))
}

func must(err error) {
	if err != nil {
		ctrl.Log.Error(err, "fatal error")
		os.Exit(1)
	}
}
