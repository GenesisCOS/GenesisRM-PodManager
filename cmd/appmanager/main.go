package main

import (
	goflag "flag"
	"os"

	"github.com/spf13/pflag"

	"swiftkube.io/swiftkube/cmd/appmanager/app"
)

func main() {
	command := app.NewApplicationManagerCommand()

	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
