package podmanager

import (
	"time"

	stats "github.com/containerd/cgroups/v3/cgroup2/stats"
)

type KubernetesContainerMetrics struct {
	CPURequest int64
	CPULimit   int64

	MemRequest int64
	MemLimit   int64
}

type KubernetesPodMetrics struct {
	containers []*KubernetesContainerMetrics

	TotalCPURequest int64
	TotalCPULimit   int64

	TotalMemRequest int64
	TotalMemLimit   int64
}

type PodMetrics struct {
	PInfo *PodInfo

	PodCPUQuota   int64
	PodCPUPeriod  uint64
	PodCPURequest uint64

	Kubernetes *KubernetesPodMetrics
	timestamp  time.Time

	ContainerdMetrics *stats.Metrics
}

type NodeMetrics struct {
	NodeName       string
	AllocatableCPU uint64 // mcore
}
