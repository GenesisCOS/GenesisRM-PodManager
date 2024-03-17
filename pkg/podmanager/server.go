package podmanager

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	cgroup "swiftkube.io/swiftkube/pkg/cgroup"
	"swiftkube.io/swiftkube/pkg/signals"
)

type PodState = string
type CPUState = string
type MemoryState = string
type EndpointState = string

const (
	CPU_UNKNOWN                    CPUState = "unknown"
	CPU_DYNAMIC_OVERPROVISION      CPUState = "dynamic-overprovision"
	CPU_DYNAMIC_RESOURCE_EFFICIENT CPUState = "dynamic-resource-efficient"
	CPU_MAX                        CPUState = "cpu-max"

	MEMORY_UNKNOWN MemoryState = "unknown"
	MEMORY_SWAPPED MemoryState = "mem-swapped"
	MEMORY_MAX     MemoryState = "mem-max"

	POD_READY_FULLSPEED PodState = "Ready-FullSpeed"
	POD_READY_RUNNING   PodState = "Ready-Running"
	POD_READY_CATNAP    PodState = "Ready-CatNap"
	POD_READY_LONGNAP   PodState = "Ready-LongNap"
	POD_INITIALIZING    PodState = "Initializing"
	POD_WARMINGUP       PodState = "WarmingUp"

	ENDPOINT_UP   EndpointState = "Up"
	ENDPOINT_DOWN EndpointState = "Down"
)

type ContainerInfo struct {
	name string

	cg *cgroup.Cgroup
}

type PodInfo struct {
	Pod *corev1.Pod

	cg *cgroup.Cgroup

	cpuState    CPUState
	memoryState MemoryState

	containerInfos []*ContainerInfo
}

type MetricDataPoint struct {
	CPUUsage    int64
	CPUQuota    float64
	PodCPUQuota float64

	MemUsageInBytes  int64
	MemLimitInBytes  int64
	PodMemStat       *cgroup.CgMemoryStat
	ContainerMemStat *cgroup.CgMemoryStat

	CPURequest   int64
	CPULimit     int64
	CPUAllocated int64

	MemRequest   int64
	MemLimit     int64
	MemAllocated int64

	timestamp time.Time
}

type PodManager struct {
	nodeName string

	client *kubernetes.Clientset

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

func (pm *PodManager) GetPodMap() *sync.Map {
	return &(pm.podMap)
}

func (c *PodManager) enqueuePod(obj interface{}) {
	var key string
	var err error

	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

func (c *PodManager) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *PodManager) processNextWorkItem(ctx context.Context) bool {
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

func (manager *PodManager) GetPodInfo(key string) *PodInfo {
	v, ok := manager.podMap.Load(key)
	if !ok {
		return nil
	}
	return v.(*PodInfo)
}

func (c *PodManager) syncHandler(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name

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

	pod = pod.DeepCopy()

	if pod.Spec.NodeName != c.nodeName {
		c.podMap.Delete(key)
		return nil
	}

	podCg, err := cgroup.NewPodCg(pod)
	if err != nil {
		klog.ErrorS(err, "Fail to get pod cgroup")
		c.podMap.Delete(key)
		return nil
	}

	state, ok := pod.GetLabels()["swiftkube.io/state"]
	if !ok {
		c.podMap.Delete(key)
		return nil
	}

	endpoint, ok := pod.GetLabels()["swiftkube.io/endpoint"]
	if !ok {
		c.podMap.Delete(key)
		return nil
	}

	containerInfos := make([]*ContainerInfo, 0)
	for _, containerStatus := range pod.Status.ContainerStatuses {

		cg, err := cgroup.NewContainerCg(pod, &containerStatus)
		if err != nil {
			klog.ErrorS(err, "Fail to get container cgroup")
			c.podMap.Delete(key)
			return nil
		}

		containerInfos = append(containerInfos, &ContainerInfo{
			name: containerStatus.Name,
			cg:   cg,
		})
	}

	// (state == WarmingUp or Ready-FullSpeed) and endpoint == Down
	if (state == string(POD_WARMINGUP) || state == string(POD_READY_FULLSPEED)) && endpoint == string(ENDPOINT_DOWN) {
		for {
			t := time.NewTimer(500 * time.Millisecond)
			// TODO
			//if c.GetPodInfo(key).cpuState == CPU_MAX && c.GetPodInfo(key).memoryState == MEMORY_MAX {
			if c.GetPodInfo(key).cpuState == CPU_MAX {
				break
			}
			<-t.C
		}

		pod.GetLabels()["swiftkube.io/endpoint"] = string(ENDPOINT_UP)
		for {
			_, err := c.client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, v1.UpdateOptions{})
			if err == nil {
				break
			} else {
				klog.ErrorS(err, "Fail to update pod. retry ...")
				pod, err = c.podLister.Pods(pod.Namespace).Get(pod.Name)
				if errors.IsNotFound(err) {
					utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
					c.podMap.Delete(key)
					return nil
				}
				pod = pod.DeepCopy()
				pod.GetLabels()["swiftkube.io/endpoint"] = string(ENDPOINT_UP)
			}
		}
	}

	// state == Ready-Running and endpoint == Down
	if state == string(POD_READY_RUNNING) && endpoint == string(ENDPOINT_DOWN) {
		for {
			t := time.NewTimer(500 * time.Millisecond)
			//if c.GetPodInfo(key).cpuState == CPU_DYNAMIC_OVERPROVISION && c.GetPodInfo(key).memoryState == MEMORY_MAX {
			if c.GetPodInfo(key).cpuState == CPU_DYNAMIC_OVERPROVISION {
				break
			}
			<-t.C
		}

		pod.GetLabels()["swiftkube.io/endpoint"] = string(ENDPOINT_UP)
		for {
			_, err := c.client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, v1.UpdateOptions{})
			if err == nil {
				break
			} else {
				klog.ErrorS(err, "Fail to update pod. retry ...")
				pod, err = c.podLister.Pods(pod.Namespace).Get(pod.Name)
				if errors.IsNotFound(err) {
					utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
					c.podMap.Delete(key)
					return nil
				}
				pod = pod.DeepCopy()
				pod.GetLabels()["swiftkube.io/endpoint"] = string(ENDPOINT_UP)
			}
		}
	}

	// (state == Ready-CatNap or Ready-LongNap) and endpoint == Up
	if (state == string(POD_READY_CATNAP) || state == string(POD_READY_LONGNAP)) && endpoint == string(ENDPOINT_UP) {
		pod.GetLabels()["swiftkube.io/endpoint"] = string(ENDPOINT_DOWN)
		for {
			_, err := c.client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, v1.UpdateOptions{})
			if err == nil {
				break
			} else {
				klog.ErrorS(err, "Fail to update pod. retry ...")
				pod, err = c.podLister.Pods(pod.Namespace).Get(pod.Name)
				if errors.IsNotFound(err) {
					utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
					c.podMap.Delete(key)
					return nil
				}
				pod = pod.DeepCopy()
				pod.GetLabels()["swiftkube.io/endpoint"] = string(ENDPOINT_UP)
			}
		}
	}

	pod, err = c.podLister.Pods(pod.Namespace).Get(pod.Name)
	if errors.IsNotFound(err) {
		utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
		c.podMap.Delete(key)
		return nil
	}

	c.podMap.Swap(key, &PodInfo{
		Pod: pod.DeepCopy(),

		cg: podCg,

		cpuState:    CPU_UNKNOWN,
		memoryState: MEMORY_UNKNOWN,

		containerInfos: containerInfos,
	})

	return nil
}

func (c *PodManager) CollectMetrics() map[string]*MetricDataPoint {
	retval := make(map[string]*MetricDataPoint)

	c.podMap.Range(func(podKey, info interface{}) bool {
		pInfo := info.(*PodInfo)

		// cpuacct.usage
		cpuacctUsage, err := pInfo.cg.GetCPUAcctUsage()
		if err != nil {
			klog.ErrorS(err, "read cpuacct.usage failed.")
			return true
		}

		// memory.usage_in_bytes
		podMemUsageInBytes, err := pInfo.cg.GetMemoryUsageInBytes()
		if err != nil {
			klog.ErrorS(err, "read memory.usage_in_bytes failed.")
			return true
		}

		podMemStat, err := pInfo.cg.GetMemoryStat()
		if err != nil {
			klog.ErrorS(err, "read memory.stat failed.")
			return true
		}

		var podCPUQuota float64 = -1.0

		// cpu.cfs_quota_us
		podCFSQuota, err := pInfo.cg.GetCFSQuota()
		if err != nil {
			klog.ErrorS(err, "read cpu.cfs_quota_us failed.")
			return true
		}

		if podCFSQuota != -1 {
			// cpu.cfs_period_us
			podCFSPeriod, err := pInfo.cg.GetCFSPeriod()
			if err != nil {
				klog.ErrorS(err, "read cpu.cfs_period_us failed.")
				return true
			}

			podCPUQuota = float64(podCFSQuota) / float64(podCFSPeriod)
		}

		var containerCPUQuota float64 = 0.0
		var containerMemLimitInBytes int64 = 0
		var containerMemStat *cgroup.CgMemoryStat = nil

		// Container metrics
		for _, containerInfo := range pInfo.containerInfos {
			// cpu.cfs_quota_us
			cfsQuotaUs, err := containerInfo.cg.GetCFSQuota()
			if err != nil {
				klog.ErrorS(err, "read container cpu.cfs_quota_us failed.")
				return true
			}

			if cfsQuotaUs != -1 {
				// cpu.cfs_period_us
				cfsPeriodUs, err := containerInfo.cg.GetCFSQuota()
				if err != nil {
					klog.ErrorS(err, "read container cpu.cfs_period_us failed.")
					return true
				}

				containerCPUQuota += float64(cfsQuotaUs) / float64(cfsPeriodUs)
			}

			// memory.limit_in_bytes
			perContainerMemLimitInBytes, err := containerInfo.cg.GetMemoryLimitInBytes()
			if err != nil {
				klog.ErrorS(err, "read container memory.limit_in_bytes failed.")
				return true
			}

			// if not memory limit < 0 or memory limit > 512 GiB
			if perContainerMemLimitInBytes > 0 && perContainerMemLimitInBytes < 549755813888 {
				containerMemLimitInBytes += perContainerMemLimitInBytes
			}

			perContainerMemStat, err := containerInfo.cg.GetMemoryStat()
			if err != nil {
				klog.ErrorS(err, "Fail to read memory.stat")
				return true
			}

			if containerMemStat == nil {
				containerMemStat = perContainerMemStat
			} else {
				containerMemStat.Add(perContainerMemStat)
			}
		}

		var memRequest int64 = 0
		var cpuRequest int64 = 0
		var memLimit int64 = 0
		var cpuLimit int64 = 0

		for _, container := range pInfo.Pod.Spec.Containers {
			memRequest += container.Resources.Requests.Memory().Value()
			cpuRequest += container.Resources.Requests.Cpu().MilliValue()
			memLimit += container.Resources.Limits.Memory().Value()
			cpuLimit += container.Resources.Limits.Cpu().MilliValue()
		}

		var memAllocated int64 = 0
		var cpuAllocated int64 = 0

		for _, containerStatus := range pInfo.Pod.Status.ContainerStatuses {
			cpuAllocated += containerStatus.AllocatedResources.Cpu().MilliValue()
			memAllocated += containerStatus.AllocatedResources.Memory().Value()
		}

		timestamp := time.Now()

		retval[podKey.(string)] = &MetricDataPoint{
			CPUUsage:    cpuacctUsage,
			CPUQuota:    containerCPUQuota,
			PodCPUQuota: podCPUQuota,

			MemUsageInBytes:  podMemUsageInBytes,
			MemLimitInBytes:  containerMemLimitInBytes,
			PodMemStat:       podMemStat,
			ContainerMemStat: containerMemStat,

			MemRequest:   memRequest,
			CPURequest:   cpuRequest,
			MemLimit:     memLimit,
			CPULimit:     cpuLimit,
			MemAllocated: memAllocated,
			CPUAllocated: cpuAllocated,

			timestamp: timestamp,
		}
		return true
	})

	return retval
}

func NewPodManagerCommand() *cobra.Command {
	klog.InitFlags(nil)

	cmd := &cobra.Command{
		Use:  "podmanager",
		Long: "TODO",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := signals.SetupSignalHandler()

			cfg, err := kuberest.InClusterConfig()
			if err != nil {
				klog.Error(err)
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

			podManager := &PodManager{
				nodeName: hostname,

				client: kubeClient,

				podInformer: podInformer,
				podLister:   podInformer.Lister(),
				podSynced:   podInformer.Informer().HasSynced,

				workqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "PodManager"),
			}

			podManager.podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: podManager.enqueuePod,
				UpdateFunc: func(old, new interface{}) {
					oldP := old.(*corev1.Pod)
					newP := new.(*corev1.Pod)
					if oldP.ResourceVersion == newP.ResourceVersion {
						// Periodic resync will send update events for all known Deployments.
						// Two different versions of the same Deployment will always have different RVs.
						return
					}
					podManager.enqueuePod(new)
				},
				DeleteFunc: podManager.enqueuePod,
			})

			go podManager.Run(ctx)

			kubeInformerFactory.Start(ctx.Done())

			<-ctx.Done()

			return nil
		},
	}

	return cmd
}

func (m *PodManager) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)

	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), m.podSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// Launch worker to process Pod resources
	for i := 0; i < 64; i++ {
		go wait.UntilWithContext(ctx, m.runWorker, time.Second)
	}

	cpuScaler := CPUScaler{
		manager: m,
		podMap:  make(map[string]*PerPodCPUScalerData),
	}

	logger.Info("Start CPU scaler")
	go cpuScaler.Start(context.TODO())

	server := &http.Server{
		Addr:         "0.0.0.0:10000",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	mux := http.NewServeMux()
	mux.Handle("/cpu/prom", &Monitor{
		manager: m,
	})

	server.Handler = mux
	go server.ListenAndServe()

	<-ctx.Done()

	return nil
}
