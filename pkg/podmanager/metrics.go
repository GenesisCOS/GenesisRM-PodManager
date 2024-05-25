package podmanager

import (
	"time"

	statsv1 "github.com/containerd/cgroups/stats/v1"
	cgroup "swiftkube.io/swiftkube/pkg/cgroup"
)

type PodMetrics struct {
	CPUUsage         uint64
	CPUQuota         float64
	PodCPUQuota      float64
	MemUsageInBytes  uint64
	MemLimitInBytes  uint64
	PodMemStat       *cgroup.CgMemoryStat
	ContainerMemStat *cgroup.CgMemoryStat
	CPURequest       int64
	CPULimit         int64
	CPUAllocated     int64
	MemRequest       int64
	MemLimit         int64
	MemAllocated     int64

	timestamp time.Time

	ContainerdMetrics *statsv1.Metrics
}

type NodeMetrics struct {
	NodeName       string
	AllocatableCPU uint64 // mcore
}
