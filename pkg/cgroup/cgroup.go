package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

var containerGroupCgroups map[string]*Cgroup = make(map[string]*Cgroup, 0)
var containerGroupCgroupsLock sync.Mutex

func getContainerID(id string) (string, error) {
	if len(id) == 0 {
		return "", fmt.Errorf("container ID is empty")
	}

	split := strings.Split(id, "//")
	if len(split) == 2 {
		return split[1], nil
	} else {
		return "", fmt.Errorf("wrong container ID format: %s", id)
	}
}

func loadCgroup(path string) (*cgroup2.Manager, string, error) {
	var control *cgroup2.Manager = nil
	var err error
	if cgroups.Mode() == cgroups.Unified {
		control, err = cgroup2.Load(path, cgroup2.WithMountpoint("/sys/fs/cgroup"))
		if err != nil {
			return nil, "", err
		}
	} else {
		return nil, "", fmt.Errorf("only support cgroup v2")
	}

	return control, filepath.Join("/sys/fs/cgroup", path), nil
}

func cgroupPath(qosClass corev1.PodQOSClass, group string, uid string, cid string) (string, error) {
	var qos string = ""
	var path string = ""

	if qosClass == corev1.PodQOSBurstable {
		qos = "-burstable"
	} else if qosClass == corev1.PodQOSBestEffort {
		qos = "-besteffort"
	}

	if qos != "" {
		path = filepath.Join("/kubepods.slice", fmt.Sprintf("kubepods%s.slice", qos))
	} else {
		path = "/kubepods.slice"
	}

	if group != "" {
		group = "-" + strings.ReplaceAll(group, "-", "_")
	}

	if group != "" {
		if qos != "" {
			path = filepath.Join(
				path,
				fmt.Sprintf("kubepods%s%s.slice", qos, group),
			)
		} else {
			path = filepath.Join(
				path,
				fmt.Sprintf("kubepods%s.slice", group),
			)
		}
	}

	if uid != "" {
		uid = strings.ReplaceAll(uid, "-", "_")
		if group != "" {
			if qos != "" {
				path = filepath.Join(
					path,
					fmt.Sprintf("kubepods%s%s-pod%s.slice", qos, group, uid),
				)
			} else {
				path = filepath.Join(
					path,
					fmt.Sprintf("kubepods%s-pod%s.slice", group, uid),
				)
			}
		} else {
			if qos != "" {
				path = filepath.Join(
					path,
					fmt.Sprintf("kubepods%s-pod%s.slice", qos, uid),
				)
			} else {
				path = filepath.Join(
					path,
					fmt.Sprintf("kubepods-pod%s.slice", uid),
				)
			}
		}
	}

	if cid != "" {
		// TODO 目前只支持containerd作为容器运行时
		path = filepath.Join(
			path,
			fmt.Sprintf("cri-containerd-%s.scope", cid),
		)
	}

	return path, nil
}

func GetContainerGroupCgroup(group string) *Cgroup {
	return containerGroupCgroups[group]
}

func loadContainerGroupCgroup(groupName string, qosClass corev1.PodQOSClass) error {
	containerGroupCgroupsLock.Lock()
	defer containerGroupCgroupsLock.Unlock()

	_, ok := containerGroupCgroups[groupName]
	if !ok {
		path, err := cgroupPath(qosClass, groupName, "", "")
		if err != nil {
			return err
		}
		control, path, err := loadCgroup(path)
		if err != nil {
			return err
		}
		containerGroupCgroups[groupName] = &Cgroup{
			path:    path,
			Control: control,
		}
	}

	return nil
}

func loadBurstableCgroup() (*cgroup2.Manager, string, error) {
	path := "/kubepods.slice/kubepods-burstable.slice"
	return loadCgroup(path)
}

func loadBesteffortCgroup() (*cgroup2.Manager, string, error) {
	path := "/kubepods.slice/kubepods-besteffort.slice"
	return loadCgroup(path)
}

func loadPodCgroup(uid types.UID, qosClass corev1.PodQOSClass, group string) (*cgroup2.Manager, string, error) {
	if group != "" {
		err := loadContainerGroupCgroup(group, qosClass)
		if err != nil {
			return nil, "", err
		}
	}
	path, err := cgroupPath(qosClass, group, string(uid), "")
	if err != nil {
		return nil, "", err
	}
	return loadCgroup(path)
}

func loadContainerCgroup(uid types.UID, qosClass corev1.PodQOSClass, containerId string, group string) (*cgroup2.Manager, string, error) {
	path, err := cgroupPath(qosClass, group, string(uid), containerId)
	if err != nil {
		return nil, "", err
	}
	return loadCgroup(path)
}

type Cgroup struct {
	path    string
	Control *cgroup2.Manager
}

func (cg *Cgroup) GetCPUQuotaAndPeriod() (int64, uint64, error) {
	bytes, err := os.ReadFile(filepath.Join(cg.path, "cpu.max"))
	if err != nil {
		return 0, 0, err
	}
	v := string(bytes)
	v = strings.Trim(v, "\n")
	vs := strings.Split(v, " ")
	if len(vs) != 2 {
		return 0, 0, fmt.Errorf("parse cpu.max failed")
	}
	var quota int64
	if vs[0] == "max" {
		quota = -1
	} else {
		quota, err = strconv.ParseInt(vs[0], 10, 64)
		if err != nil {
			return 0, 0, err
		}
	}
	period, err := strconv.ParseUint(vs[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return quota, period, nil
}

func LoadBesteffortCgroup() (*Cgroup, error) {
	control, path, err := loadBesteffortCgroup()
	if err != nil {
		return nil, err
	}

	cg := &Cgroup{
		path:    path,
		Control: control,
	}
	return cg, nil
}

func LoadBurstableCgroup() (*Cgroup, error) {
	control, path, err := loadBurstableCgroup()
	if err != nil {
		return nil, err
	}

	cg := &Cgroup{
		path:    path,
		Control: control,
	}
	return cg, nil
}

func LoadContainerCgroup(pod *corev1.Pod, con *corev1.ContainerStatus) (*Cgroup, error) {
	containerId, err := getContainerID(con.ContainerID)
	if err != nil {
		return nil, err
	}
	podGroup, ok := pod.GetLabels()["genesis.io/container-group"]
	if !ok {
		podGroup = ""
	}
	control, path, err := loadContainerCgroup(pod.GetUID(), pod.Status.QOSClass, containerId, podGroup)
	if err != nil {
		return nil, err
	}

	cg := &Cgroup{
		path:    path,
		Control: control,
	}

	return cg, nil
}

func LoadPodCgroup(pod *corev1.Pod) (*Cgroup, error) {
	group, ok := pod.GetLabels()["genesis.io/container-group"]
	if !ok {
		group = ""
	}

	control, path, err := loadPodCgroup(pod.GetUID(), pod.Status.QOSClass, group)
	if err != nil {
		return nil, err
	}

	cg := &Cgroup{
		path:    path,
		Control: control,
	}

	return cg, nil
}
