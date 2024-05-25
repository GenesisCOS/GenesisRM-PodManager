package podmanager

import (
	"fmt"
	"net/http"
)

const (
	cgroupPodCPUQuotaName  = "swiftmonitor_cgroup_pod_cpu_quota"
	cgroupCPUQuotaName     = "swiftmonitor_cgroup_cpu_quota"
	cgroupCPUAcctUsageName = "swiftmonitor_cgroup_cpuacct_usage"

	containerdPodCPUUsage  = "swiftmonitor_containerd_pod_cpu_usage"
	containerdPodMemoryRSS = "swiftmonitor_containerd_pod_memory_rss"

	cgroupMemStatUsageInBytesName = "swiftmonitor_cgroup_memory_stat_usage_in_bytes" // Read from memory.stat (cache + rss).
	cgroupMemStatSwapInBytesName  = "swiftmonitor_cgroup_memory_stat_swap_in_bytes"
	cgroupMemStatRssInBytesName   = "swiftmonitor_cgroup_memory_stat_rss_in_bytes"
	cgroupMemStatCacheInBytesName = "swiftmonitor_cgroup_memory_stat_cache_in_bytes"

	swiftMonitorK8sPodMemoryRequest   = "swiftmonitor_pod_memory_request"
	swiftMonitorK8sPodCpuRequest      = "swiftmonitor_pod_cpu_request"
	swiftMonitorK8sPodMemoryLimit     = "swiftmonitor_pod_memory_limit"
	swiftMonitorK8sPodCpuLimit        = "swiftmonitor_pod_cpu_limit"
	swiftMonitorK8sPodCpuAllocated    = "swiftmonitor_pod_cpu_allocated"
	swiftMonitorK8sPodMemoryAllocated = "swiftmonitor_pod_memory_allocated"
)

type Monitor struct {
	manager *PodManager
}

func (c *Monitor) parseNodeMetricsResponse(metrics *NodeMetrics) []byte {
	out := fmt.Sprintf("swiftmonitor_node_cpu_allocatable{nodename=\"%s\"} %d\n",
		metrics.NodeName, metrics.AllocatableCPU)

	return []byte(out)
}

func (c *Monitor) parsePodMetricsResponse(pInfo *PodInfo, v *PodMetrics) []byte {
	if pInfo.Pod == nil {
		return []byte("")
	}
	if v.ContainerMemStat == nil {
		return []byte("")
	}
	state, ok := pInfo.Pod.GetLabels()["swiftkube.io/state"]
	if !ok {
		state = "None"
	}

	// cgroup CPU quotas
	out := fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		cgroupCPUAcctUsageName, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.CPUUsage)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %f\n",
		cgroupCPUQuotaName, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.CPUQuota)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %f\n",
		cgroupPodCPUQuotaName, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.PodCPUQuota)

	// Container memory.stat
	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		cgroupMemStatUsageInBytesName, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerMemStat.RSS+v.ContainerMemStat.Cache)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		cgroupMemStatSwapInBytesName, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerMemStat.Swap)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		cgroupMemStatRssInBytesName, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerMemStat.RSS)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		cgroupMemStatCacheInBytesName, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerMemStat.Cache)

	// K8s stats
	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		swiftMonitorK8sPodMemoryRequest, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.MemRequest)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		swiftMonitorK8sPodCpuRequest, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.CPURequest)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		swiftMonitorK8sPodMemoryLimit, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.MemLimit)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		swiftMonitorK8sPodCpuLimit, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.CPULimit)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		swiftMonitorK8sPodCpuAllocated, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.CPUAllocated)

	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		swiftMonitorK8sPodMemoryAllocated, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.MemAllocated)

	// Containerd
	out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		containerdPodCPUUsage, pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerdMetrics.CPU.Usage.Total)

	return []byte(out)
}

func (c *Monitor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	podMetrics := c.manager.CollectPodMetrics()
	nodeMetrics := c.manager.CollectNodeMetrics()

	for k, metrics := range podMetrics {
		loadVal, ok := c.manager.GetPodMap().Load(k)
		if ok {
			pInfo := loadVal.(*PodInfo)
			out := c.parsePodMetricsResponse(pInfo, metrics)
			w.Write(out)
		} else {
			loadVal, ok := c.manager.GetUncontrolledPodMap().Load(k)
			if ok {
				pInfo := loadVal.(*PodInfo)
				out := c.parsePodMetricsResponse(pInfo, metrics)
				w.Write(out)
			}
		}
	}

	out := c.parseNodeMetricsResponse(nodeMetrics)
	w.Write(out)
}
