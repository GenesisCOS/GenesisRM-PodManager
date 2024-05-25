package podmanager

type PodCPUResource struct {
	Quota uint64
}

type PodResource struct {
	CPU PodCPUResource
}

type CPUResourceInfo struct {
	Total            uint64 // cgroup quota
	TotalAllocatable uint64 // cgroup quota
	Allocatable      uint64 // cgroup quota
}

type ResourceInfo struct {
	CPU *CPUResourceInfo
}
