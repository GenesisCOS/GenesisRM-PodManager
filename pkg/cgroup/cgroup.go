package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	cgroupPath                    = "/sys/fs/cgroup"
	bestEffortPodCgroupPath       = "/kubepods.slice/kubepods-besteffort.slice"
	burstablePodCgroupPath        = "/kubepods.slice/kubepods-burstable.slice"
	bestEffortPodCgroupPathPrefix = "/kubepods-besteffort-pod"
	burstablePodCgroupPathPrefix  = "/kubepods-burstable-pod"
	podCgroupPathSuffix           = ".slice"
)

type CgCPUStat struct {
	NrPeriods     int64
	NrThrottled   int64
	NrBursts      int64
	ThrottledTime int64
	BurstTime     int64
}

type CgMemoryStat struct {
	Cache int64
	RSS   int64
	Swap  int64
}

func (d *CgMemoryStat) Add(other *CgMemoryStat) {
	d.RSS += other.RSS
	d.Cache += other.Cache
	d.Swap += other.Swap
}

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

func checkCgFile(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file %s not exists", path)
		}
	}
	return err
}

func loadPodCgroups(podUid types.UID, qosClass corev1.PodQOSClass) (*cgroup2.Manager, string, error) {
	uid := strings.ReplaceAll(string(podUid), "-", "_")
	var qos string

	if qosClass == corev1.PodQOSBurstable {
		qos = "burstable"
	} else if qosClass == corev1.PodQOSBestEffort {
		qos = "besteffort"
	} else {
		return nil, "", fmt.Errorf("invalid QOS class %s", string(qosClass))
	}

	path := fmt.Sprintf("/kubepods.slice/kubepods-%s.slice/kubepods-%s-pod%s.slice",
		qos, qos, uid)

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

func loadContainerCgroups(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (*cgroup2.Manager, string, error) {
	uid := strings.ReplaceAll(string(podUid), "-", "_")
	var qos string

	if qosClass == corev1.PodQOSBurstable {
		qos = "burstable"
	} else if qosClass == corev1.PodQOSBestEffort {
		qos = "besteffort"
	} else {
		return nil, "", fmt.Errorf("invalid QOS class %s", string(qosClass))
	}

	path := fmt.Sprintf("/kubepods.slice/kubepods-%s.slice/kubepods-%s-pod%s.slice/%s",
		qos, qos, uid, fmt.Sprintf("/cri-containerd-%s.scope", containerId))

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

	return control, filepath.Join("/sys/fs/cgroup", path), err
}

func getPodCgFilePath(podUid types.UID, qosClass corev1.PodQOSClass, file string) (string, error) {
	uid := strings.ReplaceAll(string(podUid), "-", "_")
	var qos string

	if qosClass == corev1.PodQOSBurstable {
		qos = "burstable"
	} else if qosClass == corev1.PodQOSBestEffort {
		qos = "besteffort"
	} else {
		return "", fmt.Errorf("invalid QOS class %s", string(qosClass))
	}

	path := fmt.Sprintf("/sys/fs/cgroup/kubepods.slice/kubepods-%s.slice/kubepods-%s-pod%s.slice/%s",
		qos, qos, uid, file)

	return path, nil
}

func getContainerCgFilePath(podUid types.UID, qosClass corev1.PodQOSClass, family string, containerId string, file string) (string, error) {
	path := ""
	uid := strings.ReplaceAll(string(podUid), "-", "_")

	if qosClass == corev1.PodQOSBurstable {
		path += cgroupPath + fmt.Sprintf("/%s", family) + burstablePodCgroupPath + burstablePodCgroupPathPrefix + uid + podCgroupPathSuffix + fmt.Sprintf("/cri-containerd-%s.scope/", containerId) + file
	} else if qosClass == corev1.PodQOSBestEffort {
		path += cgroupPath + fmt.Sprintf("/%s", family) + bestEffortPodCgroupPath + bestEffortPodCgroupPathPrefix + uid + podCgroupPathSuffix + fmt.Sprintf("/cri-containerd-%s.scope/", containerId) + file
	} else {
		return "", fmt.Errorf("invalid QOS class %s", string(qosClass))
	}

	err := checkCgFile(path)
	if err != nil {
		return "", err
	}

	return path, nil
}

func cgReadInt64(file string) (int64, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(string(data[:len(data)-1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func cgReadUInt64(file string) (uint64, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(string(data[:len(data)-1]), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func cgWriteInt64(file string, value int64) error {
	return cgWriteString(file, fmt.Sprintf("%d", value))
}

func cgWriteString(file string, value string) error {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	_, err = f.WriteString(value)
	f.Sync()
	return err
}

type Cgroup struct {
	path       string
	Containerd *cgroup2.Manager
}

func (cg *Cgroup) GetCPUQuotaAndPeriod() (int64, uint64, error) {
	bytes, err := os.ReadFile(filepath.Join(cg.path, "cpu.max"))
	if err != nil {
		return 0, 0, err
	}
	v := string(bytes)
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

func LoadContainerCgroup(pod *corev1.Pod, con *corev1.ContainerStatus) (*Cgroup, error) {
	containerId, err := getContainerID(con.ContainerID)
	if err != nil {
		return nil, err
	}
	containerdCgroups, path, err := loadContainerCgroups(pod.GetUID(), pod.Status.QOSClass, containerId)
	if err != nil {
		return nil, err
	}

	cg := &Cgroup{
		path:       path,
		Containerd: containerdCgroups,
	}

	return cg, nil
}

func LoadPodCgroup(pod *corev1.Pod) (*Cgroup, error) {
	containerdCgroups, path, err := loadPodCgroups(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		return nil, err
	}

	cg := &Cgroup{
		path:       path,
		Containerd: containerdCgroups,
	}

	return cg, nil
}
