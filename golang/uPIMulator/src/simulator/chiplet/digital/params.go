package digital

// Parameters captures the technology-specific knobs for the digital chiplet.
// Values are expressed using SI-friendly units (MHz for clocks, pJ for energy,
// mm^2 for area) to keep accounting consistent across the simulator.
type Parameters struct {
	ClockMHz          int
	Voltage           float64
	StaticPowerMw     float64
	BaseAreaMm2       float64
	PeArray           PEArrayParameters
	Spu               SPUParameters
	Vpu               VPUParameters
	Buffer            BufferParameters
	Interconnect      InterconnectParameters
	LeakageOverheadMw float64
}

// PEArrayParameters models the systolic array MAC engines.
type PEArrayParameters struct {
	MacEnergyPJ                 float64
	MacAreaMm2                  float64
	LoadBandwidthBytesPerCycle  int64
	StoreBandwidthBytesPerCycle int64
	ActivationReadPJPerByte     float64
	WeightReadPJPerByte         float64
	OutputWritePJPerByte        float64
}

// SPUParameters models scalar/vector processing energy and throughput.
type SPUParameters struct {
	ScalarEnergyPJ       float64
	VectorEnergyPJ       float64
	SpecialEnergyPJ      float64
	ScalarThroughput     int
	VectorThroughput     int
	SpecialLatencyCycles int
	ClusterAreaMm2       float64
}

// VPUParameters models dedicated vector processing units that can operate in
// parallel with the SPU clusters.
type VPUParameters struct {
	UnitsPerCluster int
	VectorLanes     int
	IssueWidth      int
	LatencyCycles   int
	VectorEnergyPJ  float64
	UnitAreaMm2     float64
}

// BufferParameters describes on-chip SRAM buffers.
type BufferParameters struct {
	ActivationBytes      int64
	ScratchBytes         int64
	ReadEnergyPJPerByte  float64
	WriteEnergyPJPerByte float64
	AreaMm2              float64
	LeakagePowerMw       float64
}

// InterconnectParameters captures the cost of moving data to/from the host or
// other chiplets.
type InterconnectParameters struct {
	BytesPerCycle   int64
	EnergyPJPerByte float64
}

// DefaultParameters returns a conservative technology model derived from
// published PIM prototypes (e.g., 7nm-class digital ASIC).
func DefaultParameters() Parameters {
	return Parameters{
		ClockMHz:      1000,
		Voltage:       0.7,
		StaticPowerMw: 45.0,
		BaseAreaMm2:   8.5,
		PeArray: PEArrayParameters{
			MacEnergyPJ:                 0.55,
			MacAreaMm2:                  0.00012,
			LoadBandwidthBytesPerCycle:  2048,
			StoreBandwidthBytesPerCycle: 4096,
			ActivationReadPJPerByte:     0.35,
			WeightReadPJPerByte:         0.42,
			OutputWritePJPerByte:        0.48,
		},
		Spu: SPUParameters{
			ScalarEnergyPJ:       1.2,
			VectorEnergyPJ:       2.4,
			SpecialEnergyPJ:      6.5,
			ScalarThroughput:     8,
			VectorThroughput:     32,
			SpecialLatencyCycles: 12,
			ClusterAreaMm2:       0.08,
		},
		Vpu: VPUParameters{
			UnitsPerCluster: 2,
			VectorLanes:     256,
			IssueWidth:      4,
			LatencyCycles:   4,
			VectorEnergyPJ:  2.1,
			UnitAreaMm2:     0.06,
		},
		Buffer: BufferParameters{
			ActivationBytes:      8 * 1024 * 1024,
			ScratchBytes:         8 * 1024 * 1024,
			ReadEnergyPJPerByte:  0.28,
			WriteEnergyPJPerByte: 0.31,
			AreaMm2:              3.2,
			LeakagePowerMw:       12.0,
		},
		Interconnect: InterconnectParameters{
			BytesPerCycle:   1024,
			EnergyPJPerByte: 0.6,
		},
		LeakageOverheadMw: 8.0,
	}
}
