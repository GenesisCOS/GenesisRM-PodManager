package podmanager

import (
	"context"
	"math"
	"strconv"
	"time"

	statsv1 "github.com/containerd/cgroups/stats/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"swiftkube.io/swiftkube/pkg/podmanager/sample"
	"swiftkube.io/swiftkube/pkg/podmanager/types"
)

type CPUMetrics struct {
	exist bool

	PodMetrics *statsv1.Metrics
	Timestamp  time.Time

	LastQuota    uint64
	CurrentQuota uint64

	scaleDown bool
	margin    float64

	usageHistory        sample.Sample
	throttleRateHistory sample.Sample
}

func NewCPUMetrics(maxLength int) *CPUMetrics {
	return &CPUMetrics{
		exist:               true,
		LastQuota:           0,
		usageHistory:        sample.NewFixLengthSample(maxLength),
		throttleRateHistory: sample.NewFixLengthSample(maxLength),
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

var alpha float64 = 3
var betaMax float64 = 0.9
var betaMin float64 = 0.5

func (s *CPUScaler) dynCalculateQuota(metrics *CPUMetrics, throttleTarget float64) uint64 {
	var quota float64
	throttleRatio := metrics.throttleRateHistory.Last()
	metrics.margin = max(0, metrics.margin+throttleRatio-throttleTarget)
	if throttleRatio > alpha*throttleTarget { // scale up
		if metrics.scaleDown {
			lastQuota := float64(metrics.LastQuota) / float64(types.DefaultCPUPeriod)
			quota = lastQuota + (lastQuota - quota)
			metrics.margin = metrics.margin + throttleRatio - throttleTarget
		} else {
			currentQuota := float64(metrics.CurrentQuota) / float64(types.DefaultCPUPeriod)
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
					quota := uint64(math.Ceil(float64(types.DefaultCPUPeriod) * types.DefaultMaxCPULimit))
					s.update(pInfo, quota, metrics, types.CPU_DYNAMIC_RESOURCE_EFFICIENT)
				} else {
					quota := s.dynCalculateQuota(metrics, throttleTarget)
					s.update(pInfo, quota, metrics, types.CPU_MAX)
				}
			}

			podMetrics, err := pInfo.Cgroup.Containerd.Stat()

			if err != nil {
				klog.ErrorS(err, "failed to stat")
				s.cpuMetrics[key].PodMetrics = nil
			} else {
				prevPodMetrics := s.cpuMetrics[key].PodMetrics
				prevTimestamp := s.cpuMetrics[key].Timestamp
				timestamp := time.Now()
				duration := timestamp.Sub(prevTimestamp).Nanoseconds()

				if prevPodMetrics != nil {
					throttlingRate := float64(prevPodMetrics.CPU.Throttling.ThrottledPeriods-podMetrics.CPU.Throttling.ThrottledPeriods) /
						float64(prevPodMetrics.CPU.Throttling.Periods-podMetrics.CPU.Throttling.Periods)
					s.cpuMetrics[key].throttleRateHistory.Update(throttlingRate)

					usage := float64(podMetrics.CPU.Usage.Total-prevPodMetrics.CPU.Usage.Total) / float64(duration)
					s.cpuMetrics[key].usageHistory.Update(usage)
				}

				s.cpuMetrics[key].Timestamp = timestamp
				s.cpuMetrics[key].PodMetrics = podMetrics
			}
		}

		s.cleanupMetrics()

		<-timer.C
	}
}
