package main

import (
	"os"

	"k8s.io/component-base/cli"
	_ "k8s.io/component-base/logs/json/register"
	_ "k8s.io/component-base/metrics/prometheus/clientgo"
	_ "k8s.io/component-base/metrics/prometheus/version"
	kubelet "k8s.io/kubernetes/cmd/kubelet/app"
)

func main() {
	runKubelet()
}

func runKubelet() {
	command := kubelet.NewKubeletCommand()
	code := cli.Run(command)
	os.Exit(code)
}
