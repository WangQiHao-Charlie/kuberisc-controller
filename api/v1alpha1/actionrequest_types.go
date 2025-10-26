package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type CompletionPolicy struct {
	Type     string `json:"type,omitempty"` // All|AtLeastN
	AtLeastN int    `json:"atLeastN,omitempty"`
}
type Strategy struct {
	Parallelism      int               `json:"parallelism,omitempty"`
	RetryLimit       int               `json:"retryLimit,omitempty"`
	CompletionPolicy *CompletionPolicy `json:"completionPolicy,omitempty"`
}
type Selector struct {
	PodSelector map[string]string `json:"podSelector,omitempty"` // 简化版 matchLabels
}
type ActionRequestSpec struct {
	InstructionRef          InstructionRef    `json:"instructionRef"`
	Params                  map[string]string `json:"params,omitempty"`
	Selector                Selector          `json:"selector"`
	Strategy                *Strategy         `json:"strategy,omitempty"`
	TimeoutSeconds          int32             `json:"timeoutSeconds,omitempty"`
	TTLSecondsAfterFinished int32             `json:"ttlSecondsAfterFinished,omitempty"`
}

type Counts struct {
	// Split fields so each has a distinct JSON tag
	Total     int `json:"total,omitempty"`
	Running   int `json:"running,omitempty"`
	Succeeded int `json:"succeeded,omitempty"`
	Failed    int `json:"failed,omitempty"`
}
type ActionRequestStatus struct {
	Phase  string  `json:"phase,omitempty"`  // Pending|Running|Aggregating|Completed
	Reason string  `json:"reason,omitempty"` // Succeeded|Partial|Failed|Timeout
	Counts *Counts `json:"counts,omitempty"`
	// 可选：CompletedAt、AggregatingAt...
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ar
type ActionRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ActionRequestSpec   `json:"spec,omitempty"`
	Status            ActionRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ActionRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ActionRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ActionRequest{}, &ActionRequestList{})
}
