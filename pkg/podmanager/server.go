package podmanager

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/moby/ipvs"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	cgroup "swiftkube.io/swiftkube/pkg/cgroup"
	"swiftkube.io/swiftkube/pkg/podmanager/types"
)

type ContainerInfo struct {
	Name   string
	Cgroup *cgroup.Cgroup
}

type PodInfo struct {
	Pod            *corev1.Pod
	PodResource    PodResource
	Cgroup         *cgroup.Cgroup
	CPUState       types.PodCPUState
	MemoryState    types.PodMemoryState
	ContainerInfos []*ContainerInfo
}

type PodManager struct {
	NodeName string
	Cores    uint64

	client      *kubernetes.Clientset
	PodInformer coreinformers.PodInformer
	PodLister   corelisters.PodLister
	PodSynced   cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface

	updateResourceLock sync.Mutex

	LCPodMap           sync.Map
	BEPodMap           sync.Map
	UncontrolledPodMap sync.Map

	BesteffortCgroup *cgroup.Cgroup
	BurstableCgroup  *cgroup.Cgroup
}

func (pm *PodManager) PodInfo(pod *corev1.Pod) *PodInfo {
	key, _ := cache.MetaNamespaceKeyFunc(pod)
	return pm.PodInfoByKey(key)
}

func (pm *PodManager) PodInfoByKey(key string) *PodInfo {
	v, ok := pm.LCPodMap.Load(key)
	if ok {
		return v.(*PodInfo)
	}
	v, ok = pm.BEPodMap.Load(key)
	if ok {
		return v.(*PodInfo)
	}
	v, ok = pm.UncontrolledPodMap.Load(key)
	if ok {
		return v.(*PodInfo)
	}
	return nil
}

func (pm *PodManager) GetPodMap() *sync.Map {
	return &(pm.LCPodMap)
}

func (pm *PodManager) GetUncontrolledPodMap() *sync.Map {
	return &(pm.UncontrolledPodMap)
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
	v, ok := manager.LCPodMap.Load(key)
	if !ok {
		return nil
	}
	return v.(*PodInfo)
}

const defaultIPVSWeight int64 = 100

func editIPVSWeight(realServerIP string, realServerPort uint16, weight int64) error {
	handle, err := ipvs.New("")

	if err != nil {
		return err
	}
	svcs, err := handle.GetServices()
	if err != nil {
		return err
	}
	for _, svc := range svcs {
		dests, err := handle.GetDestinations(svc)
		if err != nil {
			return err
		}

		for _, dest := range dests {
			if dest.Address.Equal(net.ParseIP(realServerIP)) && dest.Port == realServerPort {
				dest.Weight = int(weight)
				return handle.UpdateDestination(svc, dest)
			}
		}
	}

	/* 以下是命令行的方式修改权重。目前使用moby的ipvs包，就无需调用ipvsadm了。
	cmd := exec.Command(
		"ipvsadm", "-e",
		"--real-server", fmt.Sprintf("%s:%d", realServerIP, realServerPort),
		"--tcp-service", fmt.Sprintf("%s:%d", service.Address.String(), service.Port),
		"--weight", strconv.FormatInt(weight, 10),
		"----masquerading",
	)

	return cmd.Run()
	*/

	return fmt.Errorf("not found real server")
}

func (c *PodManager) IsLocalPod(pod *corev1.Pod) bool {
	return pod.Spec.NodeName == c.NodeName
}

func (c *PodManager) IsPodRunning(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodRunning
}

func (c *PodManager) setLoadBalanceWeight(pod *corev1.Pod) {
	// 示例命令行：ipvsadm -e --real-server 172.30.4.97:18856 --tcp-service 10.4.233.75:18856 -w 500 -m
	// 设置 ipvs 的权重
	weightStr, ok := pod.GetLabels()["swiftkube.io/ipvs-weight"]
	if ok {
		weight, err := strconv.ParseInt(weightStr, 10, 64)
		podIP := pod.Status.PodIP
		containerPort := pod.Spec.Containers[0].Ports[0].ContainerPort

		if err != nil {
			klog.ErrorS(err, "parse int failed")
			weight = defaultIPVSWeight
		}

		err = editIPVSWeight(podIP, uint16(containerPort), weight)
		if err != nil {
			klog.ErrorS(err, "edit ipvs real server weight failed")
		}
	}
}

func (c *PodManager) listLocalPods(selector labels.Selector) ([]*corev1.Pod, error) {
	localPods := make([]*corev1.Pod, 0)
	pods, err := c.PodLister.List(selector)
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	// 过滤在本地的pod
	for _, pod := range pods {
		if c.IsLocalPod(pod) {
			localPods = append(localPods, pod.DeepCopy())
		}
	}
	return localPods, nil
}

func (c *PodManager) ListAllLocalPods() ([]*corev1.Pod, error) {
	return c.listLocalPods(labels.Everything())
}

func (c *PodManager) ListControlledLocalPods() ([]*corev1.Pod, error) {
	selector := labels.NewSelector()
	requirement, err := labels.NewRequirement(types.ENABLED_LABEL, selection.Equals, []string{"true"})
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	selector = selector.Add(*requirement)
	return c.listLocalPods(selector)
}

func (c *PodManager) IsPodControlledByGenesis(pod *corev1.Pod) bool {
	enabled, ok := pod.GetLabels()[types.ENABLED_LABEL]
	return ok && enabled == "true"
}

func (c *PodManager) syncPodState(pod *corev1.Pod, key string) {
	state := pod.GetLabels()[types.STATE_LABEL]
	endpoint := pod.GetLabels()[types.ENDPOINT_LABEL]

	// (state == WarmingUp or Ready-FullSpeed) and endpoint == Down
	if (state == string(types.WU) || state == string(types.RFS)) && endpoint == string(types.ENDPOINT_DOWN) {
		for {
			t := time.NewTimer(500 * time.Millisecond)
			// TODO
			//if c.GetPodInfo(key).cpuState == CPU_MAX && c.GetPodInfo(key).memoryState == MEMORY_MAX {
			if c.GetPodInfo(key).CPUState == types.CPU_MAX {
				break
			}
			<-t.C
		}

		pod.GetLabels()[types.ENDPOINT_LABEL] = string(types.ENDPOINT_UP)
		for {
			_, err := c.client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, v1.UpdateOptions{})
			if err == nil {
				break
			} else {
				klog.ErrorS(err, "failed to update pod. retry ...")
				pod, err = c.PodLister.Pods(pod.Namespace).Get(pod.Name)
				if errors.IsNotFound(err) {
					utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
					c.LCPodMap.Delete(key)
					return
				}
				pod = pod.DeepCopy()
				pod.GetLabels()[types.ENDPOINT_LABEL] = string(types.ENDPOINT_UP)
			}
		}
	}

	// state == Ready-Running and endpoint == Down
	if state == string(types.RR) && endpoint == string(types.ENDPOINT_DOWN) {
		for {
			t := time.NewTimer(500 * time.Millisecond)
			//if c.GetPodInfo(key).cpuState == CPU_DYNAMIC_OVERPROVISION && c.GetPodInfo(key).memoryState == MEMORY_MAX {
			if c.GetPodInfo(key).CPUState == types.CPU_DYNAMIC_OVERPROVISION {
				break
			}
			<-t.C
		}

		pod.GetLabels()[types.ENDPOINT_LABEL] = string(types.ENDPOINT_UP)
		for {
			_, err := c.client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, v1.UpdateOptions{})
			if err == nil {
				break
			} else {
				klog.ErrorS(err, "Fail to update pod. retry ...")
				pod, err = c.PodLister.Pods(pod.Namespace).Get(pod.Name)
				if errors.IsNotFound(err) {
					utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
					c.LCPodMap.Delete(key)
					return
				}
				pod = pod.DeepCopy()
				pod.GetLabels()[types.ENDPOINT_LABEL] = string(types.ENDPOINT_UP)
			}
		}
	}

	// (state == Ready-CatNap or Ready-LongNap) and endpoint == Up
	if (state == string(types.RCN) || state == string(types.RLN)) && endpoint == string(types.ENDPOINT_UP) {
		pod.GetLabels()[types.ENDPOINT_LABEL] = string(types.ENDPOINT_DOWN)
		for {
			_, err := c.client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, v1.UpdateOptions{})
			if err == nil {
				break
			} else {
				klog.ErrorS(err, "Fail to update pod. retry ...")
				pod, err = c.PodLister.Pods(pod.Namespace).Get(pod.Name)
				if errors.IsNotFound(err) {
					utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
					c.LCPodMap.Delete(key)
					return
				}
				pod = pod.DeepCopy()
				pod.GetLabels()[types.ENDPOINT_LABEL] = string(types.ENDPOINT_UP)
			}
		}
	}
}

func (c *PodManager) syncHandler(_ context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Pod resource with this namespace/name
	pod, err := c.PodLister.Pods(namespace).Get(name)
	if err != nil {
		// The Pod resource may no longer exist,
		// in which case we stop processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
			c.LCPodMap.Delete(key)
			c.BEPodMap.Delete(key)
			c.UncontrolledPodMap.Delete(key)
			return nil
		}
		return err
	}

	pod = pod.DeepCopy()

	c.setLoadBalanceWeight(pod)

	if !c.IsLocalPod(pod) || !c.IsPodRunning(pod) {
		c.LCPodMap.Delete(key)
		c.BEPodMap.Delete(key)
		c.UncontrolledPodMap.Delete(key)
		return nil
	}

	podCgroup, err := cgroup.LoadPodCgroup(pod)
	if err != nil {
		klog.ErrorS(err, "failed to load pod cgroup")
		c.LCPodMap.Delete(key)
		c.BEPodMap.Delete(key)
		c.UncontrolledPodMap.Delete(key)
		return nil
	}

	containerInfos := make([]*ContainerInfo, 0)
	for _, containerStatus := range pod.Status.ContainerStatuses {

		containerCgroup, err := cgroup.LoadContainerCgroup(pod, &containerStatus)
		if err != nil {
			klog.ErrorS(err, "fail to load container cgroup")
			c.LCPodMap.Delete(key)
			c.BEPodMap.Delete(key)
			c.UncontrolledPodMap.Delete(key)
			return nil
		}

		containerInfos = append(containerInfos, &ContainerInfo{
			Name:   containerStatus.Name,
			Cgroup: containerCgroup,
		})
	}

	serviceType := GetServiceType(pod)
	if serviceType == types.SERVICE_TYPE_UNKNOWN {
		// 默认所有Pod都是LC的
		serviceType = types.SERVICE_TYPE_LC
	}

	var pInfo *PodInfo
	var loaded bool
	var v any

	// 新加入Pod时没有初始化ResourceInfo
	// 所以在我看来新加入的Pod是没有被分到资源的
	// 即使该Pod此时的CPU利用率很高
	// 但是问题不大，因为cpuscaler会很快update他的资源的（1秒以内）
	var podMap *sync.Map = nil
	if c.IsPodControlledByGenesis(pod) {
		c.syncPodState(pod, key)
		if serviceType == types.SERVICE_TYPE_LC {
			podMap = &c.LCPodMap
		} else if serviceType == types.SERVICE_TYPE_BE {
			podMap = &c.BEPodMap
		}
	} else {
		podMap = &c.UncontrolledPodMap
	}
	v, loaded = podMap.LoadOrStore(key, &PodInfo{
		Pod:            pod.DeepCopy(),
		Cgroup:         podCgroup,
		CPUState:       types.CPU_UNKNOWN,
		MemoryState:    types.MEMORY_UNKNOWN,
		ContainerInfos: containerInfos,
	})
	pInfo = v.(*PodInfo)
	if loaded {
		pInfo.Pod = pod.DeepCopy()
	}

	return nil
}

func (c *PodManager) collectPodMetrics(pInfo *PodInfo) (*PodMetrics, error) {
	metrics, err := pInfo.Cgroup.Control.Stat()
	if err != nil {
		return nil, err
	}

	podCPUQuota, podCPUPeriod, err := pInfo.Cgroup.GetCPUQuotaAndPeriod()
	if err != nil {
		return nil, err
	}

	kubernetesContainerMetrics := make([]*KubernetesContainerMetrics, 0)

	var totalMemRequest int64 = 0
	var totalCPURequest int64 = 0
	var totalMemLimit int64 = 0
	var totalCPULimit int64 = 0

	for _, container := range pInfo.Pod.Spec.Containers {
		cm := &KubernetesContainerMetrics{
			CPURequest: container.Resources.Requests.Cpu().MilliValue(),
			CPULimit:   container.Resources.Limits.Cpu().MilliValue(),
			MemRequest: container.Resources.Requests.Memory().Value(),
			MemLimit:   container.Resources.Limits.Memory().Value(),
		}
		totalMemRequest += container.Resources.Requests.Memory().Value()
		totalCPURequest += container.Resources.Requests.Cpu().MilliValue()
		totalMemLimit += container.Resources.Limits.Memory().Value()
		totalCPULimit += container.Resources.Limits.Cpu().MilliValue()

		kubernetesContainerMetrics = append(kubernetesContainerMetrics, cm)
	}

	timestamp := time.Now()

	return &PodMetrics{
		PInfo:        pInfo,
		PodCPUQuota:  podCPUQuota,
		PodCPUPeriod: podCPUPeriod,

		Kubernetes: &KubernetesPodMetrics{
			containers:      kubernetesContainerMetrics,
			TotalCPURequest: totalCPURequest,
			TotalCPULimit:   totalCPULimit,
			TotalMemRequest: totalMemRequest,
			TotalMemLimit:   totalMemLimit,
		},

		timestamp:         timestamp,
		ContainerdMetrics: metrics,
	}, nil
}

func (c *PodManager) NodeResourceInfo() *ResourceInfo {
	totalCPU := uint64(runtime.NumCPU() * int(types.DefaultCPUPeriod))
	totalAllocatableCPU := uint64(float64(totalCPU) * 0.9)
	allocatable := totalAllocatableCPU

	c.LCPodMap.Range(func(key, info interface{}) bool {
		pInfo := info.(*PodInfo)
		allocatable -= pInfo.PodResource.CPU.Quota
		return true
	})

	nodeResourceInfo := ResourceInfo{
		CPU: &CPUResourceInfo{
			Total:            totalCPU,
			TotalAllocatable: totalAllocatableCPU,
			Allocatable:      allocatable,
		},
	}

	return &nodeResourceInfo
}

func (c *PodManager) CollectNodeMetrics() *NodeMetrics {
	resourceInfo := c.NodeResourceInfo()
	return &NodeMetrics{
		NodeName:       c.NodeName,
		AllocatableCPU: uint64(float64(resourceInfo.CPU.Allocatable) / float64(types.DefaultCPUPeriod)),
	}
}

func (c *PodManager) CollectPodMetrics() []*PodMetrics {
	retval := make([]*PodMetrics, 0)

	for _, m := range []*sync.Map{
		&c.LCPodMap,
		&c.BEPodMap,
		&c.UncontrolledPodMap,
	} {
		m.Range(func(podKey, info interface{}) bool {
			pInfo := info.(*PodInfo)
			p, err := c.collectPodMetrics(pInfo)
			if err != nil {
				klog.ErrorS(err, "collect pod metrics failed")
				return true
			}
			retval = append(retval, p)
			return true
		})
	}

	return retval
}

func (m *PodManager) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)

	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), m.PodSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	var err error
	m.BesteffortCgroup, err = cgroup.LoadBesteffortCgroup()
	if err != nil {
		return err
	}
	m.BurstableCgroup, err = cgroup.LoadBurstableCgroup()
	if err != nil {
		return err
	}

	// Launch worker to process Pod resources
	for i := 0; i < 64; i++ {
		go wait.UntilWithContext(ctx, m.runWorker, time.Second)
	}

	cpuScaler := NewCPUScaler(m)

	logger.Info("Start CPU scaler")
	go cpuScaler.Start(context.TODO())

	server := &http.Server{
		Addr:         "0.0.0.0:10000",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	mux := http.NewServeMux()
	mux.Handle("/stats", &Monitor{
		manager: m,
	})

	server.Handler = mux
	go server.ListenAndServe()

	<-ctx.Done()

	return nil
}

func NewPodManager(nodeName string, clientset *kubernetes.Clientset, informerFactory kubeinformers.SharedInformerFactory) *PodManager {
	podInformer := informerFactory.Core().V1().Pods()
	podmanager := &PodManager{
		NodeName:    nodeName,
		Cores:       uint64(runtime.NumCPU()),
		client:      clientset,
		PodInformer: podInformer,
		PodLister:   podInformer.Lister(),
		PodSynced:   podInformer.Informer().HasSynced,
		workqueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "PodManager"),
	}

	podmanager.PodInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: podmanager.enqueuePod,
		UpdateFunc: func(old, new interface{}) {
			oldP := old.(*corev1.Pod)
			newP := new.(*corev1.Pod)
			if oldP.ResourceVersion == newP.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			podmanager.enqueuePod(new)
		},
		DeleteFunc: podmanager.enqueuePod,
	})
	return podmanager
}
