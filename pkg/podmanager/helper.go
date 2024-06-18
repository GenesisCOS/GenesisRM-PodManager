package podmanager

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"swiftkube.io/swiftkube/pkg/podmanager/types"
)

func GetPodState(pod *corev1.Pod) types.PodState {
	state, ok := pod.GetLabels()[types.STATE_LABEL]
	if !ok {
		return types.POD_UNKNOWN_STATE
	}

	switch state {
	case types.POD_READY_FULLSPEED_STATE.String():
		return types.POD_READY_FULLSPEED_STATE
	case types.POD_READY_CATNAP_STATE.String():
		return types.POD_READY_CATNAP_STATE
	case types.POD_READY_RUNNING_STATE.String():
		return types.POD_READY_RUNNING_STATE
	case types.POD_READY_LONGNAP_STATE.String():
		return types.POD_READY_LONGNAP_STATE
	case types.POD_INITIALIZING_STATE.String():
		return types.POD_INITIALIZING_STATE
	case types.POD_WARMINGUP_STATE.String():
		return types.POD_WARMINGUP_STATE
	default:
		return types.POD_UNKNOWN_STATE
	}
}

func GetServiceType(pod *corev1.Pod) types.PodServiceType {
	serviceType, ok := pod.GetLabels()[types.SERVICE_TYPE_LABEL]
	if !ok {
		return types.SERVICE_TYPE_UNKNOWN
	}

	if serviceType == types.SERVICE_TYPE_BE.String() {
		return types.SERVICE_TYPE_BE
	} else if serviceType == types.SERVICE_TYPE_LC.String() {
		return types.SERVICE_TYPE_LC
	}

	return types.SERVICE_TYPE_UNKNOWN
}

func GetThrottleTarget(pod *corev1.Pod) float64 {
	throttleTarget, err := strconv.ParseFloat(pod.GetLabels()[types.CPU_THROTTLE_TARGET_LABEL], 64)
	if err != nil {
		// 默认 throttled target 为0.1
		throttleTarget = 0.1
	}
	return throttleTarget
}
