package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	ScaleScheduleKind        = "ScaleSchedule"
	ClusterScaleScheduleKind = "ClusterScaleSchedule"

	ConditionValid             = "Valid"
	ConditionReady             = "Ready"
	ConditionTargetFound       = "TargetFound"
	ConditionSchedulesResolved = "SchedulesResolved"
	ConditionScalingAllowed    = "ScalingAllowed"
	ConditionSuspended         = "Suspended"
)

type WindowType string

const (
	WindowAlways  WindowType = "Always"
	WindowMonthly WindowType = "Monthly"
)

// ScheduleSpec is shared by namespace and cluster-scoped schedules.
type ScheduleSpec struct {
	// +kubebuilder:default=UTC
	// +optional
	TimeZone string `json:"timeZone,omitempty"`
	// ValidFrom is inclusive.
	// +optional
	ValidFrom *metav1.Time `json:"validFrom,omitempty"`
	// ValidUntil is exclusive.
	// +optional
	ValidUntil *metav1.Time   `json:"validUntil,omitempty"`
	Schedule   ScheduleWindow `json:"schedule"`
}

type ScheduleWindow struct {
	// +kubebuilder:validation:Enum=Always;Monthly
	Type WindowType `json:"type"`
	// +optional
	Monthly *MonthlyWindow `json:"monthly,omitempty"`
}

type MonthlyWindow struct {
	// Months restricts occurrence start months; empty means every month.
	// +optional
	Months []int32 `json:"months,omitempty"`
	// +kubebuilder:validation:MinItems=1
	Days []int32 `json:"days"`
	// StartTime is a local HH:MM wall time.
	StartTime string `json:"startTime"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=44640
	DurationMinutes int32 `json:"durationMinutes"`
}

type ScheduleStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	NextTransitionTime *metav1.Time `json:"nextTransitionTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=css
// +kubebuilder:subresource:status
type ClusterScaleSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ScheduleSpec   `json:"spec"`
	Status            ScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterScaleScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterScaleSchedule `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=ss
// +kubebuilder:subresource:status
type ScaleSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ScheduleSpec   `json:"spec"`
	Status            ScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ScaleScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScaleSchedule `json:"items"`
}

type ScheduleReference struct {
	// +kubebuilder:default=dkhalife.dev
	// +optional
	APIGroup string `json:"apiGroup,omitempty"`
	// +kubebuilder:validation:Enum=ScaleSchedule;ClusterScaleSchedule
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Reserved for forward compatibility; cross-namespace references are unsupported.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

type DeploymentReference struct {
	// +kubebuilder:default=apps/v1
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`
	// +kubebuilder:validation:Enum=Deployment
	// +kubebuilder:default=Deployment
	// +optional
	Kind string `json:"kind,omitempty"`
	Name string `json:"name"`
}

type DeploymentScaleRule struct {
	Name        string            `json:"name"`
	ScheduleRef ScheduleReference `json:"scheduleRef"`
	Replicas    int32             `json:"replicas"`
	// +optional
	Priority int32 `json:"priority,omitempty"`
}

type SuspensionBehavior string

const (
	SuspensionHoldCurrent     SuspensionBehavior = "HoldCurrent"
	SuspensionRestoreBaseline SuspensionBehavior = "RestoreBaseline"
)

type Suspension struct {
	// +optional
	Enabled bool `json:"enabled"`
	// +kubebuilder:validation:Enum=HoldCurrent;RestoreBaseline
	// +kubebuilder:default=HoldCurrent
	Behavior SuspensionBehavior `json:"behavior"`
	// +optional
	Reason string `json:"reason,omitempty"`
}

type DeploymentScalePlanSpec struct {
	TargetRef DeploymentReference   `json:"targetRef"`
	Baseline  int32                 `json:"baseline"`
	Rules     []DeploymentScaleRule `json:"rules"`
	// +optional
	Suspension *Suspension `json:"suspension,omitempty"`
}

type DeploymentScalePlanStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	ActiveRule string `json:"activeRule,omitempty"`
	// +optional
	DesiredReplicas *int32 `json:"desiredReplicas,omitempty"`
	// +optional
	CurrentReplicas *int32 `json:"currentReplicas,omitempty"`
	// +optional
	LastEvaluationTime *metav1.Time `json:"lastEvaluationTime,omitempty"`
	// +optional
	NextEvaluationTime *metav1.Time `json:"nextEvaluationTime,omitempty"`
	// +optional
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`
	// +optional
	LastAppliedReplicas *int32 `json:"lastAppliedReplicas,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=dsp
// +kubebuilder:subresource:status
type DeploymentScalePlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DeploymentScalePlanSpec   `json:"spec"`
	Status            DeploymentScalePlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type DeploymentScalePlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeploymentScalePlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&ClusterScaleSchedule{}, &ClusterScaleScheduleList{},
		&ScaleSchedule{}, &ScaleScheduleList{},
		&DeploymentScalePlan{}, &DeploymentScalePlanList{},
	)
}
