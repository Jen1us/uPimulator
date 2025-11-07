package digital

import "math"

// PEArray models a systolic array used for matrix multiply workloads. The
// current abstraction keeps track of geometry and exposes helper utilities for
// estimating latency when scheduling tiles.
type PEArray struct {
	Rows           int
	Cols           int
	PipelineDepth  int
	OutputLatency  int
	UtilizedCycles int
}

// NewPEArray builds a PE array with the provided geometry. A small pipeline
// depth is assumed to model register stages inside the systolic datapath.
func NewPEArray(rows, cols int) PEArray {
	if rows <= 0 {
		rows = 1
	}
	if cols <= 0 {
		cols = 1
	}

	return PEArray{
		Rows:          rows,
		Cols:          cols,
		PipelineDepth: 8,
		OutputLatency: 4,
	}
}

// EstimateMatmulCycles computes a latency estimate for a tile of size m×n×k
// (m rows of activations, n columns of outputs, k accumulation depth). The
// model assumes a classic systolic array schedule where rows and columns flow
// through the array, leading to a ramp-up of size rows+cols and a steady-state
// proportional to k.
func (pe *PEArray) EstimateMatmulCycles(m, n, k int) int {
	if m <= 0 || n <= 0 || k <= 0 {
		return 1
	}

	tileRows := int(math.Ceil(float64(m) / float64(pe.Rows)))
	tileCols := int(math.Ceil(float64(n) / float64(pe.Cols)))
	steady := k + pe.PipelineDepth
	ramp := pe.Rows + pe.Cols - 2
	cycles := (steady + ramp + pe.OutputLatency) * tileRows * tileCols
	if cycles < 1 {
		cycles = 1
	}

	return cycles
}
