package main

import (
	"context"
	"errors"
	goflag "flag"
	_ "fmt"
	"os"

	"github.com/spf13/pflag"
	kubelet "k8s.io/kubernetes/cmd/kubelet/app"

	"swiftkube.io/swiftkube/pkg/podmanager"
)

func main() {
	command := podmanager.NewPodManagerCommand()

	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}

func runKubelet(ctx context.Context) error {
	command := kubelet.NewKubeletCommand()

	err := command.ExecuteContext(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		os.Exit(1)
	}

	return nil
}
