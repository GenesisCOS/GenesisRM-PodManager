package main

import (
	"os"

	"k8s.io/component-base/cli"
	_ "k8s.io/component-base/metrics/prometheus/restclient" // for client metric registration
	_ "k8s.io/component-base/metrics/prometheus/version"    // for version metric registration
	"k8s.io/kubernetes/cmd/kube-proxy/app"
)

func main() {
	command := app.NewProxyCommand()
	code := cli.Run(command)
	os.Exit(code)
}
