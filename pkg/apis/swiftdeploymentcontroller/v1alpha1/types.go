package v1alpha1

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SwiftDeployment is a specification for a SwiftDeployment resource
type SwiftDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SwiftDeploymentSpec   `json:"spec"`
	Status SwiftDeploymentStatus `json:"status"`
}

// SwiftDeploymentSpec is the spec for a SwiftDeployment resource
type SwiftDeploymentSpec struct {
	ServiceName        string                 `json:"serviceName"`
	Replicas           *int32                 `json:"replicas"`
	RunningReplicas    *int32                 `json:"runningReplicas"`
	DeploymentTemplate DeploymentTemplateSpec `json:"deploymentTemplate"`
	ServiceTemplate    ServiceTemplateSpec    `json:"serviceTemplate"`
}

// SwiftDeploymentStatus is the status for a SwiftDeployment resource
type SwiftDeploymentStatus struct {
	RunningReplicas   int32 `json:"runningReplicas"`
	AvailableReplicas int32 `json:"availableReplicas"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SwiftDeploymentList is a list of SwiftDeployment resources
type SwiftDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []SwiftDeployment `json:"items"`
}

type DeploymentTemplateSpec struct {
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Spec              appsv1.DeploymentSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}

type ServiceTemplateSpec struct {
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Spec              corev1.ServiceSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}
