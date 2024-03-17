package podmanager

import (
	"context"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	defaultMaxCPUQuota = 3000 // milli core
	defaultMinCPUQuota = 100  // milli core
)

type PerPodCPUScalerData struct {
	state string
	exist bool

	prevUsage     int64
	prevTimestamp int64
	history       []float64
}

type CPUScaler struct {
	manager *PodManager

	podMap map[string]*PerPodCPUScalerData
}

func (s *CPUScaler) Start(ctx context.Context) {

	for {
		t := time.NewTimer(500 * time.Millisecond)

		selector := labels.NewSelector()
		requirement, err := labels.NewRequirement("swiftkube.io/state", selection.Exists, []string{})
		if err != nil {
			klog.ErrorS(err, "Fail to construct requirement")
		}
		selector = selector.Add(*requirement)
		pods, err := s.manager.podLister.List(selector)
		if err != nil {
			klog.ErrorS(err, "Fail to list pods")
		}
		localPods := make([]*v1.Pod, 0)
		for _, pod := range pods {
			if pod.Spec.NodeName == s.manager.nodeName {
				localPods = append(localPods, pod.DeepCopy())
			}
		}

		for _, pod := range localPods {

			key, _ := cache.MetaNamespaceKeyFunc(pod)
			state := pod.GetLabels()["swiftkube.io/state"]
			endpoint := pod.GetLabels()["swiftkube.io/endpoint"]

			v, ok := s.manager.podMap.Load(key)
			if !ok {
				delete(s.podMap, key)
				continue
			}
			pInfo := v.(*PodInfo)

			var perPodData *PerPodCPUScalerData
			if data, ok := s.podMap[key]; ok {
				data.state = state
				data.exist = true
				perPodData = data
			} else {
				perPodData = &PerPodCPUScalerData{
					state:     state,
					exist:     true,
					prevUsage: -1,
					history:   make([]float64, 0),
				}
				s.podMap[key] = perPodData
			}
			cpuPeriod, err := pInfo.cg.GetCFSPeriod()
			if err != nil {
				klog.ErrorS(err, "Fail to read CPU period")
			}
			if state == "Ready-Running" {
				var maxUsage float64 = 0
				if len(perPodData.history) < 40 {
					maxUsage = defaultMaxCPUQuota / 1000
				} else {
					for _, v := range perPodData.history {
						maxUsage = max(maxUsage, v)
					}
				}
				maxUsage = max(maxUsage, defaultMinCPUQuota/1000)
				// TODO
				//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, int64(math.Ceil(float64(cpuPeriod)*maxUsage)))
				//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
				err = pInfo.cg.SetCFSQuota(cpuPeriod * (defaultMaxCPUQuota / 1000))
				if err != nil {
					klog.ErrorS(err, "Fail to set CPU quota")
				}
				pInfo.cpuState = CPU_DYNAMIC_OVERPROVISION
			} else if state == "Ready-FullSpeed" {
				//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
				err = pInfo.cg.SetCFSQuota(cpuPeriod * (defaultMaxCPUQuota / 1000))
				if err != nil {
					klog.ErrorS(err, "Fail to set CPU quota")
				}
				pInfo.cpuState = CPU_MAX
			} else if state == "Initializing" || state == "WarmingUp" {
				//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
				err = pInfo.cg.SetCFSQuota(cpuPeriod * (defaultMaxCPUQuota / 1000))
				if err != nil {
					klog.ErrorS(err, "Fail to set CPU quota")
				}
				pInfo.cpuState = CPU_MAX
			} else if state == "Ready-CatNap" || state == "Ready-LongNap" {
				if endpoint == string(ENDPOINT_DOWN) {
					var avgUsage float64 = 0
					if len(perPodData.history) > 0 {
						for _, v := range perPodData.history {
							avgUsage += v
						}
						avgUsage = avgUsage / float64(len(perPodData.history))
					} else {
						avgUsage = defaultMaxCPUQuota / 1000
					}
					// TODO
					//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, int64(math.Ceil(float64(cpuPeriod)*avgUsage/0.6)))
					//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
					err = pInfo.cg.SetCFSQuota(cpuPeriod * (defaultMaxCPUQuota / 1000))
					if err != nil {
						klog.ErrorS(err, "Fail to set CPU quota")
					}
					pInfo.cpuState = CPU_DYNAMIC_RESOURCE_EFFICIENT
				} else {
					//err = cgWriteInt64(pInfo.cfsQuotaUsFilePath, cpuPeriod*(initalizingCPUQuota/1000))
					err = pInfo.cg.SetCFSQuota(cpuPeriod * (defaultMaxCPUQuota / 1000))
					if err != nil {
						klog.ErrorS(err, "Fail to set CPU quota")
					}
					pInfo.cpuState = CPU_MAX
				}
			}

			var usage int64 = 0
			for _, containerInfo := range pInfo.containerInfos {
				//perContainerUsage, err := cgReadInt64(containerInfo.cpuacctUsageFilePath)
				perContainerUsage, err := containerInfo.cg.GetCPUAcctUsage()
				if err != nil {
					klog.ErrorS(err, "Fail to read CPU usage")
				}
				usage += perContainerUsage
			}

			// Year 2262
			timestamp := time.Now().UnixNano()

			prevUsage := s.podMap[key].prevUsage
			prevTimestamp := s.podMap[key].prevTimestamp
			if prevUsage != -1 {
				s.podMap[key].history = append(s.podMap[key].history, float64(usage-prevUsage)/float64(timestamp-prevTimestamp))
				if len(s.podMap[key].history) > 10 {
					length := len(s.podMap[key].history)
					s.podMap[key].history = s.podMap[key].history[length-10 : length]
				}
			}

			s.podMap[key].prevUsage = usage
			s.podMap[key].prevTimestamp = timestamp

			newPodMap := make(map[string]*PerPodCPUScalerData)
			for k, v := range s.podMap {
				if v.exist {
					v.exist = false
					newPodMap[k] = v
				}
			}
			s.podMap = newPodMap
		}

		<-t.C
	}
}

type MemoryScaler struct {
}
