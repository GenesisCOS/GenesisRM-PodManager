package main

import (
	_ "fmt"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/component-base/cli"
	kubectlcmd "k8s.io/kubectl/pkg/cmd"
	kubectlcmdutil "k8s.io/kubectl/pkg/cmd/util"
)

func main() {
	runKubectl()
}

func runKubectl() {
	command := kubectlcmd.NewDefaultKubectlCommand()
	if err := cli.RunNoErrOutput(command); err != nil {
		kubectlcmdutil.CheckErr(err)
	}
}
