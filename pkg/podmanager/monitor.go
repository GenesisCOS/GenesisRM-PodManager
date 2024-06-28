package podmanager

import (
	"fmt"
	"net/http"

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
	/* 这段代码pod label的顺序可能再每次查询的时候都不一样
	一旦不一样就会生成一条新的时序数据
	podLabelString := ""
	for k, v := range pInfo.Pod.GetLabels() {
		if podLabelString != "" {
			podLabelString += ","
		}
		podLabelString += fmt.Sprintf("%s=%s", k, v)
	}
	labels["podlabels"] = podLabelString
	*/

	labels["podname"] = pInfo.Pod.GetName()
	labels["namespace"] = pInfo.Pod.GetNamespace()
	labels["nodename"] = pInfo.Pod.Spec.NodeName
	labels["state"] = helper.GetPodState(pInfo.Pod).String()
	labels["serviceType"] = helper.GetPodServiceType(pInfo.Pod).String()

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
