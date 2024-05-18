package types

type PodState = string
type CPUState = string
type MemoryState = string
type EndpointState = string

const (
	CPU_UNKNOWN                    CPUState = "unknown"
	CPU_DYNAMIC_OVERPROVISION      CPUState = "dynamic-overprovision"
	CPU_DYNAMIC_RESOURCE_EFFICIENT CPUState = "dynamic-resource-efficient"
	CPU_MAX                        CPUState = "cpu-max"

	MEMORY_UNKNOWN MemoryState = "unknown"
	MEMORY_SWAPPED MemoryState = "mem-swapped"
	MEMORY_MAX     MemoryState = "mem-max"

	POD_READY_FULLSPEED PodState = "Ready-FullSpeed"
	POD_READY_RUNNING   PodState = "Ready-Running"
	POD_READY_CATNAP    PodState = "Ready-CatNap"
	POD_READY_LONGNAP   PodState = "Ready-LongNap"
	POD_INITIALIZING    PodState = "Initializing"
	POD_WARMINGUP       PodState = "WarmingUp"

	ENDPOINT_UP   EndpointState = "Up"
	ENDPOINT_DOWN EndpointState = "Down"
)

const (
	DefaultMaxCPUQuota = float64(3)
	DefaultMinCPUQuota = float64(0.1)

	RR   = "Ready-Running"
	RCN  = "Ready-CatNap"
	RLN  = "Ready-LongNap"
	WU   = "WarmingUp"
	Init = "Initializing"
	RFS  = "Ready-FullSpeed"

	DefaultMaxHistoryLength = 20
	DefaultMinHistoryLength = 10

	STATE_LABEL    = "swiftkube.io/state"
	ENDPOINT_LABEL = "swiftkube.io/endpoint"
)
