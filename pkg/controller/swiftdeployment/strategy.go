package swiftdeployment

import (
	corev1 "k8s.io/api/core/v1"
)

type SuspendStrategy string
type ActivateStrategy string

const (
	SuspendInOrder SuspendStrategy = "InOrder"
)

const (
	ActivateInOrder ActivateStrategy = "InOrder"
)

func SelectPodsToSuspend(rPods []*corev1.Pod, number int, ss SuspendStrategy) []*corev1.Pod {
	switch ss {
	case SuspendInOrder:
		return SelectPodsToSuspendInOrder(rPods, number)
	default:
		return SelectPodsToSuspendInOrder(rPods, number)
	}
}

func SelectPodsToSuspendInOrder(rPods []*corev1.Pod, number int) []*corev1.Pod {
	return getPodInOrder(rPods, number)
}

func SelectPodsToActivate(sPods []*corev1.Pod, number int, as ActivateStrategy) []*corev1.Pod {
	switch as {
	case ActivateInOrder:
		return SelectPodsToSuspendInOrder(sPods, number)
	default:
		return SelectPodsToSuspendInOrder(sPods, number)
	}
}

func SelectPodsToActivateInOrder(sPods []*corev1.Pod, number int) []*corev1.Pod {
	return getPodInOrder(sPods, number)
}

func getPodInOrder(pods []*corev1.Pod, number int) []*corev1.Pod {
	residue := number
	var ret []*corev1.Pod
	for _, pod := range pods {
		ret = append(ret, pod)
		residue--
		if residue == 0 {
			break
		}
	}
	return ret
}
