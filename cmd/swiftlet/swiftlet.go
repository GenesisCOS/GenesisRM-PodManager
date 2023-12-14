package main

import (
	goflag "flag"
	"os"

	"github.com/spf13/pflag"

	"swiftkube.io/swiftkube/cmd/swiftlet/app"
)

func main() {
	command := app.NewSwiftletCommand()

	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
