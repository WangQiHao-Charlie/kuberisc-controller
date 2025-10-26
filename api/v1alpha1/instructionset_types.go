package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type ExecTemplate struct {
	Command []string `json:"command,omitempty"`
}

type Instruction struct {
	Name         string            `json:"name"`
	Driver       map[string]string `json:"driver,omitempty"` // mode/addr 等，自由扩展
	ExecTemplate *ExecTemplate     `json:"execTemplate,omitempty"`
}

type InstructionSetSpec struct {
	Runtime      string        `json:"runtime,omitempty"`
	Version      string        `json:"version,omitempty"`
	Instructions []Instruction `json:"instructions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=is
type InstructionSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              InstructionSetSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type InstructionSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InstructionSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InstructionSet{}, &InstructionSetList{})
}
