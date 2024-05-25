package main

import (
	goflag "flag"
	"os"

	"github.com/spf13/pflag"

	"swiftkube.io/swiftkube/cmd/podmanager/app"
)

func main() {
	command := app.NewPodManagerCommand()

	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
