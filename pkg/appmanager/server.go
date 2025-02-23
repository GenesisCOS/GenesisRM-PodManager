package appmanager

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	kubeinformers "k8s.io/client-go/informers"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	skinformers "swiftkube.io/swiftkube/pkg/generated/informers/externalversions"
	"swiftkube.io/swiftkube/pkg/signals"
)

type ApplicationManager struct {
	kubeclient          kubernetes.Interface
	kubeinformerfactory kubeinformers.SharedInformerFactory

	podInformer coreinformers.PodInformer
	podLister   corelisters.PodLister
	podSynced   cache.InformerSynced

	deployInformer appsinformers.DeploymentInformer
	deployLister   appslisters.DeploymentLister
	deploySynced   cache.InformerSynced

	nodeInformer coreinformers.NodeInformer
	nodeLister   corelisters.NodeLister
	nodeSynced   cache.InformerSynced
}

func (manager *ApplicationManager) Run(ctx context.Context) error {
	logger := klog.FromContext(ctx)

	server := &http.Server{
		Addr:         "0.0.0.0:10001",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	mux := http.NewServeMux()
	mux.Handle("/stat", &statHandler{appmanager: manager})

	server.Handler = mux
	go server.ListenAndServe()

	deployHelperWS := NewDeploymentHelperWebService(manager)
	nodeHelperWS := NewNodeHelperWebService(manager)

	restful.DefaultContainer.Add(deployHelperWS.WebService())
	restful.DefaultContainer.Add(nodeHelperWS.WebService())

	manager.kubeinformerfactory.Start(ctx.Done())
	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(
		ctx.Done(),
		manager.podSynced,
		manager.deploySynced,
		manager.nodeSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	log.Fatal(http.ListenAndServe(":10000", nil))

	<-ctx.Done()

	server.Close()
	return nil
}

type ControllerContext struct {
	KubeInformerFactory kubeinformers.SharedInformerFactory
	SkInformerFactory   skinformers.SharedInformerFactory
	ClientBuilder       ClientBuilder
}

type Options struct {
	KubeConfig string
	MasterURL  string
	Worker     int
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.KubeConfig, "kubeconfig", o.KubeConfig, "Path to a kubeconfig. Only required if out-of-cluster.")
	fs.StringVar(&o.MasterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	fs.IntVar(&o.Worker, "worker", o.Worker, "Worker number")
}

func NewOptions() *Options {
	u, err := user.Current()
	if err != nil {
		klog.Error(err)
		os.Exit(-1)
	}
	homeDir := u.HomeDir

	return &Options{
		KubeConfig: fmt.Sprintf("%s/.kube/config", homeDir),
		Worker:     8,
	}
}

type ClientBuilder struct {
	cfg *rest.Config
}

func (c *ClientBuilder) KubeClientOrDie() kubernetes.Interface {
	kubeClient, err := kubernetes.NewForConfig(c.cfg)
	if err != nil {
		klog.Fatal(err)
	}
	return kubeClient
}

/*
func (c *ClientBuilder) SkClientOrDie() skclientset.Interface {
	skClient, err := skclientset.NewForConfig(c.cfg)
	if err != nil {
		klog.Fatal(err)
	}
	return skClient
}
*/

func NewApplicationManagerCommand() *cobra.Command {
	opts := NewOptions()
	klog.InitFlags(nil)

	cmd := &cobra.Command{
		Use:  "appmanager",
		Long: "TODO",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := signals.SetupSignalHandler()

			cfg, err := rest.InClusterConfig()
			if err != nil {
				klog.ErrorS(err, "Error creating in-cluster configuration")
				cfg, err = clientcmd.BuildConfigFromFlags(opts.MasterURL, opts.KubeConfig)
				if err != nil {
					klog.ErrorS(err, "Error building kubeconfig")
					klog.FlushAndExit(klog.ExitFlushTimeout, 1)
				}
			}

			clientBuilder := ClientBuilder{
				cfg: cfg,
			}

			kubeclient := clientBuilder.KubeClientOrDie()
			//skClient := clientBuilder.SkClientOrDie()

			kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeclient, time.Second*30)
			//skInformerFactory := skinformers.NewSharedInformerFactory(skClient, time.Second*30)

			podInformer := kubeInformerFactory.Core().V1().Pods()
			deployInformer := kubeInformerFactory.Apps().V1().Deployments()
			nodeInformer := kubeInformerFactory.Core().V1().Nodes()

			appManager := &ApplicationManager{
				kubeclient:          kubeclient,
				kubeinformerfactory: kubeInformerFactory,

				podInformer: podInformer,
				podLister:   podInformer.Lister(),
				podSynced:   podInformer.Informer().HasSynced,

				deployInformer: deployInformer,
				deployLister:   deployInformer.Lister(),
				deploySynced:   deployInformer.Informer().HasSynced,

				nodeInformer: nodeInformer,
				nodeLister:   nodeInformer.Lister(),
				nodeSynced:   nodeInformer.Informer().HasSynced,
			}
			/*
				controllerContext := ControllerContext{
					KubeInformerFactory: kubeInformerFactory,
					SkInformerFactory:   skInformerFactory,
					ClientBuilder:       clientBuilder,
				}
			*/

			//startController(ctx, controllerContext)

			//skInformerFactory.Start(ctx.Done())

			return appManager.Run(ctx)
			//return Run(ctx)
		},
		Args: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				if len(arg) > 0 {
					return fmt.Errorf("%q does not take any arguments, got %q", cmd.CommandPath(), args)
				}
			}
			return nil
		},
	}

	opts.AddFlags(cmd.Flags())
	return cmd
}

/*
func Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
*/

/*
func startController(ctx context.Context, controllerContext ControllerContext) error {
	err := startSwiftDeploymentController(ctx, controllerContext)
	if err != nil {
		klog.Error(err, "Error building kukbeconfig")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	return nil
}

func startSwiftDeploymentController(ctx context.Context, controllerContext ControllerContext) error {
	sdc, err := sdccontroller.NewSwiftDeploymentController(
		ctx,
		controllerContext.ClientBuilder.KubeClientOrDie(),
		controllerContext.ClientBuilder.SkClientOrDie(),
		controllerContext.KubeInformerFactory.Apps().V1().Deployments(),
		controllerContext.KubeInformerFactory.Core().V1().Services(),
		controllerContext.KubeInformerFactory.Core().V1().Pods(),
		controllerContext.SkInformerFactory.Swiftkube().V1alpha1().SwiftDeployments(),
	)
	if err != nil {
		klog.Error(err, "Error creating SwiftDeployment controller")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
	go sdc.Run(ctx, 2)
	return err
}
*/
