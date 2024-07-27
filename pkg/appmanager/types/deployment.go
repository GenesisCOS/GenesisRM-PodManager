package types

import appsv1 "k8s.io/api/apps/v1"

type DeploymentSpecInfo struct {
	Replicas *int32 `json:"replicas,omitempty"`
}

type DeploymentStatusInfo struct {
	// Total number of non-terminated pods targeted by this deployment (their labels match the selector).
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Total number of non-terminated pods targeted by this deployment that have the desired template spec.
	// +optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`

	// readyReplicas is the number of pods targeted by this Deployment with a Ready Condition.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Total number of available pods (ready for at least minReadySeconds) targeted by this deployment.
	// +optional
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`

	// Total number of unavailable pods targeted by this deployment. This is the total number of
	// pods that are still required for the deployment to have 100% available capacity. They may
	// either be pods that are running but not yet available or pods that still have not been created.
	// +optional
	UnavailableReplicas int32 `json:"unavailableReplicas,omitempty"`
}

type DeploymentInfo struct {
	TypeMetaInfo   `json:",inline"`
	ObjectMetaInfo `json:"metadata,omitempty"`
	Spec           DeploymentSpecInfo   `json:"spec,omitempty"`
	Status         DeploymentStatusInfo `json:"status,omitempty"`
}

func NewDeploymentInfo(deploy *appsv1.Deployment) *DeploymentInfo {
	deployCopy := deploy.DeepCopy()
	return &DeploymentInfo{
		TypeMetaInfo:   NewTypeMetaInfo(deployCopy.TypeMeta),
		ObjectMetaInfo: NewObjectMetaInfo(deployCopy.ObjectMeta),
		Spec: DeploymentSpecInfo{
			Replicas: deployCopy.Spec.Replicas,
		},
		Status: DeploymentStatusInfo{
			Replicas:            deployCopy.Status.Replicas,
			UpdatedReplicas:     deployCopy.Status.UpdatedReplicas,
			ReadyReplicas:       deployCopy.Status.ReadyReplicas,
			AvailableReplicas:   deployCopy.Status.AvailableReplicas,
			UnavailableReplicas: deployCopy.Status.UnavailableReplicas,
		},
	}
}
