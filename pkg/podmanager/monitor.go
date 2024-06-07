package podmanager

import (
	"fmt"
	"net/http"
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

	state, ok := pInfo.Pod.GetLabels()["swiftkube.io/state"]
	if !ok {
		state = "None"
	}

	out := ""

	// CPU
	out += fmt.Sprintf("swiftmonitor_pod_cpu_period{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.PodCPUPeriod)

	out += fmt.Sprintf("swiftmonitor_pod_cpu_quota{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.PodCPUQuota)

	// Memory
	out += fmt.Sprintf("swiftmonitor_pod_memory_stat_swap_usage{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerdMetrics.GetMemory().GetSwapUsage())

	out += fmt.Sprintf("swiftmonitor_pod_memory_stat_anon{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerdMetrics.GetMemory().GetAnon())

	// K8s stats
	out += fmt.Sprintf("swiftmonitor_pod_total_memory_request{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.Kubernetes.TotalMemRequest)

	out += fmt.Sprintf("swiftmonitor_pod_total_cpu_request{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.Kubernetes.TotalCPURequest)

	out += fmt.Sprintf("swiftmonitor_pod_total_memory_limit{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.Kubernetes.TotalMemLimit)

	out += fmt.Sprintf("swiftmonitor_pod_total_cpu_limit{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.Kubernetes.TotalCPULimit)

	// Containerd
	out += fmt.Sprintf("swiftmonitor_pod_cpu_usage{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerdMetrics.GetCPU().GetUsageUsec())

	out += fmt.Sprintf("swiftmonitor_pod_cpu_throttled{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerdMetrics.GetCPU().GetThrottledUsec())

	out += fmt.Sprintf("swiftmonitor_pod_cpu_nr_throttled{podname=\"%s\", namespace=\"%s\", state=\"%s\"} %d\n",
		pInfo.Pod.Name, pInfo.Pod.Namespace, state, v.ContainerdMetrics.GetCPU().GetNrThrottled())

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
