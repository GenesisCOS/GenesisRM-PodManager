package app

import (
	"os"
	"time"

	"github.com/spf13/cobra"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	kuberest "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	pm "swiftkube.io/swiftkube/pkg/podmanager"
	"swiftkube.io/swiftkube/pkg/signals"
)

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

			podManager := pm.NewPodManager(hostname, kubeClient, kubeInformerFactory)
			go podManager.Run(ctx)

			kubeInformerFactory.Start(ctx.Done())

			<-ctx.Done()

			return nil
		},
	}

	return cmd
}
