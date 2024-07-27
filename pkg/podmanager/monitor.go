package podmanager

import (
	"fmt"
	"net/http"
	"strconv"

	"swiftkube.io/swiftkube/pkg/helper"
)

type Monitor struct {
	manager *PodManager
}

func (c *Monitor) parseNodeMetricsResponse(metrics *NodeMetrics) []byte {
	out := fmt.Sprintf("swiftmonitor_node_cpu_allocatable{nodename=\"%s\"} %d\n",
		metrics.NodeName, metrics.AllocatableCPU)

	return []byte(out)
}

func parsePodMetric(name string, value float64, labels map[string]string, pInfo *PodInfo) string {

	// 是否所有容器都ready了
	allReady := true
	for _, containerStatus := range pInfo.Pod.Status.ContainerStatuses {
		if !containerStatus.Ready {
			allReady = false
			break
		}
	}

	labels["podname"] = pInfo.Pod.GetName()
	labels["namespace"] = pInfo.Pod.GetNamespace()
	labels["nodename"] = pInfo.Pod.Spec.NodeName
	labels["state"] = helper.GetPodState(pInfo.Pod).String()
	labels["serviceType"] = helper.GetPodServiceType(pInfo.Pod).String()
	service, ok := pInfo.Pod.GetLabels()["swiftkube.io/service"]
	if ok {
		labels["service"] = service
	}
	labels["ready"] = strconv.FormatBool(allReady)

	labelString := ""
	for k, v := range labels {
		if labelString != "" {
			labelString += ","
		}
		labelString += fmt.Sprintf("%s=\"%s\"", k, v)
	}
	return fmt.Sprintf("%s{%s} %f\n", name, labelString, value)
}

func (c *Monitor) parsePodMetricsResponse(pInfo *PodInfo, v *PodMetrics) []byte {
	if pInfo.Pod == nil {
		return []byte("")
	}

	out := ""

	// CPU quota and period
	out += parsePodMetric("swiftmonitor_pod_cpu_period", float64(v.PodCPUPeriod), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_cpu_quota", float64(v.PodCPUQuota), make(map[string]string), pInfo)

	// K8s stats
	out += parsePodMetric("swiftmonitor_pod_total_memory_request", float64(v.Kubernetes.TotalMemRequest), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_total_cpu_request", float64(v.PodCPURequest), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_total_memory_limit", float64(v.Kubernetes.TotalMemLimit), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_total_cpu_limit", float64(v.Kubernetes.TotalCPULimit), make(map[string]string), pInfo)

	// Containerd CPU
	out += parsePodMetric("swiftmonitor_pod_cpu_usage", float64(v.ContainerdMetrics.GetCPU().GetUsageUsec()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_cpu_throttled", float64(v.ContainerdMetrics.GetCPU().GetThrottledUsec()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_cpu_nr_throttled", float64(v.ContainerdMetrics.GetCPU().GetNrThrottled()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_cpu_nr_periods", float64(v.ContainerdMetrics.GetCPU().GetNrPeriods()), make(map[string]string), pInfo)

	// Containerd memory
	out += parsePodMetric("swiftmonitor_pod_memory_stat_swap_usage", float64(v.ContainerdMetrics.GetMemory().GetSwapUsage()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_memory_stat_anon", float64(v.ContainerdMetrics.GetMemory().GetAnon()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_memory_stat_active_anon", float64(v.ContainerdMetrics.GetMemory().GetActiveAnon()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_memory_stat_file", float64(v.ContainerdMetrics.GetMemory().GetFile()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_memory_stat_active_file", float64(v.ContainerdMetrics.GetMemory().GetActiveFile()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_memory_stat_pgfault", float64(v.ContainerdMetrics.GetMemory().GetPgfault()), make(map[string]string), pInfo)
	out += parsePodMetric("swiftmonitor_pod_memory_stat_usage", float64(v.ContainerdMetrics.GetMemory().GetUsage()), make(map[string]string), pInfo)

	return []byte(out)
}

func (c *Monitor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	podMetrics := c.manager.CollectPodMetrics()
	nodeMetrics := c.manager.CollectNodeMetrics()

	for _, metrics := range podMetrics {
		out := c.parsePodMetricsResponse(metrics.PInfo, metrics)
		w.Write(out)
	}

	out := c.parseNodeMetricsResponse(nodeMetrics)
	w.Write(out)
}
