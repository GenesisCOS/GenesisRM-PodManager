package podmanager

import (
	"fmt"
	"strconv"

	"github.com/containerd/cgroups/v3/cgroup2"
	"k8s.io/klog/v2"
	cgroup "swiftkube.io/swiftkube/pkg/cgroup"

	"swiftkube.io/swiftkube/pkg/podmanager/types"
)

type PodCPUResource struct {
	Quota uint64
}

type PodResource struct {
	CPU PodCPUResource
}

type CPUResourceInfo struct {
	Total            uint64
	TotalAllocatable uint64
	Allocatable      uint64
}

type ResourceInfo struct {
	CPU *CPUResourceInfo
}

func (c *PodManager) UpdateContainerGroupCPUResource(group string, quota uint64) error {
	var err error = nil
	cg := cgroup.GetContainerGroupCgroup(group)
	if cg != nil {
		period := types.DefaultCPUPeriod
		int64Quota := int64(quota)
		err = cg.Control.Update(&cgroup2.Resources{
			CPU: &cgroup2.CPU{
				Max: cgroup2.NewCPUMax(&int64Quota, &period),
			},
		})
	}
	return err
}

func cpusetString(cpus []uint64) string {
	retval := ""
	for _, v := range cpus {
		if retval != "" {
			retval += ","
		}
		retval += strconv.FormatUint(v, 10)
	}
	return retval
}

func (c *PodManager) UpdatePodCPUSetFromLower(pInfo *PodInfo, number uint64) error {
	if number > c.Cores {
		return fmt.Errorf("OutOfcpu")
	}
	cpus := make([]uint64, 0)
	for i := uint64(0); i < number; i = i + 1 {
		cpus = append(cpus, i)
	}
	return c.UpdatePodCPUSet(pInfo, cpus)
}

func (c *PodManager) UpdatePodCPUSetRange(pInfo *PodInfo, start uint64, end uint64) error {
	if start > end {
		return fmt.Errorf("range start > end (start = %d end = %d)", start, end)
	}
	cpus := make([]uint64, 0)
	for i := start; i <= end; i = i + 1 {
		cpus = append(cpus, i)
	}
	return c.UpdatePodCPUSet(pInfo, cpus)
}

func (c *PodManager) UpdatePodCPUSetFromUpper(pInfo *PodInfo, number uint64) error {
	if number > c.Cores {
		return fmt.Errorf("OutOfcpu")
	}
	cpus := make([]uint64, 0)
	for i := uint64(0); i < number; i = i + 1 {
		cpus = append([]uint64{c.Cores - 1 - i}, cpus...)
	}

	return c.UpdatePodCPUSet(pInfo, cpus)
}

func (c *PodManager) UpdatePodCPUSet(pInfo *PodInfo, cpus []uint64) error {
	err := pInfo.Cgroup.Control.Update(&cgroup2.Resources{
		CPU: &cgroup2.CPU{
			Cpus: cpusetString(cpus),
		},
	})
	if err != nil {
		klog.ErrorS(err, "update cpuset failed")
	}
	return err
}

func (c *PodManager) UpdatePodCPUQuota(pInfo *PodInfo, quota uint64, ignoreResourcePool bool) error {
	int64Quota := int64(quota)
	period := types.DefaultCPUPeriod

	err := pInfo.Cgroup.Control.Update(&cgroup2.Resources{
		CPU: &cgroup2.CPU{
			Max: cgroup2.NewCPUMax(&int64Quota, &period),
		},
	})
	if err != nil {
		klog.Error(err)
	}

	return err
}
