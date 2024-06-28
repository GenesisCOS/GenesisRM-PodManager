package podmanager

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	statsv1 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"swiftkube.io/swiftkube/pkg/helper"
	"swiftkube.io/swiftkube/pkg/podmanager/sample"
	"swiftkube.io/swiftkube/pkg/podmanager/types"
)

type CPUMetrics struct {
	exist bool

	PodMetrics     *statsv1.Metrics
	PrevPodMetrics *statsv1.Metrics
	Timestamp      time.Time

	LastQuota    uint64
	CurrentQuota uint64

	scaleDown bool
	margin    float64

	usageHistory          sample.Sample
	throttlingRateHistory sample.Sample
}

func NewCPUMetrics(maxLength int) *CPUMetrics {
	return &CPUMetrics{
		exist:                 true,
		PodMetrics:            nil,
		PrevPodMetrics:        nil,
		LastQuota:             0,
		usageHistory:          sample.NewFixLengthSample(maxLength),
		throttlingRateHistory: sample.NewFixLengthSample(maxLength),
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

func calculateThrottlingRate(cur, prev *statsv1.Metrics) float64 {
	return float64(cur.GetCPU().GetNrThrottled()-prev.GetCPU().GetNrThrottled()) /
		float64(cur.GetCPU().GetNrPeriods()-prev.GetCPU().GetNrPeriods())
}

func calculateUsage(cur, prev *statsv1.Metrics, durationUsec int64) float64 {
	return float64(cur.GetCPU().GetUsageUsec()-prev.GetCPU().GetUsageUsec()) / float64(durationUsec)
}

var alpha float64 = 1
var betaMax float64 = 0.9
var betaMin float64 = 0.5

func (s *CPUScaler) dynCalculateQuota(metrics *CPUMetrics, throttleTarget float64) uint64 {
	var quota float64
	throttleRatio := metrics.throttlingRateHistory.Last()
	metrics.margin = max(0, metrics.margin+throttleRatio-throttleTarget)
	if throttleRatio > alpha*throttleTarget { // scale up
		currentQuota := float64(metrics.CurrentQuota) / float64(types.DefaultCPUPeriod)
		if metrics.scaleDown {
			lastQuota := float64(metrics.LastQuota) / float64(types.DefaultCPUPeriod)
			quota = lastQuota + (lastQuota - currentQuota)
		} else {
			quota = currentQuota * (1 + throttleRatio - alpha*throttleTarget)
		}
		metrics.scaleDown = false
	} else { // scale down
		proposed := metrics.usageHistory.Max() + metrics.margin*metrics.usageHistory.Stdev()
		currentQuota := float64(metrics.CurrentQuota) / float64(types.DefaultCPUPeriod)
		if proposed <= betaMax*currentQuota {
			quota = max(betaMin*currentQuota, proposed)
		} else {
			quota = currentQuota
		}
		metrics.scaleDown = true
	}
	quota = max(quota, types.DefaultMinCPULimit)
	return uint64(math.Ceil(quota * float64(types.DefaultCPUPeriod)))
}

func (s *CPUScaler) update(pInfo *PodInfo, quota uint64, metrics *CPUMetrics, state types.PodCPUState) {
	err := s.podmanager.UpdatePodCPUQuota(pInfo, quota)
	if err != nil {
		klog.ErrorS(err, "failed to update resource")
	}

	pInfo.CPUState = state

	metrics.LastQuota = metrics.CurrentQuota
	metrics.CurrentQuota = quota
}

func (s *CPUScaler) cleanupMetrics() {
	keys := make([]string, 0, len(s.cpuMetrics))
	for k := range s.cpuMetrics {
		keys = append(keys, k)
	}
	for _, k := range keys {
		if s.cpuMetrics[k].exist {
			s.cpuMetrics[k].exist = false
		} else {
			delete(s.cpuMetrics, k)
		}
	}
}

type updateAction struct {
	pInfo              *PodInfo
	quota              uint64
	request            uint64
	metrics            *CPUMetrics
	state              types.PodCPUState
	ignoreResourcePool bool
	cpuset             string
}

func (s *CPUScaler) Start(ctx context.Context) {

	klog.InfoS("CPU Scaler started")
	timer := time.NewTimer(time.Second)

	for {
		timer.Reset(time.Second)
		localPods, err := s.podmanager.ListControlledLocalPods()
		if err != nil {
			klog.Error(err)
			<-timer.C
			continue
		}

		// 包含LC Pod和LC Mix Pod的UpdateAction
		updateActions := make([]*updateAction, 0)
		bePods := make([]*PodInfo, 0)
		beMixPods := make([]*PodInfo, 0)

		for _, pod := range localPods {

			key, _ := cache.MetaNamespaceKeyFunc(pod)
			pInfo := s.podmanager.PodInfo(pod)
			if pInfo == nil {
				delete(s.cpuMetrics, key)
				continue
			}

			serviceType := helper.GetPodServiceType(pInfo.Pod)
			cpuset, ok := pod.GetLabels()[types.CPUSET_LABEL]
			if !ok {
				if serviceType == types.SERVICE_TYPE_BE {
					cpuset = types.CPUSET_BE
				} else if serviceType == types.SERVICE_TYPE_LC {
					cpuset = types.CPUSET_LC
				} else {
					klog.ErrorS(fmt.Errorf("unknown service types %s", serviceType), "", "pod", pod.GetName(), "namespace", pod.GetNamespace())
					continue
				}
			}

			state := pod.GetLabels()[types.STATE_LABEL]
			endpoint := pod.GetLabels()[types.ENDPOINT_LABEL]

			// 不去计算BE任务的CPU quota
			if serviceType == types.SERVICE_TYPE_BE {
				delete(s.cpuMetrics, key)
				if cpuset == types.CPUSET_BE {
					bePods = append(bePods, pInfo)
				} else if cpuset == types.CPUSET_MIX {
					beMixPods = append(beMixPods, pInfo)
				}
				continue
			}

			cpuRequestStr, ok := pod.GetLabels()["swiftkube.io/cpu-request"]
			if !ok {
				cpuRequestStr = "3000"
			}
			cpuRequest, err := strconv.ParseUint(cpuRequestStr, 10, 64)
			if err != nil {
				klog.Error(err)
				cpuRequest = 3000
			}
			requestQuota := (float64(cpuRequest) / 1000) * float64(types.DefaultCPUPeriod)

			// 是否所有容器都ready了
			allReady := true
			for _, containerStatus := range pInfo.Pod.Status.ContainerStatuses {
				if !containerStatus.Ready {
					allReady = false
					break
				}
			}

			throttleTarget := helper.GetPodThrottleTarget(pInfo.Pod)

			var metrics *CPUMetrics
			if m, ok := s.cpuMetrics[key]; ok {
				m.exist = true
				metrics = m
			} else {
				metrics = NewCPUMetrics(types.DefaultMaxHistoryLength)
				s.cpuMetrics[key] = metrics
			}

			podMetrics, err := pInfo.Cgroup.Control.Stat()

			if err != nil {
				klog.ErrorS(err, "failed to stat")
				s.cpuMetrics[key].PodMetrics = nil
			} else {
				prevPodMetrics := s.cpuMetrics[key].PodMetrics
				prevTimestamp := s.cpuMetrics[key].Timestamp
				timestamp := time.Now()
				durationUsec := timestamp.Sub(prevTimestamp).Microseconds()

				if prevPodMetrics != nil {
					// throttling rate (nr_throttled / nr_period)
					throttlingRate := calculateThrottlingRate(podMetrics, prevPodMetrics)
					s.cpuMetrics[key].throttlingRateHistory.Update(throttlingRate)

					// CPU total usage
					usage := calculateUsage(podMetrics, prevPodMetrics, durationUsec)
					s.cpuMetrics[key].usageHistory.Update(usage)
				}

				s.cpuMetrics[key].Timestamp = timestamp
				s.cpuMetrics[key].PrevPodMetrics = prevPodMetrics
				s.cpuMetrics[key].PodMetrics = podMetrics

				if prevPodMetrics == nil {
					continue // 处理下一个Pod
				}
			}

			if metrics.LastQuota == 0 {
				metrics.LastQuota = uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
				metrics.CurrentQuota = uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
			}

			if cpuset == types.CPUSET_MIX {
				quota := uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
				updateActions = append(updateActions, &updateAction{
					pInfo:              pInfo,
					quota:              quota,
					metrics:            metrics,
					state:              types.CPU_MAX,
					ignoreResourcePool: false,
					cpuset:             cpuset,
				})
				continue
			}

			if allReady {
				if state == types.RR { // Ready-Running
					quota := s.dynCalculateQuota(metrics, throttleTarget)
					quota = min(uint64(math.Ceil(float64(types.DefaultCPUPeriod)*types.DefaultMaxCPULimit)), quota)
					updateActions = append(updateActions, &updateAction{
						pInfo:              pInfo,
						quota:              quota,
						request:            uint64(requestQuota),
						metrics:            metrics,
						state:              types.CPU_DYNAMIC_OVERPROVISION,
						ignoreResourcePool: false,
						cpuset:             cpuset,
					})

				} else if state == types.Init || state == types.WU { // Initializing WarmingUp Ready-FullSpeed
					quota := uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
					updateActions = append(updateActions, &updateAction{
						pInfo:              pInfo,
						quota:              quota,
						request:            uint64(requestQuota),
						metrics:            metrics,
						state:              types.CPU_MAX,
						ignoreResourcePool: true,
						cpuset:             cpuset,
					})

				} else if state == types.RFS {
					quota := uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
					updateActions = append(updateActions, &updateAction{
						pInfo:              pInfo,
						quota:              quota,
						request:            uint64(requestQuota),
						metrics:            metrics,
						state:              types.CPU_MAX,
						ignoreResourcePool: false,
						cpuset:             cpuset,
					})

				} else if state == types.RCN || state == types.RLN { // Ready-CatNap Ready-LongNap
					if endpoint == string(types.ENDPOINT_DOWN) {
						quota := s.dynCalculateQuota(metrics, 0.7)
						quota = min(uint64(math.Ceil(float64(types.DefaultCPUPeriod)*types.DefaultMaxCPULimit)), quota)
						updateActions = append(updateActions, &updateAction{
							pInfo:              pInfo,
							quota:              quota,
							request:            uint64(requestQuota),
							metrics:            metrics,
							state:              types.CPU_DYNAMIC_RESOURCE_EFFICIENT,
							ignoreResourcePool: false,
							cpuset:             types.CPUSET_BE, // RCN与RLN Pod自动转为BE cpuset
						})
					} else {
						quota := uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
						updateActions = append(updateActions, &updateAction{
							pInfo:              pInfo,
							quota:              quota,
							request:            uint64(requestQuota),
							metrics:            metrics,
							state:              types.CPU_MAX,
							ignoreResourcePool: false,
							cpuset:             cpuset,
						})
					}
				}
			} else {
				// 如果Pod中存在容器没有ready，我们希望容器能够快速完成初始化
				quota := uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
				updateActions = append(updateActions, &updateAction{
					pInfo:              pInfo,
					quota:              quota,
					request:            uint64(requestQuota),
					metrics:            metrics,
					state:              types.CPU_MAX,
					ignoreResourcePool: true,
					cpuset:             cpuset,
				})
			}
		}

		var totalRequestLCCores float64 = 0
		var totalRequestMixCores float64 = 0
		// updateActions 中全部为 LC Pod
		for _, action := range updateActions {
			allocateCores := float64(action.quota) / float64(types.DefaultCPUPeriod)
			requestCores := float64(action.request) / float64(types.DefaultCPUPeriod)
			switch action.cpuset {
			case types.CPUSET_MIX:
				totalRequestMixCores += min(requestCores, allocateCores) // MIX cpuset中的LC Pod
			case types.CPUSET_LC:
				totalRequestLCCores += min(requestCores, allocateCores) // LC cpuset中的LC Pod
			default:
			}
		}

		if totalRequestLCCores > float64(s.podmanager.Cores) {
			totalRequestLCCores = float64(s.podmanager.Cores)
		}

		totalRequestLCCoresInt := uint64(math.Ceil(totalRequestLCCores))
		// update LC pods cpu resources
		for _, action := range updateActions {
			if action.cpuset == types.CPUSET_LC {
				s.podmanager.UpdatePodCPUSetFromLower(action.pInfo, totalRequestLCCoresInt)
			}
			s.update(action.pInfo, action.quota, action.metrics, action.state)
		}

		// 混部区域，该cpuset即存在LC任务也存在BE任务
		totalRequestMixCoresInt := uint64(math.Ceil(totalRequestMixCores))
		mixCpusetRangeStart := totalRequestLCCoresInt
		mixCpusetRangeEnd := min(s.podmanager.Cores-1, totalRequestLCCoresInt+totalRequestMixCoresInt-1)
		for _, action := range updateActions {
			if mixCpusetRangeEnd >= mixCpusetRangeStart {
				if action.cpuset == types.CPUSET_MIX {
					s.podmanager.UpdatePodCPUSetRange(action.pInfo, mixCpusetRangeStart, mixCpusetRangeEnd)
				}
				s.update(
					action.pInfo,
					uint64(math.Ceil(float64(types.DefaultCPUPeriod)*types.DefaultMaxCPULimit)),
					action.metrics,
					action.state,
				)
			} else {
				if action.cpuset == types.CPUSET_LC {
					s.podmanager.UpdatePodCPUSetFromLower(action.pInfo, totalRequestLCCoresInt)
				}
				s.update(action.pInfo, action.quota, action.metrics, action.state)
			}
		}

		// update BE pods cpu resources
		beCores := s.podmanager.Cores - totalRequestLCCoresInt - totalRequestMixCoresInt
		// 至少分配 total cpus * 0.03 的核给 BE cpuset
		minBeCores := uint64(math.Ceil(float64(s.podmanager.Cores) * 0.03))
		beCores = max(beCores, minBeCores)

		mixCores := s.podmanager.Cores - mixCpusetRangeStart
		mixCores = max(mixCores, minBeCores)

		for _, action := range updateActions {
			if action.cpuset == types.CPUSET_BE {
				s.podmanager.UpdatePodCPUSetFromUpper(action.pInfo, mixCores)
			}
		}

		for _, beMixPod := range beMixPods {
			s.podmanager.UpdatePodCPUSetFromUpper(beMixPod, mixCores)
		}

		for _, bePod := range bePods {
			s.podmanager.UpdatePodCPUSetFromUpper(bePod, beCores)
		}

		// klog.InfoS("cpusets", "LC", totalRequestLCCoresInt, "MIX", totalRequestMixCoresInt, "BE", beCores)

		s.cleanupMetrics()
		<-timer.C
	}
}
