package rram

// Parameters captures device-level characteristics for the RRAM chiplet.
type Parameters struct {
	ClockMHz                    int
	Voltage                     float64
	StaticPowerMw               float64
	BaseAreaMm2                 float64
	Tile                        TileParameters
	PulseEnergyPJ               float64
	DacEnergyPJ                 float64
	AdcEnergyPJ                 float64
	PreprocessEnergyPJPerCycle  float64
	PostprocessEnergyPJPerCycle float64
	InputReadEnergyPJPerByte    float64
	OutputWriteEnergyPJPerByte  float64
	WeightReadEnergyPJPerByte   float64
	WeightLoadBytesPerCycle     int64
	IdleLeakEnergyPJPerCycle    float64
	WeightControllerEnergyPJ    float64
}

// TileParameters describes the geometry/properties of a single tile.
type TileParameters struct {
	SenseArrayAreaMm2 float64
	ControllerAreaMm2 float64
	LeakagePowerMw    float64
}

// DefaultParameters provides a coarse model aligned with 2-bit RRAM CIM
// demonstrators (e.g., 128x128 cells, 12-bit ADC).
func DefaultParameters() Parameters {
	return Parameters{
		ClockMHz:      800,
		Voltage:       0.6,
		StaticPowerMw: 30.0,
		BaseAreaMm2:   6.0,
		Tile: TileParameters{
			SenseArrayAreaMm2: 0.045,
			ControllerAreaMm2: 0.12,
			LeakagePowerMw:    3.5,
		},
		PulseEnergyPJ:               2.5,  // per bitline pulse across array
		DacEnergyPJ:                 0.35, // per DAC activation
		AdcEnergyPJ:                 5.2,  // per ADC conversion (12-bit)
		PreprocessEnergyPJPerCycle:  1.1,
		PostprocessEnergyPJPerCycle: 1.8,
		InputReadEnergyPJPerByte:    0.45,
		OutputWriteEnergyPJPerByte:  0.52,
		WeightReadEnergyPJPerByte:   0.38,
		WeightLoadBytesPerCycle:     4096,
	}
}
