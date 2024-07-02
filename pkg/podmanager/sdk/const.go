package types

type PodState string

func (ps PodState) String() string {
	return string(ps)
}

type PodCPUState string
type PodMemoryState string
type PodEndpointState string

const (
	CPU_UNKNOWN                    PodCPUState = "unknown"
	CPU_DYNAMIC_OVERPROVISION      PodCPUState = "dynamic-overprovision"
	CPU_DYNAMIC_RESOURCE_EFFICIENT PodCPUState = "dynamic-resource-efficient"
	CPU_MAX                        PodCPUState = "cpu-max"

	MEMORY_UNKNOWN PodMemoryState = "unknown"
	MEMORY_SWAPPED PodMemoryState = "mem-swapped"
	MEMORY_MAX     PodMemoryState = "mem-max"

	POD_READY_FULLSPEED_STATE PodState = "Ready-FullSpeed"
	POD_READY_RUNNING_STATE   PodState = "Ready-Running"
	POD_READY_CATNAP_STATE    PodState = "Ready-CatNap"
	POD_READY_LONGNAP_STATE   PodState = "Ready-LongNap"
	POD_INITIALIZING_STATE    PodState = "Initializing"
	POD_WARMINGUP_STATE       PodState = "WarmingUp"
	POD_UNKNOWN_STATE         PodState = "Unknown"

	ENDPOINT_UP      PodEndpointState = "Up"
	ENDPOINT_DOWN    PodEndpointState = "Down"
	ENDPOINT_UNKNOWN PodEndpointState = "Unknown"
)

type PodServiceType string

func (st PodServiceType) String() string {
	return string(st)
}

const (
	DefaultCPUPeriod   uint64  = 100000                  // 100000 us
	DefaultMaxCPULimit float64 = 3                       // 3 core
	DefaultMinCPULimit float64 = 0.1                     // 0.1 core
	DefaultMemoryHigh  int64   = 10 * 1024 * 1024 * 1024 // 10 GiB

	RR   = "Ready-Running"
	RCN  = "Ready-CatNap"
	RLN  = "Ready-LongNap"
	WU   = "WarmingUp"
	Init = "Initializing"
	RFS  = "Ready-FullSpeed"

	DefaultMaxHistoryLength = 20
	DefaultMinHistoryLength = 10

	ENABLED_LABEL             = "swiftkube.io/enabled"
	STATE_LABEL               = "swiftkube.io/state"
	ENDPOINT_LABEL            = "swiftkube.io/endpoint"
	SERVICE_TYPE_LABEL        = "swiftkube.io/service-type"
	CPUSET_LABEL              = "swiftkube.io/cpuset"
	CPU_THROTTLE_TARGET_LABEL = "swiftkube.io/throttle-target"

	SERVICE_TYPE_LC      PodServiceType = "lc-service"
	SERVICE_TYPE_BE      PodServiceType = "be-service"
	SERVICE_TYPE_UNKNOWN PodServiceType = "unknown-service"

	CPUSET_LC      = "lc-cpuset"
	CPUSET_BE      = "be-cpuset"
	CPUSET_MIX     = "mix-cpuset"
	CPUSET_UNKNOWN = "unknown-cpuset"
)
