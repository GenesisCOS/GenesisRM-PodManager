package podmanager

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"time"

	statsv1 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"swiftkube.io/swiftkube/pkg/helper"
	"swiftkube.io/swiftkube/pkg/podmanager/sample"
	genesissdk "swiftkube.io/swiftkube/pkg/podmanager/sdk"
)

var swapfiles []string

type MemoryControl struct {
	high int64
}

type CPUControl struct {
	quota uint64
}

type PerPodMetrics struct {
	exist         bool
	PrevState     genesissdk.PodState
	PodStats      *statsv1.Metrics
	PrevPodStats  *statsv1.Metrics
	Timestamp     time.Time
	cpumetrics    *CPUMetrics
	memorymetrics *MemoryMetrics
	RLNTimestamp  *time.Time
}

type CPUMetrics struct {
	LastQuota    uint64
	CurrentQuota uint64

	scaleDown          bool
	margin             float64
	lastScaleTimestamp time.Time

	usageHistory         sample.Sample
	throttledRateHistory sample.Sample
}

type MemoryMetrics struct {
	CurrentHigh int64
}

func NewCPUMetrics() *CPUMetrics {
	return &CPUMetrics{
		LastQuota:            0,
		CurrentQuota:         0,
		lastScaleTimestamp:   time.Now(),
		usageHistory:         sample.NewFixLengthSample(50),
		throttledRateHistory: sample.NewFixLengthSample(10),
	}
}

func NewMemoryMetrics() *MemoryMetrics {
	return &MemoryMetrics{
		CurrentHigh: -1,
	}
}

type ResourceController struct {
	podmanager *PodManager

	metrics map[string]*PerPodMetrics
}

func NewResourceController(podmanager *PodManager) *ResourceController {
	return &ResourceController{
		podmanager: podmanager,
		metrics:    make(map[string]*PerPodMetrics),
	}
}

func calculateThrottledRate(cur, prev *statsv1.Metrics) float64 {
	//return float64(cur.GetCPU().GetNrThrottled()-prev.GetCPU().GetNrThrottled()) /
	//	float64(cur.GetCPU().GetNrPeriods()-prev.GetCPU().GetNrPeriods())
	return float64(cur.GetCPU().GetNrThrottled() - prev.GetCPU().GetNrThrottled())
}

func calculateUsage(cur, prev *statsv1.Metrics, durationUsec int64) float64 {
	return float64(cur.GetCPU().GetUsageUsec()-prev.GetCPU().GetUsageUsec()) / float64(durationUsec)
}

func (s *ResourceController) dynCalculateQuota(metrics *PerPodMetrics, throttleTarget float64) uint64 {
	var quota float64
	throttledRate := metrics.cpumetrics.throttledRateHistory.Mean()
	currentQuota := float64(metrics.cpumetrics.CurrentQuota) / float64(genesissdk.DefaultCPUPeriod)
	lastQuota := float64(metrics.cpumetrics.LastQuota) / float64(genesissdk.DefaultCPUPeriod)
	quota = currentQuota
	if throttledRate > 3*throttleTarget && metrics.cpumetrics.scaleDown {
		quota = 2*lastQuota - currentQuota
		//quota = genesissdk.DefaultMaxCPULimit
		metrics.cpumetrics.margin += throttledRate - throttleTarget
		for i := 0; i < 10; i++ {
			metrics.cpumetrics.throttledRateHistory.Update(0)
		}
		metrics.cpumetrics.scaleDown = false
	}
	now := time.Now()
	if now.Sub(metrics.cpumetrics.lastScaleTimestamp).Seconds() >= 1 {
		throttledRate = metrics.cpumetrics.throttledRateHistory.Mean()
		usageMax := metrics.cpumetrics.usageHistory.Max()
		usageStdev := metrics.cpumetrics.margin * metrics.cpumetrics.usageHistory.Stdev()
		metrics.cpumetrics.margin += throttledRate - throttleTarget
		metrics.cpumetrics.margin = max(0, metrics.cpumetrics.margin)
		metrics.cpumetrics.scaleDown = false
		if throttledRate > 3*throttleTarget { // scale up
			quota = currentQuota * (1 + (throttledRate - 3*throttleTarget))
		} else { // scale down
			proposed := usageMax + usageStdev*metrics.cpumetrics.margin
			if proposed <= 0.9*currentQuota {
				quota = max(0.5*currentQuota, proposed)
				metrics.cpumetrics.scaleDown = true
			}
		}
		for i := 0; i < 10; i++ {
			metrics.cpumetrics.throttledRateHistory.Update(0)
		}
		metrics.cpumetrics.lastScaleTimestamp = now
	}

	//quota = max(quota, genesissdk.DefaultMinCPULimit)
	quota = max(0.1, quota)
	return uint64(math.Ceil(quota * float64(genesissdk.DefaultCPUPeriod)))
}

func (s *ResourceController) reloadSwappages(pids []uint64, swapfiles []string) {
	if len(pids) == 0 {
		return
	}

	params := make([]string, 0)
	for _, pid := range pids {
		params = append(params, "-p")
		params = append(params, strconv.FormatUint(pid, 10))
	}

	for _, swapfile := range swapfiles {
		params = append(params, "-f")
		params = append(params, swapfile)
	}

	cmd := exec.Command(
		"/usr/local/bin/reloadswappage", params...,
	)

	err := cmd.Run()
	if err != nil {
		klog.Error(err)
		return
	}
	code := cmd.ProcessState.ExitCode()
	if code != 0 {
		klog.Error(fmt.Errorf("reloadswappage return non-zero (code = %d)", code))
	}
}

func (s *ResourceController) update(action *updateAction) {
	if action.cpucontrol.quota != action.metrics.cpumetrics.CurrentQuota {
		err := s.podmanager.UpdatePodCPUQuota(action.pInfo, action.cpucontrol.quota)
		if err != nil {
			klog.ErrorS(err, "failed to update CPU quota")
		}
		klog.InfoS("update cpu.max", "name", action.pInfo.Pod.Name, "quota", action.cpucontrol.quota)
	}

	action.pInfo.CPUState = action.state
	action.metrics.cpumetrics.LastQuota = action.metrics.cpumetrics.CurrentQuota
	action.metrics.cpumetrics.CurrentQuota = action.cpucontrol.quota

	if action.memorycontrol.high != action.metrics.memorymetrics.CurrentHigh {
		err := s.podmanager.UpdatePodMemoryHigh(action.pInfo, action.memorycontrol.high)
		if err != nil {
			klog.ErrorS(err, "failed to update Memory high")
			return
		}

		klog.InfoS("update memory.high", "name", action.pInfo.Pod.Name, "quota", action.memorycontrol.high)
		/*
			if action.memorycontrol.high > 0 {
				pids, err := action.pInfo.Cgroup.Control.Procs(true)
				if err != nil {
					klog.Error(err)
					return
				}
				if len(pids) > 0 {
					//s.reloadSwappages(pids, swapfiles)
				}
			}
		*/
	}

	action.metrics.memorymetrics.CurrentHigh = action.memorycontrol.high

}

func (s *ResourceController) cleanupMetrics() {
	keys := make([]string, 0, len(s.metrics))
	for k := range s.metrics {
		keys = append(keys, k)
	}
	for _, k := range keys {
		if s.metrics[k].exist {
			s.metrics[k].exist = false
		} else {
			delete(s.metrics, k)
		}
	}
}

type updateAction struct {
	pInfo         *PodInfo
	request       uint64
	metrics       *PerPodMetrics
	state         genesissdk.PodCPUState
	cpuset        string
	cpucontrol    *CPUControl
	memorycontrol *MemoryControl
}

type SwapfileConfig struct {
	Swapfiles []string `yaml:"swapfiles"`
}

func (s *ResourceController) Start(ctx context.Context) {

	klog.InfoS("Resource controller started")
	timer := time.NewTimer(time.Second)

	var prevTotalRequestLCCoresInt uint64 = 0
	var prevTotalRequestMixCoresInt uint64 = 0

	// 读取swapfiles.yaml配置文件
	conf, err := os.Open("/etc/podmanager/swapfiles.yaml")
	if err != nil {
		klog.Fatal(err)
	}
	decoder := yaml.NewDecoder(conf)
	var swapfileConfig SwapfileConfig
	err = decoder.Decode(&swapfileConfig)
	if err != nil {
		klog.Fatal(err)
	}
	conf.Close()
	swapfiles = swapfileConfig.Swapfiles

	// 主循环
	for {
		//timer.Reset(time.Second)
		timer.Reset(time.Microsecond * time.Duration(genesissdk.DefaultCPUPeriod))
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
				delete(s.metrics, key)
				continue // 处理下一个Pod
			}

			serviceType := helper.GetPodServiceType(pInfo.Pod)
			cpuset, ok := pod.GetLabels()[genesissdk.CPUSET_LABEL]
			// 如果pod没有cpuset标签，则根据服务类型设置默认cpuset
			// 例如：BE服务设置cpuset为BE cpuset,LC服务设置cpuset为LC cpuset
			if !ok {
				if serviceType == genesissdk.SERVICE_TYPE_BE {
					cpuset = genesissdk.CPUSET_BE
				} else if serviceType == genesissdk.SERVICE_TYPE_LC {
					cpuset = genesissdk.CPUSET_LC
				} else {
					klog.ErrorS(fmt.Errorf("unknown service types %s", serviceType), "", "pod", pod.GetName(), "namespace", pod.GetNamespace())
					continue // 处理下一个Pod
				}
			}

			endpoint := helper.GetPodEndpointState(pod)
			state := helper.GetPodState(pod)

			// 不去计算BE任务的CPU quota
			if serviceType == genesissdk.SERVICE_TYPE_BE {
				delete(s.metrics, key)
				if cpuset == genesissdk.CPUSET_BE {
					bePods = append(bePods, pInfo)
				} else if cpuset == genesissdk.CPUSET_MIX {
					beMixPods = append(beMixPods, pInfo)
				}
				continue // 处理下一个Pod
			}

			// cpuRequest单位为毫核
			cpuRequest, _ := helper.GetPodCPURequestOrDefault(pod, 3000)
			requestQuota := (float64(cpuRequest) / 1000) * float64(genesissdk.DefaultCPUPeriod)

			// 是否所有容器都ready了
			allReady := true
			for _, containerStatus := range pInfo.Pod.Status.ContainerStatuses {
				if !containerStatus.Ready {
					allReady = false
					break
				}
			}

			// throttleTarget := helper.GetPodThrottleTarget(pInfo.Pod)

			var metrics *PerPodMetrics
			if m, ok := s.metrics[key]; ok {
				m.exist = true
				metrics = m
			} else {
				metrics = &PerPodMetrics{
					exist:         true,
					PrevPodStats:  nil,
					PodStats:      nil,
					cpumetrics:    NewCPUMetrics(),
					memorymetrics: NewMemoryMetrics(),
				}
				s.metrics[key] = metrics
			}

			podMetrics, err := pInfo.Cgroup.Control.Stat()
			if err != nil {
				klog.ErrorS(err, "failed to stat")
				metrics.PodStats = nil
				continue
			}

			prevPodMetrics := metrics.PodStats
			prevTimestamp := metrics.Timestamp
			timestamp := time.Now()
			durationUsec := timestamp.Sub(prevTimestamp).Microseconds()

			if prevPodMetrics != nil {
				metrics.cpumetrics.throttledRateHistory.Update(calculateThrottledRate(podMetrics, prevPodMetrics))
				metrics.cpumetrics.usageHistory.Update(calculateUsage(podMetrics, prevPodMetrics, durationUsec))
			}

			metrics.Timestamp = timestamp
			metrics.PrevPodStats = prevPodMetrics
			metrics.PodStats = podMetrics

			if prevPodMetrics == nil {
				continue // 处理下一个Pod
			}

			if metrics.cpumetrics.LastQuota == 0 {
				currentQuota, _, err := pInfo.Cgroup.GetCPUQuotaAndPeriod()
				if err != nil {
					klog.ErrorS(err, "get pod CPU quota and period failed")
					// 因为我门不知道Pod的当前Quota，所以要保证下一次分配quota不会跟CurrentQuota一样
					metrics.cpumetrics.CurrentQuota = math.MaxUint64
				} else {
					metrics.cpumetrics.CurrentQuota = uint64(currentQuota)
				}
				metrics.cpumetrics.LastQuota = genesissdk.CPUQuotaUnlimited
			}

			if cpuset == genesissdk.CPUSET_MIX {
				quota := uint64(math.Ceil(float64(genesissdk.DefaultCPUPeriod) * genesissdk.DefaultMaxCPULimit))
				updateActions = append(updateActions, &updateAction{
					pInfo:   pInfo,
					metrics: metrics,
					state:   genesissdk.CPU_MAX,
					cpuset:  cpuset,
					cpucontrol: &CPUControl{
						quota: quota,
					},
					memorycontrol: &MemoryControl{
						high: genesissdk.DefaultMemoryHigh,
					},
				})
				continue // 处理下一个Pod
			}

			if allReady {
				switch state {
				case genesissdk.POD_READY_RUNNING_STATE:
					metrics.RLNTimestamp = nil
					quota := s.dynCalculateQuota(metrics, 0.0)
					// quota = min(uint64(requestQuota), quota)
					quota = uint64(requestQuota)
					updateActions = append(updateActions, &updateAction{
						pInfo:   pInfo,
						request: uint64(requestQuota),
						metrics: metrics,
						state:   genesissdk.CPU_DYNAMIC_OVERPROVISION,
						cpuset:  cpuset,
						cpucontrol: &CPUControl{
							quota: quota,
						},
						memorycontrol: &MemoryControl{
							high: genesissdk.DefaultMemoryHigh,
						},
					})
				case genesissdk.POD_INITIALIZING_STATE:
					metrics.RLNTimestamp = nil
					updateActions = append(updateActions, &updateAction{
						pInfo:   pInfo,
						request: uint64(requestQuota),
						metrics: metrics,
						state:   genesissdk.CPU_MAX,
						cpuset:  genesissdk.CPUSET_BE, // 不要浪费LC资源
						cpucontrol: &CPUControl{
							quota: genesissdk.CPUQuotaUnlimited,
						},
						memorycontrol: &MemoryControl{
							high: genesissdk.DefaultMemoryHigh,
						},
					})
				case genesissdk.POD_WARMINGUP_STATE:
					metrics.RLNTimestamp = nil
					quota := uint64(math.Ceil(float64(genesissdk.DefaultCPUPeriod) * genesissdk.DefaultMaxCPULimit))
					updateActions = append(updateActions, &updateAction{
						pInfo:   pInfo,
						request: uint64(requestQuota),
						metrics: metrics,
						state:   genesissdk.CPU_MAX,
						cpuset:  genesissdk.CPUSET_LC, // 尽快预热
						cpucontrol: &CPUControl{
							quota: quota,
						},
						memorycontrol: &MemoryControl{
							high: genesissdk.DefaultMemoryHigh,
						},
					})
				case genesissdk.POD_READY_FULLSPEED_STATE:
					metrics.RLNTimestamp = nil
					quota := s.dynCalculateQuota(metrics, 0.0)
					// quota := uint64(math.Ceil(float64(genesissdk.DefaultCPUPeriod) * (requestQuota / 1000)))
					quota = uint64(requestQuota)
					updateActions = append(updateActions, &updateAction{
						pInfo:   pInfo,
						request: uint64(requestQuota),
						metrics: metrics,
						state:   genesissdk.CPU_MAX,
						cpuset:  genesissdk.CPUSET_LC, // FullSpeed
						cpucontrol: &CPUControl{
							quota: quota,
						},
						memorycontrol: &MemoryControl{
							high: genesissdk.DefaultMemoryHigh,
						},
					})
				case genesissdk.POD_READY_CATNAP_STATE:
					metrics.RLNTimestamp = nil
					if endpoint == genesissdk.ENDPOINT_DOWN {
						//不节流，放在BE CPUSET上
						updateActions = append(updateActions, &updateAction{
							pInfo:   pInfo,
							request: uint64(requestQuota),
							metrics: metrics,
							state:   genesissdk.CPU_DYNAMIC_RESOURCE_EFFICIENT,
							cpuset:  genesissdk.CPUSET_BE, // RCN与RLN Pod自动转为BE cpuset
							cpucontrol: &CPUControl{
								quota: genesissdk.CPUQuotaUnlimited,
							},
							memorycontrol: &MemoryControl{
								high: genesissdk.DefaultMemoryHigh,
							},
						})

					} else {
						updateActions = append(updateActions, &updateAction{
							pInfo:   pInfo,
							request: uint64(requestQuota),
							metrics: metrics,
							state:   genesissdk.CPU_MAX,
							cpuset:  cpuset,
							cpucontrol: &CPUControl{
								quota: genesissdk.CPUQuotaUnlimited,
							},
							memorycontrol: &MemoryControl{
								high: genesissdk.DefaultMemoryHigh,
							},
						})
					}
				case genesissdk.POD_READY_LONGNAP_STATE:
					if endpoint == genesissdk.ENDPOINT_DOWN {
						memoryHigh := genesissdk.DefaultMemoryHigh
						if metrics.RLNTimestamp != nil && time.Since(*metrics.RLNTimestamp).Seconds() > 3.0 {
							memoryHigh = 0
						}
						if metrics.RLNTimestamp == nil {
							t := time.Now()
							metrics.RLNTimestamp = &t
						}
						//不节流，放在BE CPUSET上
						updateActions = append(updateActions, &updateAction{
							pInfo:   pInfo,
							request: uint64(requestQuota),
							metrics: metrics,
							state:   genesissdk.CPU_DYNAMIC_RESOURCE_EFFICIENT,
							cpuset:  genesissdk.CPUSET_BE, // RCN与RLN Pod自动转为BE cpuset
							cpucontrol: &CPUControl{
								quota: genesissdk.CPUQuotaUnlimited,
							},
							memorycontrol: &MemoryControl{
								high: memoryHigh,
							},
						})

					} else {
						metrics.RLNTimestamp = nil
						updateActions = append(updateActions, &updateAction{
							pInfo:   pInfo,
							request: uint64(requestQuota),
							metrics: metrics,
							state:   genesissdk.CPU_MAX,
							cpuset:  cpuset,
							cpucontrol: &CPUControl{
								quota: genesissdk.CPUQuotaUnlimited,
							},
							memorycontrol: &MemoryControl{
								high: genesissdk.DefaultMemoryHigh,
							},
						})
					}
				}
			} else {
				// 如果Pod中存在容器没有ready，我们希望容器能够快速完成初始化
				metrics.RLNTimestamp = nil
				updateActions = append(updateActions, &updateAction{
					pInfo:   pInfo,
					request: uint64(requestQuota),
					metrics: metrics,
					state:   genesissdk.CPU_MAX,
					cpuset:  genesissdk.CPUSET_BE, // 不要浪费LC资源
					cpucontrol: &CPUControl{
						quota: genesissdk.CPUQuotaUnlimited,
					},
					memorycontrol: &MemoryControl{
						high: genesissdk.DefaultMemoryHigh,
					},
				})
			}

			metrics.PrevState = state
		} // for _, pod := range localPods

		var totalRequestLCCores float64 = 0
		var totalRequestMixCores float64 = 0
		// updateActions 中全部为 LC Pod
		for _, action := range updateActions {
			allocateCores := float64(action.cpucontrol.quota) / float64(genesissdk.DefaultCPUPeriod)
			requestCores := float64(action.request) / float64(genesissdk.DefaultCPUPeriod)
			switch action.cpuset {
			case genesissdk.CPUSET_MIX:
				totalRequestMixCores += min(requestCores, allocateCores) // MIX cpuset中的LC Pod
			case genesissdk.CPUSET_LC:
				totalRequestLCCores += min(requestCores, allocateCores) // LC cpuset中的LC Pod
			default:
			}
		}

		if totalRequestLCCores > float64(s.podmanager.Cores) {
			totalRequestLCCores = float64(s.podmanager.Cores)
		}

		totalRequestLCCoresInt := uint64(math.Ceil(totalRequestLCCores))
		// 更新LC Pods CPU资源配置
		for _, action := range updateActions {
			if action.cpuset == genesissdk.CPUSET_LC {
				action.cpucontrol.quota = genesissdk.CPUQuotaUnlimited
				s.update(action)
				if totalRequestLCCoresInt != prevTotalRequestLCCoresInt {
					s.podmanager.UpdatePodCPUSetFromLower(action.pInfo, totalRequestLCCoresInt)
				}
			}
		}

		// 混部区域，该cpuset即存在LC任务也存在BE任务
		totalRequestMixCoresInt := uint64(math.Ceil(totalRequestMixCores))
		mixCpusetRangeStart := totalRequestLCCoresInt
		mixCpusetRangeEnd := min(s.podmanager.Cores-1, totalRequestLCCoresInt+totalRequestMixCoresInt-1)

		for _, action := range updateActions {
			if mixCpusetRangeEnd >= mixCpusetRangeStart && action.cpuset == genesissdk.CPUSET_MIX {
				if totalRequestMixCoresInt != prevTotalRequestMixCoresInt || totalRequestLCCoresInt != prevTotalRequestLCCoresInt {
					s.podmanager.UpdatePodCPUSetRange(action.pInfo, mixCpusetRangeStart, mixCpusetRangeEnd)
				}
				action.cpucontrol.quota = genesissdk.CPUQuotaUnlimited
				s.update(action)
			} else if mixCpusetRangeEnd < mixCpusetRangeStart && action.cpuset == genesissdk.CPUSET_MIX {
				if totalRequestLCCoresInt != prevTotalRequestLCCoresInt {
					s.podmanager.UpdatePodCPUSetFromLower(action.pInfo, totalRequestLCCoresInt)
				}
				action.cpucontrol.quota = genesissdk.CPUQuotaUnlimited
				s.update(action)
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
			if action.cpuset == genesissdk.CPUSET_BE {
				if totalRequestMixCoresInt != prevTotalRequestMixCoresInt || totalRequestLCCoresInt != prevTotalRequestLCCoresInt {
					s.podmanager.UpdatePodCPUSetFromUpper(action.pInfo, mixCores)
				}
				action.cpucontrol.quota = genesissdk.CPUQuotaUnlimited
				s.update(action)
			}
		}

		for _, beMixPod := range beMixPods {
			if totalRequestMixCoresInt != prevTotalRequestMixCoresInt || totalRequestLCCoresInt != prevTotalRequestLCCoresInt {
				s.podmanager.UpdatePodCPUSetFromUpper(beMixPod, mixCores)
			}
		}

		for _, bePod := range bePods {
			if totalRequestMixCoresInt != prevTotalRequestMixCoresInt || totalRequestLCCoresInt != prevTotalRequestLCCoresInt {
				s.podmanager.UpdatePodCPUSetFromUpper(bePod, beCores)
			}
		}

		prevTotalRequestLCCoresInt = totalRequestLCCoresInt
		prevTotalRequestMixCoresInt = totalRequestMixCoresInt
		s.cleanupMetrics()
		<-timer.C
	}
}
