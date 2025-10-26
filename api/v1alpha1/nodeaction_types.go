package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type InstructionRef struct {
	Name        string `json:"name"`
	Instruction string `json:"instruction"`
	Runtime     string `json:"runtime"`
}

type Artifact struct {
	Type string `json:"type,omitempty"`
	URL  string `json:"url,omitempty"`
}

type NodeActionSpec struct {
	NodeName          string            `json:"nodeName"`
	InstructionRef    InstructionRef    `json:"instructionRef"`
	ResolvedSubjectID string            `json:"resolvedSubjectID,omitempty"`
	Params            map[string]string `json:"params,omitempty"`
	ExecutionID       string            `json:"executionID,omitempty"`
	TimeoutSeconds    int32             `json:"timeoutSeconds,omitempty"`
}

type Condition struct {
	Type               string       `json:"type"`
	Status             string       `json:"status"` // True|False|Unknown
	Reason             string       `json:"reason,omitempty"`
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
}

type NodeActionResult struct {
	Artifacts  []Artifact `json:"artifacts,omitempty"`
	StdoutTail string     `json:"stdoutTail,omitempty"`
	StderrTail string     `json:"stderrTail,omitempty"`
}

type NodeActionStatus struct {
	Phase      string            `json:"phase,omitempty"` // Pending|Running|Succeeded|Failed
	StartedAt  *metav1.Time      `json:"startedAt,omitempty"`
	FinishedAt *metav1.Time      `json:"finishedAt,omitempty"`
	Result     *NodeActionResult `json:"result,omitempty"`
	Conditions []Condition       `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=na
type NodeAction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              NodeActionSpec   `json:"spec,omitempty"`
	Status            NodeActionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type NodeActionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeAction `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeAction{}, &NodeActionList{})
}
