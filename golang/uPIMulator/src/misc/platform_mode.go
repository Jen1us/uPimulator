package misc

// PlatformMode defines the high-level execution platform that the simulator
// should instantiate. Additional modes can be added as new architectures are
// integrated.
type PlatformMode string

const (
	// PlatformModeUpmem represents the legacy UPMEM channel/rank/DPU topology.
	PlatformModeUpmem PlatformMode = "upmem"
	// PlatformModeChiplet represents the forthcoming heterogeneous chiplet topology.
	PlatformModeChiplet PlatformMode = "chiplet"
)

// DefaultPlatformMode returns the mode used when no explicit selection is made.
func DefaultPlatformMode() PlatformMode {
	return PlatformModeUpmem
}

// PlatformModeFromString converts an arbitrary string into a PlatformMode. When
// the provided value is unknown the bool return will be false.
func PlatformModeFromString(value string) (PlatformMode, bool) {
	switch value {
	case string(PlatformModeUpmem):
		return PlatformModeUpmem, true
	case string(PlatformModeChiplet):
		return PlatformModeChiplet, true
	default:
		return "", false
	}
}
