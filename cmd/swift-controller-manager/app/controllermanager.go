package app

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	sdccontroller "swiftkube.io/swiftkube/pkg/controller/swiftdeployment"
	skclientset "swiftkube.io/swiftkube/pkg/generated/clientset/versioned"
	skinformers "swiftkube.io/swiftkube/pkg/generated/informers/externalversions"
	"swiftkube.io/swiftkube/pkg/signals"
)

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

func (c *ClientBuilder) SkClientOrDie() skclientset.Interface {
	skClient, err := skclientset.NewForConfig(c.cfg)
	if err != nil {
		klog.Fatal(err)
	}
	return skClient
}

func NewControllerManagerCommand() *cobra.Command {
	opts := NewOptions()
	klog.InitFlags(nil)

	cmd := &cobra.Command{
		Use:  "swift-controller-manager",
		Long: "TODO",
		RunE: func(cmd *cobra.Command, args []string) error {
			klog.Info("SwiftKube Controller Manager Starting ...")
			ctx := signals.SetupSignalHandler()

			cfg, err := rest.InClusterConfig()
			if err != nil {
				klog.Error(err, "Error creating in-cluster configuration")
				cfg, err = clientcmd.BuildConfigFromFlags(opts.MasterURL, opts.KubeConfig)
				if err != nil {
					klog.Error(err, "Error building kubeconfig")
					klog.FlushAndExit(klog.ExitFlushTimeout, 1)
				}
			}

			clientBuilder := ClientBuilder{
				cfg: cfg,
			}

			kubeClient := clientBuilder.KubeClientOrDie()
			skClient := clientBuilder.SkClientOrDie()

			kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
			skInformerFactory := skinformers.NewSharedInformerFactory(skClient, time.Second*30)

			controllerContext := ControllerContext{
				KubeInformerFactory: kubeInformerFactory,
				SkInformerFactory:   skInformerFactory,
				ClientBuilder:       clientBuilder,
			}

			startController(ctx, controllerContext)

			kubeInformerFactory.Start(ctx.Done())
			skInformerFactory.Start(ctx.Done())

			return Run(ctx)
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

func Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

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
