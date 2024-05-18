package podmanager

import (
	"context"
	"fmt"
	"math"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"swiftkube.io/swiftkube/pkg/podmanager/sample"
	"swiftkube.io/swiftkube/pkg/podmanager/types"
)

type CPUMetrics struct {
	exist bool

	prevUsage     int64
	prevTimestamp int64

	history sample.Sample
}

func NewCPUMetrics(maxLength int) *CPUMetrics {
	return &CPUMetrics{
		exist:     true,
		prevUsage: -1,
		history:   sample.NewFixLengthSample(maxLength),
	}
}

type CPUScaler struct {
	podmanager *PodManager

	cpuMetrics map[string]*CPUMetrics
}

func NewCPUScaler(podmanager *PodManager) *CPUScaler {
	return &CPUScaler{
		podmanager: podmanager,
		cpuMetrics: make(map[string]*CPUMetrics),
	}
}

func (s *CPUScaler) Start(ctx context.Context) {

	klog.InfoS("CPU Scaler started", "name", "GenesisRM")
	t := time.NewTimer(time.Second)

	for {
		t.Reset(time.Second)
		selector := labels.NewSelector()
		requirement, err := labels.NewRequirement(types.STATE_LABEL, selection.Exists, []string{})
		if err != nil {
			klog.ErrorS(err, "failed to construct requirement")
		}
		selector = selector.Add(*requirement)
		pods, err := s.podmanager.PodLister.List(selector)
		if err != nil {
			klog.ErrorS(err, "failed to list pods")
		}
		localPods := make([]*v1.Pod, 0)
		for _, pod := range pods {
			if pod.Spec.NodeName == s.podmanager.NodeName {
				localPods = append(localPods, pod.DeepCopy())
			}
		}

		for _, pod := range localPods {

			key, _ := cache.MetaNamespaceKeyFunc(pod)
			state := pod.GetLabels()[types.STATE_LABEL]
			endpoint := pod.GetLabels()[types.ENDPOINT_LABEL]

			v, ok := s.podmanager.PodMap.Load(key)
			if !ok {
				delete(s.cpuMetrics, key)
				continue
			}
			pInfo := v.(*PodInfo)

			var metrics *CPUMetrics
			if m, ok := s.cpuMetrics[key]; ok {
				m.exist = true
				metrics = m
			} else {
				metrics = NewCPUMetrics(types.DefaultMaxHistoryLength)
				s.cpuMetrics[key] = metrics
			}

			// adjust CPU quota
			cpuPeriod, err := pInfo.Cgroup.GetCFSPeriod()
			if err != nil {
				klog.ErrorS(err, "Fail to read CPU period")
			}
			if state == types.RR {
				var maxUsage float64 = 0
				if metrics.history.Count() < types.DefaultMinHistoryLength {
					maxUsage = types.DefaultMaxCPUQuota / 1000
				} else {
					maxUsage = metrics.history.Max()
				}
				klog.InfoS("Max CPU usage", "value", fmt.Sprintf("%f", maxUsage), "key", key)
				// TODO
				//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, int64(math.Ceil(float64(cpuPeriod)*maxUsage)))
				//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
				err = pInfo.Cgroup.SetCFSQuota(int64(math.Ceil(float64(cpuPeriod) * types.DefaultMaxCPUQuota)))
				if err != nil {
					klog.ErrorS(err, "Fail to set CPU quota")
				}
				pInfo.CPUState = types.CPU_DYNAMIC_OVERPROVISION

			} else if state == types.RFS {
				//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
				err = pInfo.Cgroup.SetCFSQuota(int64(math.Ceil(float64(cpuPeriod) * types.DefaultMaxCPUQuota)))
				if err != nil {
					klog.ErrorS(err, "Fail to set CPU quota")
				}
				pInfo.CPUState = types.CPU_MAX

			} else if state == types.Init || state == types.WU {
				//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
				err = pInfo.Cgroup.SetCFSQuota(int64(math.Ceil(float64(cpuPeriod) * types.DefaultMaxCPUQuota)))
				if err != nil {
					klog.ErrorS(err, "Fail to set CPU quota")
				}
				pInfo.CPUState = types.CPU_MAX

			} else if state == types.RCN || state == types.RLN {
				if endpoint == string(types.ENDPOINT_DOWN) {
					var avgUsage float64 = 0
					if metrics.history.Count() > types.DefaultMinHistoryLength {
						avgUsage = metrics.history.Mean()
					} else {
						avgUsage = types.DefaultMaxCPUQuota / 1000
					}
					klog.InfoS("Mean CPU usage", "value", fmt.Sprintf("%f", avgUsage), "key", key)
					// TODO
					//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, int64(math.Ceil(float64(cpuPeriod)*avgUsage/0.6)))
					//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
					err = pInfo.Cgroup.SetCFSQuota(int64(math.Ceil(float64(cpuPeriod) * types.DefaultMaxCPUQuota)))
					if err != nil {
						klog.ErrorS(err, "Fail to set CPU quota")
					}
					pInfo.CPUState = types.CPU_DYNAMIC_RESOURCE_EFFICIENT
				} else {
					//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
					err = pInfo.Cgroup.SetCFSQuota(int64(math.Ceil(float64(cpuPeriod) * types.DefaultMaxCPUQuota)))
					if err != nil {
						klog.ErrorS(err, "Fail to set CPU quota")
					}
					pInfo.CPUState = types.CPU_MAX
				}
			}

			var acctUsage int64 = 0
			for _, containerInfo := range pInfo.ContainerInfos {
				perContainerUsage, err := containerInfo.Cgroup.GetCPUAcctUsage()
				if err != nil {
					klog.ErrorS(err, "Fail to read CPU usage")
				}
				acctUsage += perContainerUsage
			}

			// Year 2262
			timestamp := time.Now().UnixNano()

			// calculate CPU usage
			prevUsage := s.cpuMetrics[key].prevUsage
			prevTimestamp := s.cpuMetrics[key].prevTimestamp
			if prevUsage != -1 {
				cpuUsage := float64(acctUsage-prevUsage) / float64(timestamp-prevTimestamp)
				s.cpuMetrics[key].history.Update(cpuUsage)
			} else {
				klog.InfoS("first metric", "key", key)
			}

			s.cpuMetrics[key].prevUsage = acctUsage
			s.cpuMetrics[key].prevTimestamp = timestamp
		}

		keys := make([]string, 0, len(s.cpuMetrics))
		for k := range s.cpuMetrics {
			keys = append(keys, k)
		}

		for _, k := range keys {
			if s.cpuMetrics[k].exist {
				s.cpuMetrics[k].exist = false
			} else {
				delete(s.cpuMetrics, k)
				klog.InfoS("delete CPU metrics", "key", k)
			}
		}

		<-t.C
	}
}
