package types

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
)

type TypeMetaInfo struct {
	Kind       string `json:"kind,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
}

type ObjectMetaInfo struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	UID               apitypes.UID      `json:"uid,omitempty"`
	CreationTimestamp metav1.Time       `json:"creationTimestamp,omitempty"`
}

func NewObjectMetaInfo(k8sObjectMeta metav1.ObjectMeta) ObjectMetaInfo {
	return ObjectMetaInfo{
		Name:              k8sObjectMeta.Name,
		Namespace:         k8sObjectMeta.Namespace,
		Labels:            k8sObjectMeta.Labels,
		Annotations:       k8sObjectMeta.Annotations,
		UID:               k8sObjectMeta.UID,
		CreationTimestamp: k8sObjectMeta.CreationTimestamp,
	}
}

func NewTypeMetaInfo(k8sTypeMeta metav1.TypeMeta) TypeMetaInfo {
	return TypeMetaInfo{
		Kind:       k8sTypeMeta.Kind,
		APIVersion: k8sTypeMeta.APIVersion,
	}
}
