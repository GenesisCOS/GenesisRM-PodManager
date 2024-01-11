package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	kuberest "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"swiftkube.io/swiftkube/pkg/signals"
)

const (
	cgroupPath                    = "/sys/fs/cgroup"
	bestEffortPodCgroupPath       = "/kubepods.slice/kubepods-besteffort.slice"
	burstablePodCgroupPath        = "/kubepods.slice/kubepods-burstable.slice"
	bestEffortPodCgroupPathPrefix = "/kubepods-besteffort-pod"
	burstablePodCgroupPathPrefix  = "/kubepods-burstable-pod"
	podCgroupPathSuffix           = ".slice"

	cpuacctUsageFile       = "cpuacct.usage"
	cfsPeriodUsFile        = "cpu.cfs_period_us"
	cfsQuotaUsFile         = "cpu.cfs_quota_us"
	memoryUsageInBytesFile = "memory.usage_in_bytes"
	memoryLimitInBytesFile = "memory.limit_in_bytes"
	memoryStatFile         = "memory.stat"

	cpuFamily    = "cpu"
	memoryFamily = "memory"

	swiftMonitorPodMemoryUsageInBytesName     = "swiftmonitor_pod_memory_usage_in_bytes"      // Read from memory.usage_in_bytes
	swiftMonitorPodMemoryStatUsageInBytesName = "swiftmonitor_pod_memory_stat_usage_in_bytes" // Read from memory.stat (cache + rss). Always 0
	swiftMonitorPodMemoryStatSwapInBytesName  = "swiftmonitor_pod_memory_stat_swap_in_bytes"  // Always 0
	swiftMonitorPodMemoryStatRssInBytesName   = "swiftmonitor_pod_memory_stat_rss_in_bytes"   // Always 0
	swiftMonitorPodMemoryStatCacheInBytesName = "swiftmonitor_pod_memory_stat_cache_in_bytes" // Always 0
	swiftMonitorPodCpuLimitName               = "swiftmonitor_pod_cpu_limit"

	swiftMonitorCpuLimitName               = "swiftmonitor_cpu_limit"
	swiftMonitorCpuUsageName               = "swiftmonitor_cpu_usage"
	swiftMonitorMemoryLimitInBytesName     = "swiftmonitor_memory_limit_in_bytes"
	swiftMonitorMemoryStatUsageInBytesName = "swiftmonitor_memory_stat_usage_in_bytes" // Read from memory.stat (cache + rss).
	swiftMonitorMemoryStatSwapInBytesName  = "swiftmonitor_memory_stat_swap_in_bytes"
	swiftMonitorMemoryStatRssInBytesName   = "swiftmonitor_memory_stat_rss_in_bytes"
	swiftMonitorMemoryStatCacheInBytesName = "swiftmonitor_memory_stat_cache_in_bytes"

	swiftMonitorK8sPodMemoryRequest = "swiftmonitor_k8s_pod_memory_request"
	swiftMonitorK8sPodCpuRequest    = "swiftmonitor_k8s_pod_cpu_request"
	swiftMonitorK8sPodMemoryLimit   = "swiftmonitor_k8s_pod_memory_limit"
	swiftMonitorK8sPodCpuLimit      = "swiftmonitor_k8s_pod_cpu_limit"
)

func getPodCgroupFilePath(podUid types.UID, qosClass corev1.PodQOSClass, family string, file string) (string, error) {
	path := ""
	uid := strings.ReplaceAll(string(podUid), "-", "_")

	if qosClass == corev1.PodQOSBurstable {
		path += cgroupPath + fmt.Sprintf("/%s", family) + burstablePodCgroupPath + burstablePodCgroupPathPrefix + uid + podCgroupPathSuffix + "/" + file
	} else if qosClass == corev1.PodQOSBestEffort {
		path += cgroupPath + fmt.Sprintf("/%s", family) + bestEffortPodCgroupPath + bestEffortPodCgroupPathPrefix + uid + podCgroupPathSuffix + "/" + file
	} else {
		return "", fmt.Errorf("invalid QOS class %s", string(qosClass))
	}

	return path, nil
}

func getContainerCgroupFilePath(podUid types.UID, qosClass corev1.PodQOSClass, family string, containerId string, file string) (string, error) {
	path := ""
	uid := strings.ReplaceAll(string(podUid), "-", "_")

	if qosClass == corev1.PodQOSBurstable {
		path += cgroupPath + fmt.Sprintf("/%s", family) + burstablePodCgroupPath + burstablePodCgroupPathPrefix + uid + podCgroupPathSuffix + fmt.Sprintf("/cri-containerd-%s.scope/", containerId) + file
	} else if qosClass == corev1.PodQOSBestEffort {
		path += cgroupPath + fmt.Sprintf("/%s", family) + bestEffortPodCgroupPath + bestEffortPodCgroupPathPrefix + uid + podCgroupPathSuffix + fmt.Sprintf("/cri-containerd-%s.scope/", containerId) + file
	} else {
		return "", fmt.Errorf("invalid QOS class %s", string(qosClass))
	}

	return path, nil
}

func getContainerID(id string) (string, error) {
	split := strings.Split(id, "//")
	if len(split) == 2 {
		return split[1], nil
	} else {
		return "", fmt.Errorf("wrong ID format: %s", id)
	}
}

func getPodCpuacctUsageFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgroupFilePath(podUid, qosClass, cpuFamily, cpuacctUsageFile)
}

func getPodCfsPeriodUsFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgroupFilePath(podUid, qosClass, cpuFamily, cfsPeriodUsFile)
}

func getPodCfsQuotaUsFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgroupFilePath(podUid, qosClass, cpuFamily, cfsQuotaUsFile)
}

func getPodMemoryStatFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgroupFilePath(podUid, qosClass, memoryFamily, memoryStatFile)
}

func getPodMemoryUsageInBytesFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgroupFilePath(podUid, qosClass, memoryFamily, memoryUsageInBytesFile)
}

func getContainerCfsPeriodUsFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgroupFilePath(podUid, qosClass, cpuFamily, containerId, cfsPeriodUsFile)
}

func getContainerCfsQuotaUsFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgroupFilePath(podUid, qosClass, cpuFamily, containerId, cfsQuotaUsFile)
}

func getContainerMemoryLimitInBytesFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgroupFilePath(podUid, qosClass, memoryFamily, containerId, memoryLimitInBytesFile)
}

func getContainerMemoryStatFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgroupFilePath(podUid, qosClass, memoryFamily, containerId, memoryStatFile)
}

func readInt64(file string) (int64, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(string(data[:len(data)-1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

type MemoryStatData struct {
	cache int64
	rss   int64
	swap  int64
}

func (d *MemoryStatData) Add(other *MemoryStatData) {
	d.rss += other.rss
	d.cache += other.cache
	d.swap += other.swap
}

type MetricDataPoint struct {
	cpuUsage    int64
	cpuLimit    float64
	podCpuLimit float64

	memoryUsageInBytes  int64
	memoryLimitInBytes  int64
	memoryStat          *MemoryStatData
	containerMemoryStat *MemoryStatData

	k8sMemoryRequest int64
	k8sCpuRequest    int64
	k8sMemoryLimit   int64
	k8sCpuLimit      int64

	timestamp time.Time
}

type SwiftMonitor struct {
	nodeName string

	podInformer coreinformers.PodInformer
	podLister   corelisters.PodLister
	podSynced   cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface

	podMap sync.Map
}

type ContainerInfo struct {
	cfsPeriodUsFilePath string
	cfsQuotaUsFilePath  string

	memoryLimitInBytesFilePath string
	memoryStatFilePath         string
}

type PodInfo struct {
	pod *corev1.Pod

	cpuacctUsageFilePath       string
	cfsPeriodUsFilePath        string
	cfsQuotaUsFilePath         string
	memoryUsageInBytesFilePath string
	memoryStatFilePath         string

	containerInfos []*ContainerInfo
}

type cpuPromHandler struct {
	monitor *SwiftMonitor
}

func (c *cpuPromHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	metrics := c.monitor.collectMetrics()

	for k, v := range metrics {
		loadVal, ok := c.monitor.podMap.Load(k)
		pInfo := loadVal.(*PodInfo)
		if ok {
			out := fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorCpuUsageName, pInfo.pod.Name, pInfo.pod.Namespace, v.cpuUsage)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %f\n",
				swiftMonitorCpuLimitName, pInfo.pod.Name, pInfo.pod.Namespace, v.cpuLimit)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %f\n",
				swiftMonitorPodCpuLimitName, pInfo.pod.Name, pInfo.pod.Namespace, v.podCpuLimit)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorPodMemoryUsageInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.memoryUsageInBytes)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorMemoryLimitInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.memoryLimitInBytes)

			// Pod memory.stat
			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorPodMemoryStatUsageInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.memoryStat.rss+v.memoryStat.cache)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorPodMemoryStatSwapInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.memoryStat.swap)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorPodMemoryStatRssInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.memoryStat.rss)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorPodMemoryStatCacheInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.memoryStat.cache)

			// Container memory.stat
			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorMemoryStatUsageInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.containerMemoryStat.rss+v.containerMemoryStat.cache)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorMemoryStatSwapInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.containerMemoryStat.swap)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorMemoryStatRssInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.containerMemoryStat.rss)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorMemoryStatCacheInBytesName, pInfo.pod.Name, pInfo.pod.Namespace, v.containerMemoryStat.cache)

			// K8s stats
			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodMemoryRequest, pInfo.pod.Name, pInfo.pod.Namespace, v.k8sMemoryRequest)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodCpuRequest, pInfo.pod.Name, pInfo.pod.Namespace, v.k8sCpuRequest)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodMemoryLimit, pInfo.pod.Name, pInfo.pod.Namespace, v.k8sMemoryLimit)

			out += fmt.Sprintf("%s{podname=\"%s\", namespace=\"%s\"} %d\n",
				swiftMonitorK8sPodCpuLimit, pInfo.pod.Name, pInfo.pod.Namespace, v.k8sCpuLimit)

			w.Write([]byte(out))
		}
	}
}

func (m *SwiftMonitor) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)

	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), m.podSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// Launch worker to process Pod resources
	go wait.UntilWithContext(ctx, m.runWorker, time.Second)

	server := &http.Server{
		Addr:         "0.0.0.0:10000",
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}
	mux := http.NewServeMux()
	mux.Handle("/cpu/prom", &cpuPromHandler{
		monitor: m,
	})
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(string("hello,world!\n")))
	})
	server.Handler = mux
	go server.ListenAndServe()

	<-ctx.Done()

	server.Close()
	return nil
}

func (c *SwiftMonitor) readMemoryStat(filePath string) (*MemoryStatData, error) {
	memoryStatFileData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var rss int64 = 0
	var cache int64 = 0
	var swap int64 = 0

	lines := strings.Split(string(memoryStatFileData), "\n")
	for _, line := range lines {

		line = strings.Trim(line, "\n")
		words := strings.Fields(line)
		if len(words) != 2 {
			continue
		}

		item := words[0]
		value := words[1]

		if item == "rss" {
			rss, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				klog.ErrorS(err, "parse int64 failed.", "memory.stat", "rss")
			}
		} else if item == "cache" {
			cache, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				klog.ErrorS(err, "parse int64 failed.", "memory.stat", "cache")
			}
		} else if item == "swap" {
			swap, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				klog.ErrorS(err, "parse int64 failed.", "memory.stat", "swap")
			}
		}
	}

	return &MemoryStatData{
		rss:   rss,
		cache: cache,
		swap:  swap,
	}, nil
}

func (c *SwiftMonitor) collectMetrics() map[string]*MetricDataPoint {
	retval := make(map[string]*MetricDataPoint)

	c.podMap.Range(func(podKey, info interface{}) bool {
		pInfo := info.(*PodInfo)

		// cpuacct.usage
		cpuacctUsage, err := readInt64(pInfo.cpuacctUsageFilePath)
		if err != nil {
			klog.ErrorS(err, "read cpuacct.usage failed.")
			return true
		}

		// memory.usage_in_bytes
		memoryUsage, err := readInt64(pInfo.memoryUsageInBytesFilePath)
		if err != nil {
			klog.ErrorS(err, "read memory.usage_in_bytes failed.")
			return true
		}

		memoryStatData, err := c.readMemoryStat(pInfo.memoryStatFilePath)
		if err != nil {
			klog.ErrorS(err, "read memory.stat failed.")
			return true
		}

		var podCpuLimit float64 = -1.0

		// cpu.cfs_quota_us
		podCfsQuotaUs, err := readInt64(pInfo.cfsQuotaUsFilePath)
		if err != nil {
			klog.ErrorS(err, "read cpu.cfs_quota_us failed.")
			return true
		}

		if podCfsQuotaUs != -1 {
			// cpu.cfs_period_us
			podCfsPeriodUs, err := readInt64(pInfo.cfsPeriodUsFilePath)
			if err != nil {
				klog.ErrorS(err, "read cpu.cfs_period_us failed.")
				return true
			}

			podCpuLimit = float64(podCfsQuotaUs) / float64(podCfsPeriodUs)
		}

		var cpuLimit float64 = 0.0
		var memoryLimitInBytes int64 = 0
		var containerMemoryStatData *MemoryStatData = nil

		// Container metrics
		for _, containerInfo := range pInfo.containerInfos {
			// cpu.cfs_quota_us
			cfsQuotaUs, err := readInt64(containerInfo.cfsQuotaUsFilePath)
			if err != nil {
				klog.ErrorS(err, "read container cpu.cfs_quota_us failed.")
				return true
			}

			if cfsQuotaUs != -1 {
				// cpu.cfs_period_us
				cfsPeriodUs, err := readInt64(containerInfo.cfsPeriodUsFilePath)
				if err != nil {
					klog.ErrorS(err, "read container cpu.cfs_period_us failed.")
					return true
				}

				cpuLimit += float64(cfsQuotaUs) / float64(cfsPeriodUs)
			}

			// memory.limit_in_bytes
			memoryLimit, err := readInt64(containerInfo.memoryLimitInBytesFilePath)
			if err != nil {
				klog.ErrorS(err, "read container memory.limit_in_bytes failed.")
				return true
			}

			// if not memory limit < 0 or memory limit > 512 GiB
			if memoryLimit > 0 && memoryLimit < 549755813888 {
				memoryLimitInBytes += memoryLimit
			}

			perContainerMemoryStatData, err := c.readMemoryStat(containerInfo.memoryStatFilePath)
			if err != nil {
				klog.ErrorS(err, "Read memory.stat failed.")
				return true
			}

			if containerMemoryStatData == nil {
				containerMemoryStatData = perContainerMemoryStatData
			} else {
				containerMemoryStatData.Add(perContainerMemoryStatData)
			}
		}

		var k8sMemoryRequest int64 = 0
		var k8sCpuRequest int64 = 0
		var k8sMemoryLimit int64 = 0
		var k8sCpuLimit int64 = 0

		for _, container := range pInfo.pod.Spec.Containers {
			memoryRequest := container.Resources.Requests.Memory().Value()
			k8sMemoryRequest += memoryRequest
			cpuRequest := container.Resources.Requests.Cpu().Value()
			k8sCpuRequest += cpuRequest
			memoryLimit := container.Resources.Limits.Memory().Value()
			k8sMemoryLimit += memoryLimit
			cpuLimit := container.Resources.Limits.Cpu().Value()
			k8sCpuLimit += cpuLimit
		}

		timestamp := time.Now()

		retval[podKey.(string)] = &MetricDataPoint{
			cpuUsage:    cpuacctUsage,
			cpuLimit:    cpuLimit,
			podCpuLimit: podCpuLimit,

			memoryUsageInBytes:  memoryUsage,
			memoryLimitInBytes:  memoryLimitInBytes,
			memoryStat:          memoryStatData,
			containerMemoryStat: containerMemoryStatData,

			k8sMemoryRequest: k8sMemoryRequest,
			k8sCpuRequest:    k8sCpuRequest,
			k8sMemoryLimit:   k8sMemoryLimit,
			k8sCpuLimit:      k8sCpuLimit,

			timestamp: timestamp,
		}
		return true
	})

	return retval
}

func (c *SwiftMonitor) enqueuePod(obj interface{}) {
	var key string
	var err error

	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

func (c *SwiftMonitor) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *SwiftMonitor) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// SwiftDeployment resource to be synced.
		if err := c.syncHandler(ctx, key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (c *SwiftMonitor) syncHandler(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "resourceName", key)

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Pod resource with this namespace/name
	pod, err := c.podLister.Pods(namespace).Get(name)
	if err != nil {
		// The Pod resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
			c.podMap.Delete(key)
			return nil
		}
		return err
	}

	// cpuacct.usage
	podCpuacctUsageFilePath, err := getPodCpuacctUsageFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		logger.Error(err, "Get cpuacct.usage path failed.")
		return nil
	}

	_, err = os.Stat(podCpuacctUsageFilePath)
	if os.IsNotExist(err) {
		c.podMap.Delete(key)
		return nil
	} else {
		logger.Info(fmt.Sprintf("%s exists.", podCpuacctUsageFilePath))
	}

	// memory.usage_in_bytes
	podMemoryUsageInBytesFilePath, err := getPodMemoryUsageInBytesFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		logger.Error(err, "Get memory.usage_in_bytes path failed.")
		return nil
	}

	_, err = os.Stat(podMemoryUsageInBytesFilePath)
	if os.IsNotExist(err) {
		c.podMap.Delete(key)
		logger.Error(err, "File not exists.")
		return nil
	} else {
		logger.Info(fmt.Sprintf("%s exists.", podMemoryUsageInBytesFilePath))
	}

	// cpu.cfs_period_us
	podCpuCfsPeriodFilePath, err := getPodCfsPeriodUsFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		logger.Error(err, "Get memory.usage_in_bytes path failed.")
		return nil
	}

	_, err = os.Stat(podCpuCfsPeriodFilePath)
	if os.IsNotExist(err) {
		c.podMap.Delete(key)
		logger.Error(err, "File not exists.")
		return nil
	} else {
		logger.Info(fmt.Sprintf("%s exists.", podCpuCfsPeriodFilePath))
	}

	// cpu.cfs_quota_us
	podCpuCfsQuotaFilePath, err := getPodCfsQuotaUsFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		logger.Error(err, "Get memory.usage_in_bytes path failed.")
		return nil
	}

	_, err = os.Stat(podCpuCfsQuotaFilePath)
	if os.IsNotExist(err) {
		c.podMap.Delete(key)
		logger.Error(err, "File not exists.")
		return nil
	} else {
		logger.Info(fmt.Sprintf("%s exists.", podCpuCfsQuotaFilePath))
	}

	// memory.stat
	podMemoryStatFilePath, err := getPodMemoryStatFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		logger.Error(err, "Get memory.stat path failed.")
		return nil
	}

	_, err = os.Stat(podMemoryStatFilePath)
	if os.IsNotExist(err) {
		c.podMap.Delete(key)
		logger.Error(err, "File not exists.")
		return nil
	} else {
		logger.Info(fmt.Sprintf("%s exists.", podMemoryStatFilePath))
	}

	containerInfos := make([]*ContainerInfo, 0)
	for _, containerStatus := range pod.Status.ContainerStatuses {
		containerId, err := getContainerID(containerStatus.ContainerID)
		if err != nil {
			c.podMap.Delete(key)
			logger.Error(err, "Split container ID failed.")
			return nil
		}
		logger.Info(fmt.Sprintf("Container ID = %s", containerId))

		// container cpu.cfs_period_us
		cpuCfsPeriodFilePath, err := getContainerCfsPeriodUsFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
		if err != nil {
			logger.Error(err, "Get cpu.cfs_period_us file path failed.")
			return nil
		}

		_, err = os.Stat(cpuCfsPeriodFilePath)
		if os.IsNotExist(err) {
			c.podMap.Delete(key)
			logger.Error(err, "File not exists.")
			return nil
		} else {
			logger.Info(fmt.Sprintf("%s exists.", cpuCfsPeriodFilePath))
		}

		// container cpu.cfs_quota_us
		cpuCfsQuotaFilePath, err := getContainerCfsQuotaUsFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
		if err != nil {
			logger.Error(err, "Get cpu.cfs_quota_us file path failed.")
			return nil
		}

		_, err = os.Stat(cpuCfsQuotaFilePath)
		if os.IsNotExist(err) {
			c.podMap.Delete(key)
			return nil
		} else {
			logger.Info(fmt.Sprintf("%s exists.", cpuCfsQuotaFilePath))
		}

		// container memory.limit_in_bytes
		memoryLimitInBytesFilePath, err := getContainerMemoryLimitInBytesFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
		if err != nil {
			logger.Error(err, "Get memory.limit_in_bytes file path failed.")
			return nil
		}

		_, err = os.Stat(memoryLimitInBytesFilePath)
		if os.IsNotExist(err) {
			c.podMap.Delete(key)
			return nil
		} else {
			logger.Info(fmt.Sprintf("%s exists.", memoryLimitInBytesFilePath))
		}

		// container memory.stat
		memoryStatFilePath, err := getContainerMemoryStatFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
		if err != nil {
			logger.Error(err, "Get memory.stat file path failed.")
			return nil
		}

		_, err = os.Stat(memoryStatFilePath)
		if os.IsNotExist(err) {
			c.podMap.Delete(key)
			return nil
		} else {
			logger.Info(fmt.Sprintf("%s exists.", memoryStatFilePath))
		}

		containerInfos = append(containerInfos, &ContainerInfo{
			cfsPeriodUsFilePath: cpuCfsPeriodFilePath,
			cfsQuotaUsFilePath:  cpuCfsQuotaFilePath,

			memoryStatFilePath:         memoryStatFilePath,
			memoryLimitInBytesFilePath: memoryLimitInBytesFilePath,
		})
	}

	c.podMap.Store(key, &PodInfo{
		pod: pod.DeepCopy(),

		cpuacctUsageFilePath:       podCpuacctUsageFilePath,
		cfsPeriodUsFilePath:        podCpuCfsPeriodFilePath,
		cfsQuotaUsFilePath:         podCpuCfsQuotaFilePath,
		memoryUsageInBytesFilePath: podMemoryUsageInBytesFilePath,
		memoryStatFilePath:         podMemoryStatFilePath,

		containerInfos: containerInfos,
	})

	return nil
}

func NewMonitorCommand() *cobra.Command {
	klog.InitFlags(nil)

	cmd := &cobra.Command{
		Use:  "swift-monitor",
		Long: "TODO",
		RunE: func(cmd *cobra.Command, args []string) error {
			klog.Info("Swift Monitor Starting ...")
			ctx := signals.SetupSignalHandler()

			cfg, err := kuberest.InClusterConfig()
			if err != nil {
				klog.Error(err)
				// TODO 测试用
				cfg, err = clientcmd.BuildConfigFromFlags("", "/root/.kube/config")
				if err != nil {
					klog.Error(err, "Error building kubeconfig")
					klog.FlushAndExit(klog.ExitFlushTimeout, 1)
				}
			}

			kubeClient, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				klog.Error(err, "Error building Kubernetes client")
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			}

			hostname, err := os.Hostname()
			if err != nil {
				klog.Fatal(err)
			}
			val, found := os.LookupEnv("MY_NODE_NAME")
			if found {
				hostname = val
			}

			kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
			podInformer := kubeInformerFactory.Core().V1().Pods()

			monitor := &SwiftMonitor{
				nodeName: hostname,

				podInformer: podInformer,
				podLister:   podInformer.Lister(),
				podSynced:   podInformer.Informer().HasSynced,

				workqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "SwiftMonitor"),
			}

			monitor.podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: monitor.enqueuePod,
				UpdateFunc: func(old, new interface{}) {
					oldP := old.(*corev1.Pod)
					newP := new.(*corev1.Pod)
					if oldP.ResourceVersion == newP.ResourceVersion {
						// Periodic resync will send update events for all known Deployments.
						// Two different versions of the same Deployment will always have different RVs.
						return
					}
					monitor.enqueuePod(new)
				},
				DeleteFunc: monitor.enqueuePod,
			})

			go monitor.Run(ctx)

			kubeInformerFactory.Start(ctx.Done())

			return Run(ctx)
		},
	}

	return cmd
}

func Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
