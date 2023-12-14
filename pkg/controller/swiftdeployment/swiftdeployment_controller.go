package swiftdeployment

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	swiftdeploymentv1alpha1 "swiftkube.io/swiftkube/pkg/apis/swiftdeploymentcontroller/v1alpha1"
	skclientset "swiftkube.io/swiftkube/pkg/generated/clientset/versioned"
	skscheme "swiftkube.io/swiftkube/pkg/generated/clientset/versioned/scheme"
	skinformers "swiftkube.io/swiftkube/pkg/generated/informers/externalversions/swiftdeploymentcontroller/v1alpha1"
	sklisters "swiftkube.io/swiftkube/pkg/generated/listers/swiftdeploymentcontroller/v1alpha1"
)

const (
	// SuccessSynced is used as part of the Event 'reason' when a SwiftDeployment is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a SwiftDeployment fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by SwiftDeployment"
	// MessageResourceSynced is the message used for an Event fired when a SwiftDeployment
	// is synced successfully
	MessageResourceSynced = "SwiftDeployment synced successfully"
)

type SwiftDeploymentController struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// skclientset is a clientset for our own API group (swiftkube.io)
	skclientset skclientset.Interface

	dLister  appslisters.DeploymentLister
	dSynced  cache.InformerSynced
	sLister  corelisters.ServiceLister
	sSynced  cache.InformerSynced
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
	kubeclientset kubernetes.Interface,
	skclientset skclientset.Interface,
	dInformer appsinformers.DeploymentInformer,
	sInformer coreinformers.ServiceInformer,
	pInformer coreinformers.PodInformer,
	sdInformer skinformers.SwiftDeploymentInformer) (*SwiftDeploymentController, error) {
	logger := klog.FromContext(ctx)

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilruntime.Must(skscheme.AddToScheme(scheme.Scheme))
	logger.V(4).Info("Creating event broadcaster")

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "swiftdeployment-controller"})

	sdc := &SwiftDeploymentController{
		kubeclientset: kubeclientset,
		skclientset:   skclientset,
		dLister:       dInformer.Lister(),
		dSynced:       dInformer.Informer().HasSynced,
		sLister:       sInformer.Lister(),
		sSynced:       sInformer.Informer().HasSynced,
		sdLister:      sdInformer.Lister(),
		sdSynced:      sdInformer.Informer().HasSynced,
		pLister:       pInformer.Lister(),
		pSynced:       pInformer.Informer().HasSynced,
		workqueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "SwiftDeployments"),
		recorder:      recorder,
	}

	sdInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: sdc.enqueueSwiftDeployment,
		UpdateFunc: func(old, new interface{}) {
			newSDepl := new.(*swiftdeploymentv1alpha1.SwiftDeployment)
			oldSDepl := old.(*swiftdeploymentv1alpha1.SwiftDeployment)
			if newSDepl.ResourceVersion == oldSDepl.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			sdc.enqueueSwiftDeployment(new)
		},
	})

	// Set up an event handler for when Deployment resources change. This
	// handler will lookup the owner of the given Deployment, and if it is
	// owned by a SwiftDeployment resource then the handler will enqueue that
	// SwiftDeployment resource for processing.
	// This way, we don't need to implement custom logic for
	// handling Deployment resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	dInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: sdc.handleDeployment,
		UpdateFunc: func(old, new interface{}) {
			newDepl := new.(*appsv1.Deployment)
			oldDepl := old.(*appsv1.Deployment)
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same Deployment will always have different RVs.
				return
			}
			sdc.handleDeployment(new)
		},
		DeleteFunc: sdc.handleDeployment,
	})

	return sdc, nil
}

func (c *SwiftDeploymentController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()
	logger := klog.FromContext(ctx)

	// Wait for the caches to be synced before starting workers
	logger.Info("Waiting for informer caches to sync")

	if ok := cache.WaitForCacheSync(ctx.Done(), c.dSynced, c.sdSynced, c.sSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	// Launch workers to process SwiftDeployment resources
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")
	<-ctx.Done()
	logger.Info("Shutting down workers")

	return nil
}

// enqueueSwiftDeployment takes a SwiftDeployment resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than SwiftDeployment.
func (c *SwiftDeploymentController) enqueueSwiftDeployment(obj interface{}) {
	var key string
	var err error

	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// handleDeployment will take any resource implementing metav1.Object and attempt
// to find the SwiftDeployment resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that SwiftDeployment resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *SwiftDeploymentController) handleDeployment(obj interface{}) {
	var object metav1.Object
	var ok bool
	logger := klog.FromContext(context.Background())
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		logger.V(4).Info("Recovered deleted object", "resourceName", object.GetName())
	}
	logger.V(4).Info("Processing object", "object", klog.KObj(object))
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a SwiftDeployment, we should not do anything more
		// with it.
		if ownerRef.Kind != "SwiftDeployment" {
			return
		}

		sd, err := c.sdLister.SwiftDeployments(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			logger.V(4).Info("Ignore orphaned object", "object", klog.KObj(object), "swiftdeployment", ownerRef.Name)
			return
		}

		c.enqueueSwiftDeployment(sd)
		return
	}
}

func (c *SwiftDeploymentController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *SwiftDeploymentController) processNextWorkItem(ctx context.Context) bool {
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

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the SwiftDeployment resource
// with the current status of the resource.
func (c *SwiftDeploymentController) syncHandler(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	logger := klog.LoggerWithValues(klog.FromContext(ctx), "resourceName", key)

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the SwiftDeployment resource with this namespace/name
	sd, err := c.sdLister.SwiftDeployments(namespace).Get(name)
	if err != nil {
		// The SwiftDeployment resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("swiftdeployment '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}

	needToUpdate := false
	sdCopy := sd.DeepCopy()

	// RunningReplicas must be less than or equal to Replicas
	if *sdCopy.Spec.RunningReplicas > *sdCopy.Spec.Replicas {
		utilruntime.HandleError(fmt.Errorf("swiftdeployment '%s' RunningReplicas must be less than or equal to Replicas", key))
		*sdCopy.Spec.RunningReplicas = *sdCopy.Spec.Replicas
	}

	if needToUpdate {
		_, err := c.skclientset.SwiftkubeV1alpha1().SwiftDeployments(sdCopy.Namespace).Update(context.TODO(), sdCopy, metav1.UpdateOptions{})
		return err
	}

	// Get the deployment
	deployment, err := c.dLister.Deployments(sd.Namespace).Get(getDeploymentName(sd.Name))
	// If the resource doesn't exist, we'll create it
	if errors.IsNotFound(err) {
		logger.Info(fmt.Sprintf("deployment '%s' does not exist, create it", getDeploymentName(sd.Name)))
		deployment, err = c.kubeclientset.AppsV1().Deployments(sd.Namespace).Create(context.TODO(), newDeployment(sd), metav1.CreateOptions{})
	}

	// If an error occurs during Get/Create, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// Get the service
	service, err := c.sLister.Services(sd.Namespace).Get(sd.Spec.ServiceName)
	// If the resource doesn't exist, we'll create it
	if errors.IsNotFound(err) {
		logger.Info(fmt.Sprintf("service '%s' does not exist, create it", sd.Spec.ServiceName))
		service, err = c.kubeclientset.CoreV1().Services(sd.Namespace).Create(context.TODO(), newService(sd), metav1.CreateOptions{})
	}

	// If an error occurs during Get/Create, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// If the Deployment is not controlled by this SwiftDeployment resource, we should log
	// a warning to the event recorder and return error msg.
	if !metav1.IsControlledBy(deployment, sd) {
		msg := fmt.Sprintf(MessageResourceExists, deployment.Name)
		c.recorder.Event(sd, corev1.EventTypeWarning, ErrResourceExists, msg)
		return fmt.Errorf("%s", msg)
	}

	// If the Service is not controlled by this SwiftDeployment resource, we should log
	// a warning to the event recorder and return error msg.
	if !metav1.IsControlledBy(service, sd) {
		msg := fmt.Sprintf(MessageResourceExists, service.Name)
		c.recorder.Event(sd, corev1.EventTypeWarning, ErrResourceExists, msg)
		return fmt.Errorf("%s", msg)
	}

	// Initialize swiftkube label of pods
	pods, err := c.getAllPodsFromDeployment(deployment)
	if err != nil {
		return err
	}
	for _, pod := range pods {
		_, found := pod.GetLabels()["swiftkube.io/state"]
		if !found { // pod do not contain label "swiftkube.io/state", add it
			podCopy := pod.DeepCopy()
			podCopy.GetLabels()["swiftkube.io/state"] = "running"
			_, err = c.kubeclientset.CoreV1().Pods(sd.Namespace).Update(context.TODO(), podCopy, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}

	// If this number of the replicas on the SwiftDeployment resource is specified, and the
	// number does not equal the current desired replicas on the Deployment, we
	// should update the Deployment resource.
	if sd.Spec.Replicas != nil && *sd.Spec.Replicas != *deployment.Spec.Replicas {
		logger.V(4).Info("Update deployment resource", "currentReplicas", *sd.Spec.Replicas, "desiredReplicas", *deployment.Spec.Replicas)
		deployment, err = c.kubeclientset.AppsV1().Deployments(sd.Namespace).Update(context.TODO(), newDeployment(sd), metav1.UpdateOptions{})
	}

	// If an error occurs during Update, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// Sync number of running pod
	if deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
		err = c.syncRunningPod(sd, deployment)
		if err != nil {
			return err
		}
	}

	// Finally, we update the status block of the SwiftDeployment resource to reflect the
	// current state of the world
	err = c.updateSwiftDeploymentStatus(sd, deployment)
	if err != nil {
		return err
	}

	c.recorder.Event(sd, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)

	return nil
}

func (c *SwiftDeploymentController) syncRunningPod(sd *swiftdeploymentv1alpha1.SwiftDeployment, deployment *appsv1.Deployment) error {
	rPods, err := c.getRunningPodsFromDeployment(deployment)
	if err != nil {
		return err
	}
	sPods, err := c.getSleepingPodsFromDeployment(deployment)
	if err != nil {
		return err
	}
	curRunningPodNumber := len(rPods)
	if curRunningPodNumber > int(*sd.Spec.RunningReplicas) {
		n := curRunningPodNumber - int(*sd.Spec.RunningReplicas)
		podsToSuspend := SelectPodsToSuspend(rPods, n, SuspendInOrder)
		for _, pod := range podsToSuspend {
			podCopy := pod.DeepCopy()
			podCopy.GetLabels()["swiftkube.io/state"] = "sleeping"
			podCopy.GetLabels()["swiftkube.io/sleep"] = "wait"
			_, _ = c.kubeclientset.CoreV1().Pods(sd.Namespace).Update(context.TODO(), podCopy, metav1.UpdateOptions{})
		}
	} else if curRunningPodNumber < int(*sd.Spec.RunningReplicas) {
		n := int(*sd.Spec.RunningReplicas) - curRunningPodNumber
		podsToActivate := SelectPodsToActivate(sPods, n, ActivateInOrder)
		for _, pod := range podsToActivate {
			podCopy := pod.DeepCopy()
			podCopy.GetLabels()["swiftkube.io/state"] = "runnable"
			_, _ = c.kubeclientset.CoreV1().Pods(sd.Namespace).Update(context.TODO(), podCopy, metav1.UpdateOptions{})
		}
	}
	return nil
}

func (c *SwiftDeploymentController) updateSwiftDeploymentStatus(sd *swiftdeploymentv1alpha1.SwiftDeployment, deployment *appsv1.Deployment) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance
	sdCopy := sd.DeepCopy()
	rPods, err := c.getRunningPodsFromDeployment(deployment)
	if err != nil {
		return err
	}
	sdCopy.Status.RunningReplicas = int32(len(rPods))
	sdCopy.Status.AvailableReplicas = deployment.Status.AvailableReplicas

	// If the *CustomResourceSubresources* feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the SwiftDeployment resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	// _, err := c.skclientset.SwiftkubeV1alpha1().SwiftDeployments(sd.Namespace).UpdateStatus(context.TODO(), sdCopy, metav1.UpdateOptions{})
	_, err = c.skclientset.SwiftkubeV1alpha1().SwiftDeployments(sd.Namespace).Update(context.TODO(), sdCopy, metav1.UpdateOptions{})
	return err
}

func getDeploymentName(sdName string) string {
	return sdName + "-swiftkube.io"
}

func (c *SwiftDeploymentController) getAllPodsFromDeployment(d *appsv1.Deployment) ([]*corev1.Pod, error) {
	return c.getPodsFromDeployment(d)
}

func (c *SwiftDeploymentController) getRunningPodsFromDeployment(d *appsv1.Deployment) ([]*corev1.Pod, error) {
	r, err := labels.NewRequirement("swiftkube.io/state", selection.In, []string{"running", "runnable"})
	if err != nil {
		return nil, err
	}
	return c.getPodsFromDeployment(d, *r)
}

func (c *SwiftDeploymentController) getSleepingPodsFromDeployment(d *appsv1.Deployment) ([]*corev1.Pod, error) {
	r, err := labels.NewRequirement("swiftkube.io/state", selection.Equals, []string{"sleeping"})
	if err != nil {
		return nil, err
	}
	return c.getPodsFromDeployment(d, *r)
}

func (c *SwiftDeploymentController) getPodsFromDeployment(d *appsv1.Deployment, r ...labels.Requirement) ([]*corev1.Pod, error) {
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	for _, requirement := range r {
		selector = selector.Add(requirement)
	}
	if err != nil {
		return nil, err
	}
	return c.pLister.Pods(d.Namespace).List(selector)
}

func newDeployment(sd *swiftdeploymentv1alpha1.SwiftDeployment) *appsv1.Deployment {
	sdCopy := sd.DeepCopy()
	sdCopy.Spec.DeploymentTemplate.Spec.Replicas = sdCopy.Spec.Replicas

	// Set *ResizePolicy*, Pod will not restart when resource requests and limits is updated
	for i := range sdCopy.Spec.DeploymentTemplate.Spec.Template.Spec.Containers {
		sdCopy.Spec.DeploymentTemplate.Spec.Template.Spec.Containers[i].ResizePolicy = []corev1.ContainerResizePolicy{}
		sdCopy.Spec.DeploymentTemplate.Spec.Template.Spec.Containers[i].ResizePolicy =
			append(sdCopy.Spec.DeploymentTemplate.Spec.Template.Spec.Containers[i].ResizePolicy, corev1.ContainerResizePolicy{
				ResourceName:  corev1.ResourceCPU,
				RestartPolicy: corev1.NotRequired,
			})
		sdCopy.Spec.DeploymentTemplate.Spec.Template.Spec.Containers[i].ResizePolicy =
			append(sdCopy.Spec.DeploymentTemplate.Spec.Template.Spec.Containers[i].ResizePolicy, corev1.ContainerResizePolicy{
				ResourceName:  corev1.ResourceMemory,
				RestartPolicy: corev1.NotRequired,
			})
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getDeploymentName(sdCopy.Name),
			Namespace: sdCopy.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(sdCopy, swiftdeploymentv1alpha1.SchemeGroupVersion.WithKind("SwiftDeployment")),
			},
		},
		Spec: sdCopy.Spec.DeploymentTemplate.Spec,
	}
}

func newService(sd *swiftdeploymentv1alpha1.SwiftDeployment) *corev1.Service {
	sdCopy := sd.DeepCopy()

	sdCopy.Spec.ServiceTemplate.Spec.Selector["swiftkube.io/state"] = "running"
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sdCopy.Spec.ServiceName,
			Namespace: sdCopy.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(sdCopy, swiftdeploymentv1alpha1.SchemeGroupVersion.WithKind("SwiftDeployment")),
			},
		},
		Spec: sdCopy.Spec.ServiceTemplate.Spec,
	}
}
