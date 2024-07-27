package helper

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
	genesissdk "swiftkube.io/swiftkube/pkg/podmanager/sdk"
)

func GetPodState(pod *corev1.Pod) genesissdk.PodState {
	state, ok := pod.GetLabels()[genesissdk.STATE_LABEL]
	if !ok {
		return genesissdk.POD_UNKNOWN_STATE
	}

	switch state {
	case genesissdk.POD_READY_FULLSPEED_STATE.String():
		return genesissdk.POD_READY_FULLSPEED_STATE
	case genesissdk.POD_READY_CATNAP_STATE.String():
		return genesissdk.POD_READY_CATNAP_STATE
	case genesissdk.POD_READY_RUNNING_STATE.String():
		return genesissdk.POD_READY_RUNNING_STATE
	case genesissdk.POD_READY_LONGNAP_STATE.String():
		return genesissdk.POD_READY_LONGNAP_STATE
	case genesissdk.POD_INITIALIZING_STATE.String():
		return genesissdk.POD_INITIALIZING_STATE
	case genesissdk.POD_WARMINGUP_STATE.String():
		return genesissdk.POD_WARMINGUP_STATE
	default:
		return genesissdk.POD_UNKNOWN_STATE
	}
}

func GetPodEndpointState(pod *corev1.Pod) genesissdk.PodEndpointState {
	endpoint, ok := pod.GetLabels()[genesissdk.ENDPOINT_LABEL]
	if !ok {
		return genesissdk.ENDPOINT_UNKNOWN
	}

	switch endpoint {
	case string(genesissdk.ENDPOINT_DOWN):
		return genesissdk.ENDPOINT_DOWN
	case string(genesissdk.ENDPOINT_UP):
		return genesissdk.ENDPOINT_UP
	default:
		return genesissdk.ENDPOINT_UNKNOWN
	}
}

func GetPodServiceType(pod *corev1.Pod) genesissdk.PodServiceType {
	serviceType, ok := pod.GetLabels()[genesissdk.SERVICE_TYPE_LABEL]
	if !ok {
		return genesissdk.SERVICE_TYPE_UNKNOWN
	}

	if serviceType == genesissdk.SERVICE_TYPE_BE.String() {
		return genesissdk.SERVICE_TYPE_BE
	} else if serviceType == genesissdk.SERVICE_TYPE_LC.String() {
		return genesissdk.SERVICE_TYPE_LC
	}

	return genesissdk.SERVICE_TYPE_UNKNOWN
}

func GetPodThrottleTarget(pod *corev1.Pod) float64 {
	throttleTarget, err := strconv.ParseFloat(pod.GetLabels()[genesissdk.CPU_THROTTLE_TARGET_LABEL], 64)
	if err != nil {
		// 默认 throttled target 为0.1
		throttleTarget = 0.1
	}
	return throttleTarget
}

func GetPodCPURequestOrDefault(pod *corev1.Pod, def uint64) (uint64, error) {
	cpuRequestStr, ok := pod.GetLabels()["swiftkube.io/cpu-request"]
	if !ok {
		return def, nil
	}
	cpuRequest, err := strconv.ParseUint(cpuRequestStr, 10, 64)
	if err != nil {
		return def, err
	}
	return cpuRequest, nil
}
