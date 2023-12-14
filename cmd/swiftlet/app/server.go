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
	kuberest "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"swiftkube.io/swiftkube/pkg/signals"

	skclientset "swiftkube.io/swiftkube/pkg/generated/clientset/versioned"
	skinformers "swiftkube.io/swiftkube/pkg/generated/informers/externalversions"
	swiftlet "swiftkube.io/swiftkube/pkg/swiftlet"
)

type Options struct {
	KubeConfig string
	MasterURL  string
	Hostname   string
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.KubeConfig, "kubeconfig", o.KubeConfig, "Path to a kubeconfig. Only required if out-of-cluster.")
	fs.StringVar(&o.MasterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	fs.StringVar(&o.Hostname, "hostname", o.Hostname, "Hostname.")
}

func NewOptions() *Options {
	u, err := user.Current()
	if err != nil {
		klog.Error(err)
		os.Exit(-1)
	}
	homeDir := u.HomeDir

	hostname, err := os.Hostname()
	val, found := os.LookupEnv("MY_NODE_NAME")
	if found {
		hostname = val
	}
	if err != nil {
		klog.Error(err)
		os.Exit(-1)
	}

	return &Options{
		KubeConfig: fmt.Sprintf("%s/.kube/config", homeDir),
		Hostname:   hostname,
	}
}

func NewSwiftletCommand() *cobra.Command {
	opts := NewOptions()
	klog.InitFlags(nil)

	cmd := &cobra.Command{
		Use:  "swiftlet",
		Long: "TODO",
		RunE: func(cmd *cobra.Command, args []string) error {
			klog.Info("Swiftlet Starting ...")
			ctx := signals.SetupSignalHandler()

			cfg, err := kuberest.InClusterConfig()
			if err != nil {
				klog.Error(err, "Error creating in-cluster configuration")
				cfg, err = clientcmd.BuildConfigFromFlags(opts.MasterURL, opts.KubeConfig)
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

			skClient, err := skclientset.NewForConfig(cfg)
			if err != nil {
				klog.Error(err, "Error building SwiftKube client")
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			}

			kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)
			skInformerFactory := skinformers.NewSharedInformerFactory(skClient, time.Second*30)

			swiftletController, err := swiftlet.NewSwiftDeploymentController(
				ctx, opts.Hostname, kubeClient, skClient,
				kubeInformerFactory.Core().V1().Pods(),
				skInformerFactory.Swiftkube().V1alpha1().SwiftDeployments(),
			)
			go swiftletController.Run(ctx)

			kubeInformerFactory.Start(ctx.Done())
			skInformerFactory.Start(ctx.Done())

			if err != nil {
				klog.Error(err, "Error building Swiftlet Controller")
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			}

			return Run(ctx)
		},
	}

	opts.AddFlags(cmd.Flags())
	return cmd
}

func Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
