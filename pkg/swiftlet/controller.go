package swiftlet

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	internalapi "k8s.io/cri-api/pkg/apis"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cri/remote"

	skclientset "swiftkube.io/swiftkube/pkg/generated/clientset/versioned"
	skscheme "swiftkube.io/swiftkube/pkg/generated/clientset/versioned/scheme"
	skinformers "swiftkube.io/swiftkube/pkg/generated/informers/externalversions/swiftdeploymentcontroller/v1alpha1"
	sklisters "swiftkube.io/swiftkube/pkg/generated/listers/swiftdeploymentcontroller/v1alpha1"
)

type ContainerStatusInfo struct {
	Pid int `json:"pid"`
}

type SwiftletController struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// skclientset is a clientset for our own API group (swiftkube.io)
	skclientset skclientset.Interface

	nodeName string

	sdLister sklisters.SwiftDeploymentLister
	sdSynced cache.InformerSynced
	pLister  corelisters.PodLister
	pSynced  cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

func NewSwiftDeploymentController(
	ctx context.Context,
	nodeName string,
	kubeclientset kubernetes.Interface,
	skclientset skclientset.Interface,
	pInformer coreinformers.PodInformer,
	sdInformer skinformers.SwiftDeploymentInformer) (*SwiftletController, error) {
	logger := klog.FromContext(ctx)

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilruntime.Must(skscheme.AddToScheme(skscheme.Scheme))
	logger.V(4).Info("Creating event broadcaster")

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(skscheme.Scheme, corev1.EventSource{Component: "swiftlet"})

	sdc := &SwiftletController{
		kubeclientset: kubeclientset,
		skclientset:   skclientset,
		nodeName:      nodeName,
		sdLister:      sdInformer.Lister(),
		sdSynced:      sdInformer.Informer().HasSynced,
		pLister:       pInformer.Lister(),
		pSynced:       pInformer.Informer().HasSynced,
		workqueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "SwiftDeployments"),
		recorder:      recorder,
	}

	pInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: sdc.enqueuePod,
		UpdateFunc: func(old, new interface{}) {
			oldP := old.(*corev1.Pod)
			newP := new.(*corev1.Pod)
			if oldP.ResourceVersion == newP.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			sdc.enqueuePod(new)
		},
	})

	return sdc, nil
}

func (c *SwiftletController) Run(ctx context.Context) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()
	logger := klog.FromContext(ctx)

	// Wait for the caches to be synced before starting workers
	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.pSynced, c.sdSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// Launch worker to process Pod resources
	go wait.UntilWithContext(ctx, c.runWorker, time.Second)

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down workers")

	return nil
}

func (c *SwiftletController) enqueuePod(obj interface{}) {
	var key string
	var err error

	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

func (c *SwiftletController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *SwiftletController) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := c.workqueue.Get()
	logger := klog.FromContext(ctx)

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
		logger.Info("Successfully synced", "resourceName", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (c *SwiftletController) syncHandler(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "resourceName", key)

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Pod resource with this namespace/name
	pod, err := c.pLister.Pods(namespace).Get(name)
	if err != nil {
		// The Pod resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("pod '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}

	if pod.Spec.NodeName != c.nodeName {
		return nil
	}

	state, found := pod.GetLabels()["swiftkube.io/state"]

	// If there is no label swiftkube.io/state, it indicates that
	// Pod is not controlled by SwiftKube
	if !found {
		return nil
	}

	podUid := string(pod.GetUID())
	podQosClass := string(pod.Status.QOSClass)
	podCgroup := getPodSystemdCgroup(podUid, podQosClass)
	podCopy := pod.DeepCopy()

	if state == "runnable" {
		logger.Info(fmt.Sprintf("Activate Pod %s/%s", pod.Namespace, pod.Name))

		// Activation process ...
		for _, container := range pod.Status.ContainerStatuses {
			engine, id := splitContainerID(container.ContainerID)
			containerCgroup := getContainerSystemdCgroup(podCgroup, id, engine)
			klog.Info(fmt.Sprintf("Set %s to max", containerCgroup+"/memory.high"))
			// TODO 1. Recover memory.high
			// TODO 2. Reload memory pages from swap space
		}

		podCopy.GetLabels()["swiftkube.io/state"] = "running"
		// TODO 3. Update Pod memory request
		_, err = c.kubeclientset.CoreV1().Pods(podCopy.Namespace).Update(context.TODO(), podCopy, metav1.UpdateOptions{})
	} else if state == "sleeping" {
		if sleep := pod.GetLabels()["swiftkube.io/sleep"]; sleep == "done" {
			return nil
		}

		logger.Info(fmt.Sprintf("Suspend Pod %s/%s", pod.Namespace, pod.Name))

		// Suspension process ...
		for _, container := range pod.Status.ContainerStatuses {
			engine, id := splitContainerID(container.ContainerID)
			containerCgroup := getContainerSystemdCgroup(podCgroup, id, engine)
			klog.Info(fmt.Sprintf("Set %s to 0", containerCgroup+"/memory.high"))
			// TODO 1. Set memory.high to 0
		}

		podCopy.GetLabels()["swiftkube.io/sleep"] = "done"
		// TODO 2. Update Pod memory request (must enable InPlacePodVerticalScaling feature gate)
		_, err = c.kubeclientset.CoreV1().Pods(podCopy.Namespace).Update(context.TODO(), podCopy, metav1.UpdateOptions{})
	}

	if err != nil {
		return err
	}

	return nil
}

// TODO remove hardcode URL
func getRuntimeService() (internalapi.RuntimeService, error) {
	return remote.NewRemoteRuntimeService("unix:///run/containerd/containerd.sock", 5*time.Second, nil)
}

// TODO remove hardcode URL
func getImageService() (internalapi.ImageManagerService, error) {
	return remote.NewRemoteImageService("unix:///run/containerd/containerd.sock", 5*time.Second, nil)
}

func getPodSystemdCgroup(uid string, qosClass string) string {
	uid = strings.ReplaceAll(uid, "-", "_")
	cgroup := fmt.Sprintf("/kubepods.slice/kubepods-%s.slice/kubepods-%s-pod%s.slice", qosClass, qosClass, uid)
	return "/sys/fs/cgroup" + cgroup
}

func getContainerSystemdCgroup(podCgroup string, containerID string, engine string) string {
	return podCgroup + fmt.Sprintf("/cri-%s-%s.scope", engine, containerID)
}

func splitContainerID(id string) (string, string) {
	idSplited := strings.Split(id, "://") // containerd://f64c76a100ea2f2dd7da7736fae0b453a9c3b9a119b9f5d097a006fe5c5312ed
	engine := idSplited[0]                // containerd
	containerID := idSplited[1]           // f64c76a100ea2f2dd7da7736fae0b453a9c3b9a119b9f5d097a006fe5c5312ed
	return engine, containerID
}
