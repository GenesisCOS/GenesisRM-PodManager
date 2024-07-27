package podmanager

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"k8s.io/klog/v2"
	"swiftkube.io/swiftkube/pkg/helper"
	genesissdk "swiftkube.io/swiftkube/pkg/podmanager/sdk"
)

type MemController struct {
	podmanager *PodManager
}

func NewMemController(podmanager *PodManager) *MemController {
	return &MemController{
		podmanager: podmanager,
	}
}

func (s *MemController) reloadSwappages(pids []uint64) {
	if len(pids) == 0 {
		return
	}

	params := make([]string, 0)
	for _, pid := range pids {
		params = append(params, "-p")
		params = append(params, strconv.FormatUint(pid, 10))
	}

	/*
		for _, swapfile := range swapfiles {
			params = append(params, "-f")
			params = append(params, swapfile)
		}
	*/

	cmd := exec.Command(
		"reloadswappage", params...,
	)

	err := cmd.Run()
	if err != nil {
		klog.Error(err)
		return
	}
	code := cmd.ProcessState.ExitCode()
	if code != 0 {
		klog.Error(fmt.Errorf("reloadswappage return non-zero (code = %d)", code))
	}
}

func (s *MemController) Start(ctx context.Context) {
	klog.InfoS("Memory Scaler started")
	timer := time.NewTimer(time.Second)

	for {
		timer.Reset(time.Second)
		localPods, err := s.podmanager.ListControlledLocalPods()
		if err != nil {
			klog.Error(err)
			<-timer.C
			continue
		}

		for _, pod := range localPods {
			// key, _ := cache.MetaNamespaceKeyFunc(pod)
			pInfo := s.podmanager.PodInfo(pod)

			pids, err := pInfo.Cgroup.Control.Procs(true)
			if err != nil {
				klog.Error(err)
				continue // 处理下一个Pod
			}

			if len(pids) == 0 {
				klog.Error(fmt.Errorf("get 0 pid"))
			}

			state := helper.GetPodState(pod)
			endpoint := helper.GetPodEndpointState(pod)

			if state == genesissdk.POD_READY_LONGNAP_STATE {
				if endpoint == genesissdk.ENDPOINT_UP || endpoint == genesissdk.ENDPOINT_UNKNOWN {
					s.podmanager.UpdatePodMemoryHigh(pInfo, genesissdk.DefaultMemoryHigh)
					pInfo.MemoryState = genesissdk.MEMORY_MAX
				} else {
					s.podmanager.UpdatePodMemoryHigh(pInfo, 0)
					pInfo.MemoryState = genesissdk.MEMORY_SWAPPED
				}
			} else {
				if endpoint == genesissdk.ENDPOINT_DOWN {
					// reload_swappage
					s.reloadSwappages(pids)
				}
				s.podmanager.UpdatePodMemoryHigh(pInfo, genesissdk.DefaultMemoryHigh)
				pInfo.MemoryState = genesissdk.MEMORY_MAX
			}
		}

		<-timer.C
	}
}
