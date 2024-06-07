package podmanager

import (
	"context"
	"math"
	"strconv"
	"time"

	statsv1 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

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
	quota = min(quota, types.DefaultMaxCPULimit)
	return uint64(math.Ceil(quota * float64(types.DefaultCPUPeriod)))
}

func (s *CPUScaler) update(pInfo *PodInfo, quota uint64, metrics *CPUMetrics, state types.CPUState) {
	_quota, err := s.podmanager.UpdatePodCPUResource(pInfo, quota)
	if err != nil {
		klog.ErrorS(err, "failed to update resource")
	}

	if _quota != quota {
		klog.Warningf("request quota=%d, got quota=%d",
			quota, _quota,
		)
	}

	pInfo.CPUState = state

	metrics.LastQuota = metrics.CurrentQuota
	metrics.CurrentQuota = _quota
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

func (s *CPUScaler) Start(ctx context.Context) {

	klog.InfoS("CPU Scaler started")
	timer := time.NewTimer(time.Second)

	for {
		timer.Reset(time.Second)
		localPods := s.podmanager.ListLocalPods()

		for _, pod := range localPods {

			key, _ := cache.MetaNamespaceKeyFunc(pod)
			pInfo := s.podmanager.PodInfo(pod)
			if pInfo == nil {
				delete(s.cpuMetrics, key)
				continue
			}

			state := pod.GetLabels()[types.STATE_LABEL]
			endpoint := pod.GetLabels()[types.ENDPOINT_LABEL]
			throttleTarget, err := strconv.ParseFloat(pod.GetLabels()["swiftkube.io/throttle-target"], 64)
			if err != nil {
				throttleTarget = 0.1
				klog.ErrorS(err, "failed to parse throttle target to float64")
			}

			var metrics *CPUMetrics
			if m, ok := s.cpuMetrics[key]; ok {
				m.exist = true
				metrics = m
			} else {
				metrics = NewCPUMetrics(types.DefaultMaxHistoryLength)
				s.cpuMetrics[key] = metrics
			}

			podMetrics, err := pInfo.Cgroup.Containerd.Stat()

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
			if state == types.RR { // Ready-Running
				quota := s.dynCalculateQuota(metrics, throttleTarget)
				s.update(pInfo, quota, metrics, types.CPU_DYNAMIC_OVERPROVISION)

			} else if state == types.Init || state == types.WU || state == types.RFS { // Initializing WarmingUp Ready-FullSpeed
				quota := uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
				s.update(pInfo, quota, metrics, types.CPU_MAX)

			} else if state == types.RCN || state == types.RLN { // Ready-CatNap Ready-LongNap
				if endpoint == string(types.ENDPOINT_DOWN) {
					quota := s.dynCalculateQuota(metrics, 0.7)
					s.update(pInfo, quota, metrics, types.CPU_MAX)
				} else {
					quota := uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
					// quota := s.dynCalculateQuota(metrics, 0.7)
					s.update(pInfo, quota, metrics, types.CPU_DYNAMIC_RESOURCE_EFFICIENT)
				}
			}
		}

		s.cleanupMetrics()
		<-timer.C
	}
}
