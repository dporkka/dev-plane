package runtimes

const SandboxSchemaVersion = "platform.sandbox.v1"

type SandboxRuntime string

const (
	SandboxRuntimeAuto        SandboxRuntime = "auto"
	SandboxRuntimeFirecracker SandboxRuntime = "firecracker"
	SandboxRuntimeWasm        SandboxRuntime = "wasm"
	SandboxRuntimeUnikernel   SandboxRuntime = "unikernel"
)

type SandboxResources struct {
	CPUMillis          int `json:"cpu_millis"`
	MemoryMB           int `json:"memory_mb"`
	DiskMB             int `json:"disk_mb"`
	MaxDurationSeconds int `json:"max_duration_seconds"`
}

type SandboxNetworkMode string

const (
	SandboxNetworkNone         SandboxNetworkMode = "none"
	SandboxNetworkAllowlist    SandboxNetworkMode = "allowlist"
	SandboxNetworkUnrestricted SandboxNetworkMode = "unrestricted"
)

type SandboxNetwork struct {
	Mode  SandboxNetworkMode `json:"mode"`
	Allow []string           `json:"allow,omitempty"`
}

type SandboxCheckpointMode string

const (
	SandboxCheckpointNone     SandboxCheckpointMode = "none"
	SandboxCheckpointManual   SandboxCheckpointMode = "manual"
	SandboxCheckpointPeriodic SandboxCheckpointMode = "periodic"
)

type SandboxCheckpoint struct {
	Mode            SandboxCheckpointMode `json:"mode"`
	IntervalSeconds int                   `json:"interval_seconds,omitempty"`
}

type SandboxWorkload struct {
	Interactive       bool `json:"interactive,omitempty"`
	ArbitraryBinaries bool `json:"arbitrary_binaries,omitempty"`
	WASICompatible    bool `json:"wasi_compatible,omitempty"`
	ImmutableImage    bool `json:"immutable_image,omitempty"`
}

type SandboxSecretReference struct {
	Ref     string `json:"ref"`
	MountAs string `json:"mount_as,omitempty"`
	Name    string `json:"name,omitempty"`
}

type SandboxContext struct {
	TenantID             string `json:"tenant_id,omitempty"`
	ProjectID            string `json:"project_id,omitempty"`
	TaskID               string `json:"task_id,omitempty"`
	RunID                string `json:"run_id,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	TraceID              string `json:"trace_id,omitempty"`
	AgentVaultContextRef string `json:"agent_vault_context_ref,omitempty"`
}

type SandboxSpec struct {
	SchemaVersion string                   `json:"schema_version"`
	Runtime       SandboxRuntime           `json:"runtime"`
	Resources     SandboxResources         `json:"resources"`
	Network       SandboxNetwork           `json:"network"`
	Checkpoint    SandboxCheckpoint        `json:"checkpoint"`
	Capabilities  []string                 `json:"capabilities,omitempty"`
	Secrets       []SandboxSecretReference `json:"secrets,omitempty"`
	Workload      SandboxWorkload          `json:"workload,omitempty"`
	Context       SandboxContext           `json:"context,omitempty"`
}

func DefaultDevPlaneSandboxSpec() SandboxSpec {
	return SandboxSpec{
		SchemaVersion: SandboxSchemaVersion,
		Runtime:       SandboxRuntimeAuto,
		Resources: SandboxResources{
			CPUMillis:          1000,
			MemoryMB:           2048,
			DiskMB:             8192,
			MaxDurationSeconds: 1800,
		},
		Network: SandboxNetwork{
			Mode: SandboxNetworkNone,
		},
		Checkpoint: SandboxCheckpoint{
			Mode: SandboxCheckpointManual,
		},
		Workload: SandboxWorkload{
			Interactive:       true,
			ArbitraryBinaries: true,
		},
	}
}

func normalizeSandboxSpec(spec SandboxSpec) SandboxSpec {
	defaults := DefaultDevPlaneSandboxSpec()
	if spec.SchemaVersion == "" {
		spec.SchemaVersion = defaults.SchemaVersion
	}
	if spec.Runtime == "" {
		spec.Runtime = defaults.Runtime
	}
	if spec.Resources.CPUMillis <= 0 {
		spec.Resources.CPUMillis = defaults.Resources.CPUMillis
	}
	if spec.Resources.MemoryMB <= 0 {
		spec.Resources.MemoryMB = defaults.Resources.MemoryMB
	}
	if spec.Resources.DiskMB <= 0 {
		spec.Resources.DiskMB = defaults.Resources.DiskMB
	}
	if spec.Resources.MaxDurationSeconds <= 0 {
		spec.Resources.MaxDurationSeconds = defaults.Resources.MaxDurationSeconds
	}
	if spec.Network.Mode == "" {
		spec.Network.Mode = defaults.Network.Mode
	}
	if spec.Checkpoint.Mode == "" {
		spec.Checkpoint.Mode = defaults.Checkpoint.Mode
	}
	return spec
}
