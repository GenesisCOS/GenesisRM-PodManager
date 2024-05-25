package main

import (
	goflag "flag"
	"os"

	"github.com/spf13/pflag"

	"swiftkube.io/swiftkube/pkg/appmanager"
)

func main() {
	command := appmanager.NewApplicationManagerCommand()

	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
