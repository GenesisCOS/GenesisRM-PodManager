package types

import (
	corev1 "k8s.io/api/core/v1"
)

type PodSpecInfo struct {
	Containers []corev1.Container `json:"containers"`
	NodeName   string             `json:"nodeName,omitempty"`
}

type PodStatusInfo struct {
	Phase             corev1.PodPhase          `json:"phase,omitempty"`
	ContainerStatuses []corev1.ContainerStatus `json:"containerStatuses,omitempty"`
}

type PodInfo struct {
	TypeMetaInfo   `json:",inline"`
	ObjectMetaInfo `json:"metadata,omitempty"`
	Spec           PodSpecInfo   `json:"spec,omitempty"`
	Status         PodStatusInfo `json:"status,omitempty"`
}

func NewPodInfo(pod *corev1.Pod) *PodInfo {
	podCopy := pod.DeepCopy()
	return &PodInfo{
		ObjectMetaInfo: NewObjectMetaInfo(podCopy.ObjectMeta),
		TypeMetaInfo:   NewTypeMetaInfo(podCopy.TypeMeta),
		Spec: PodSpecInfo{
			Containers: podCopy.Spec.Containers,
			NodeName:   podCopy.Spec.NodeName,
		},
		Status: PodStatusInfo{
			Phase:             podCopy.Status.Phase,
			ContainerStatuses: podCopy.Status.ContainerStatuses,
		},
	}
}
