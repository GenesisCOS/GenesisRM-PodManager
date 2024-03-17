package main

import (
	goflag "flag"
	"os"

	"github.com/spf13/pflag"

	podmanager "swiftkube.io/swiftkube/pkg/podmanager"
)

func main() {
	command := podmanager.NewPodManagerCommand()

	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
