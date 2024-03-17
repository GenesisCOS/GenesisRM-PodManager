package cgroup

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

const (
	cgroupPath                    = "/sys/fs/cgroup"
	bestEffortPodCgroupPath       = "/kubepods.slice/kubepods-besteffort.slice"
	burstablePodCgroupPath        = "/kubepods.slice/kubepods-burstable.slice"
	bestEffortPodCgroupPathPrefix = "/kubepods-besteffort-pod"
	burstablePodCgroupPathPrefix  = "/kubepods-burstable-pod"
	podCgroupPathSuffix           = ".slice"

	cpuacctUsageFile       = "cpuacct.usage"
	cfsPeriodUsFile        = "cpu.cfs_period_us"
	cfsQuotaUsFile         = "cpu.cfs_quota_us"
	memoryUsageInBytesFile = "memory.usage_in_bytes"
	memoryLimitInBytesFile = "memory.limit_in_bytes"
	memoryStatFile         = "memory.stat"
	memoryHighFile         = "memory.high"
	tasksFile              = "tasks"

	cpuFamily    = "cpu"
	memoryFamily = "memory"
)

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
			// LOG
		}
	}
	return err
}

func getPodCgFilePath(podUid types.UID, qosClass corev1.PodQOSClass, family string, file string) (string, error) {
	path := ""
	uid := strings.ReplaceAll(string(podUid), "-", "_")

	if qosClass == corev1.PodQOSBurstable {
		path += cgroupPath + fmt.Sprintf("/%s", family) + burstablePodCgroupPath + burstablePodCgroupPathPrefix + uid + podCgroupPathSuffix + "/" + file
	} else if qosClass == corev1.PodQOSBestEffort {
		path += cgroupPath + fmt.Sprintf("/%s", family) + bestEffortPodCgroupPath + bestEffortPodCgroupPathPrefix + uid + podCgroupPathSuffix + "/" + file
	} else {
		return "", fmt.Errorf("invalid QOS class %s", string(qosClass))
	}

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

func getPodCpuacctUsageFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgFilePath(podUid, qosClass, cpuFamily, cpuacctUsageFile)
}

func getPodCfsPeriodUsFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgFilePath(podUid, qosClass, cpuFamily, cfsPeriodUsFile)
}

func getPodCfsQuotaUsFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgFilePath(podUid, qosClass, cpuFamily, cfsQuotaUsFile)
}

func getPodMemoryStatFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgFilePath(podUid, qosClass, memoryFamily, memoryStatFile)
}

func getPodMemoryUsageInBytesFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgFilePath(podUid, qosClass, memoryFamily, memoryUsageInBytesFile)
}

func getPodMemoryHighFilePath(podUid types.UID, qosClass corev1.PodQOSClass) (string, error) {
	return getPodCgFilePath(podUid, qosClass, memoryFamily, memoryHighFile)
}

func getContainerMemoryHighFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgFilePath(podUid, qosClass, memoryFamily, containerId, memoryHighFile)
}

func getContainerTasksFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgFilePath(podUid, qosClass, cpuFamily, containerId, tasksFile)
}

func getContainerCfsPeriodUsFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgFilePath(podUid, qosClass, cpuFamily, containerId, cfsPeriodUsFile)
}

func getContainerCfsQuotaUsFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgFilePath(podUid, qosClass, cpuFamily, containerId, cfsQuotaUsFile)
}

func getContainerMemoryLimitInBytesFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgFilePath(podUid, qosClass, memoryFamily, containerId, memoryLimitInBytesFile)
}

func getContainerMemoryStatFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgFilePath(podUid, qosClass, memoryFamily, containerId, memoryStatFile)
}

func getContainerCpuacctUsageFilePath(podUid types.UID, qosClass corev1.PodQOSClass, containerId string) (string, error) {
	return getContainerCgFilePath(podUid, qosClass, cpuFamily, containerId, cpuacctUsageFile)
}

func cgReadMemoryStat(filePath string) (*CgMemoryStat, error) {
	memoryStatFileData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var rss int64 = 0
	var cache int64 = 0
	var swap int64 = 0

	lines := strings.Split(string(memoryStatFileData), "\n")
	for _, line := range lines {

		line = strings.Trim(line, "\n")
		words := strings.Fields(line)
		if len(words) != 2 {
			continue
		}

		item := words[0]
		value := words[1]

		if item == "rss" {
			rss, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				klog.ErrorS(err, "parse int64 failed.", "memory.stat", "rss")
			}
		} else if item == "cache" {
			cache, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				klog.ErrorS(err, "parse int64 failed.", "memory.stat", "cache")
			}
		} else if item == "swap" {
			swap, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				klog.ErrorS(err, "parse int64 failed.", "memory.stat", "swap")
			}
		}
	}

	return &CgMemoryStat{
		RSS:   rss,
		Cache: cache,
		Swap:  swap,
	}, nil
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
	cpuacctUsageFilePath string
	cfsPeriodUsFilePath  string
	cfsQuotaUsFilePath   string

	memoryLimitInBytesFilePath string
	memoryUsageInBytesFilePath string
	memoryStatFilePath         string
	memoryHighFilePath         string
}

func (cg *Cgroup) GetCPUAcctUsage() (int64, error) {
	return cgReadInt64(cg.cpuacctUsageFilePath)
}

func (cg *Cgroup) GetCFSPeriod() (int64, error) {
	return cgReadInt64(cg.cfsPeriodUsFilePath)
}

func (cg *Cgroup) GetCFSQuota() (int64, error) {
	return cgReadInt64(cg.cfsQuotaUsFilePath)
}

func (cg *Cgroup) SetCFSQuota(v int64) error {
	return cgWriteInt64(cg.cfsQuotaUsFilePath, v)
}

func (cg *Cgroup) GetMemoryUsageInBytes() (int64, error) {
	return cgReadInt64(cg.memoryUsageInBytesFilePath)
}

func (cg *Cgroup) GetMemoryLimitInBytes() (int64, error) {
	return cgReadInt64(cg.memoryLimitInBytesFilePath)
}

func (cg *Cgroup) GetMemoryStat() (*CgMemoryStat, error) {
	return cgReadMemoryStat(cg.memoryStatFilePath)
}

func NewContainerCg(pod *corev1.Pod, con *corev1.ContainerStatus) (*Cgroup, error) {
	containerId, err := getContainerID(con.ContainerID)
	if err != nil {
		return nil, err
	}
	// container cpu.cfs_period_us
	cpuCfsPeriodFilePath, err := getContainerCfsPeriodUsFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
	if err != nil {
		return nil, err
	}

	// container cpu.cfs_quota_us
	cpuCfsQuotaFilePath, err := getContainerCfsQuotaUsFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
	if err != nil {
		return nil, err
	}

	// container memory.limit_in_bytes
	memoryLimitInBytesFilePath, err := getContainerMemoryLimitInBytesFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
	if err != nil {
		return nil, err
	}

	// container memory.stat
	memoryStatFilePath, err := getContainerMemoryStatFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
	if err != nil {
		return nil, err
	}

	// container memory.high
	memoryHighFilePath, err := getContainerMemoryHighFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
	if err != nil {
		return nil, err
	}

	// container cpuacct.usage
	cpuacctUsageFilePath, err := getContainerCpuacctUsageFilePath(pod.GetUID(), pod.Status.QOSClass, containerId)
	if err != nil {
		return nil, err
	}

	cg := &Cgroup{
		cpuacctUsageFilePath: cpuacctUsageFilePath,
		cfsPeriodUsFilePath:  cpuCfsPeriodFilePath,
		cfsQuotaUsFilePath:   cpuCfsQuotaFilePath,

		memoryUsageInBytesFilePath: "",
		memoryLimitInBytesFilePath: memoryLimitInBytesFilePath,
		memoryStatFilePath:         memoryStatFilePath,
		memoryHighFilePath:         memoryHighFilePath,
	}

	return cg, nil
}

func NewPodCg(pod *corev1.Pod) (*Cgroup, error) {
	// cpuacct.usage
	podCPUacctUsageFilePath, err := getPodCpuacctUsageFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		return nil, err
	}

	// memory.usage_in_bytes
	podMemoryUsageInBytesFilePath, err := getPodMemoryUsageInBytesFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		return nil, err
	}

	// memory.usage_in_bytes
	podMemoryHighFilePath, err := getPodMemoryHighFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		return nil, err
	}

	// cpu.cfs_period_us
	podCPUCfsPeriodFilePath, err := getPodCfsPeriodUsFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		return nil, err
	}

	// cpu.cfs_quota_us
	podCPUCfsQuotaFilePath, err := getPodCfsQuotaUsFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		return nil, err
	}

	// memory.stat
	podMemoryStatFilePath, err := getPodMemoryStatFilePath(pod.GetUID(), pod.Status.QOSClass)
	if err != nil {
		return nil, err
	}

	cg := &Cgroup{
		cpuacctUsageFilePath: podCPUacctUsageFilePath,
		cfsPeriodUsFilePath:  podCPUCfsPeriodFilePath,
		cfsQuotaUsFilePath:   podCPUCfsQuotaFilePath,

		memoryLimitInBytesFilePath: "",
		memoryUsageInBytesFilePath: podMemoryUsageInBytesFilePath,
		memoryStatFilePath:         podMemoryStatFilePath,
		memoryHighFilePath:         podMemoryHighFilePath,
	}

	return cg, nil
}
