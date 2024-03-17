package podmanager

import (
	"fmt"
	"net/http"
)

const (
	cgroupPodCPUQuotaName  = "swiftmonitor_cgroup_pod_cpu_quota"
	cgroupCPUQuotaName     = "swiftmonitor_cgroup_cpu_quota"
	cgroupCPUAcctUsageName = "swiftmonitor_cgroup_cpuacct_usage"

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

func (c *Monitor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	metrics := c.manager.CollectMetrics()

	for k, v := range metrics {
		loadVal, ok := c.manager.GetPodMap().Load(k)
		pInfo := loadVal.(*PodInfo)
		if ok {
			// cgroup CPU quotas
			out := fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				cgroupCPUAcctUsageName, pInfo.Pod.Name, pInfo.Pod.Namespace, v.CPUUsage)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %f\n",
				cgroupCPUQuotaName, pInfo.Pod.Name, pInfo.Pod.Namespace, v.CPUQuota)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %f\n",
				cgroupPodCPUQuotaName, pInfo.Pod.Name, pInfo.Pod.Namespace, v.PodCPUQuota)

			// Container memory.stat
			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				cgroupMemStatUsageInBytesName, pInfo.Pod.Name, pInfo.Pod.Namespace, v.ContainerMemStat.RSS+v.ContainerMemStat.Cache)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				cgroupMemStatSwapInBytesName, pInfo.Pod.Name, pInfo.Pod.Namespace, v.ContainerMemStat.Swap)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				cgroupMemStatRssInBytesName, pInfo.Pod.Name, pInfo.Pod.Namespace, v.ContainerMemStat.RSS)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				cgroupMemStatCacheInBytesName, pInfo.Pod.Name, pInfo.Pod.Namespace, v.ContainerMemStat.Cache)

			// K8s stats
			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodMemoryRequest, pInfo.Pod.Name, pInfo.Pod.Namespace, v.MemRequest)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodCpuRequest, pInfo.Pod.Name, pInfo.Pod.Namespace, v.CPURequest)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodMemoryLimit, pInfo.Pod.Name, pInfo.Pod.Namespace, v.MemLimit)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodCpuLimit, pInfo.Pod.Name, pInfo.Pod.Namespace, v.CPULimit)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodCpuAllocated, pInfo.Pod.Name, pInfo.Pod.Namespace, v.CPUAllocated)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodMemoryAllocated, pInfo.Pod.Name, pInfo.Pod.Namespace, v.MemAllocated)

			w.Write([]byte(out))
		}
	}
}
