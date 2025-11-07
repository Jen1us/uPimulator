package digital

import "math"

// SPUCluster represents a scalar/vector processing cluster. Each cluster
// exposes a nominal throughput for scalar integer, scalar floating-point and
// vector operations. The model is intentionally coarse yet captures relative
// performance differences across workloads.
type SPUCluster struct {
	NumIntegerALUs int
	NumFloatFPUs   int
	VectorWidth    int
	HasSpecialUnit bool
	issueWidth     int
}

// NewSPUCluster builds a cluster with reasonable default issue width derived
// from operand availability.
func NewSPUCluster(intALUs, floatFPUs, vectorWidth int, hasSpecialUnit bool) SPUCluster {
	if intALUs <= 0 {
		intALUs = 1
	}
	if floatFPUs <= 0 {
		floatFPUs = 1
	}
	if vectorWidth <= 0 {
		vectorWidth = 64
	}

	issue := intALUs + floatFPUs
	if hasSpecialUnit {
		issue++
	}

	return SPUCluster{
		NumIntegerALUs: intALUs,
		NumFloatFPUs:   floatFPUs,
		VectorWidth:    vectorWidth,
		HasSpecialUnit: hasSpecialUnit,
		issueWidth:     issue,
	}
}

// EstimateMicroOpCycles returns the cycles required to execute the provided
// scalar and vector micro-ops across the cluster.
func (spu *SPUCluster) EstimateMicroOpCycles(intOps, floatOps, vectorOps int) int {
	if spu.issueWidth <= 0 {
		return int(math.Max(float64(intOps+floatOps+vectorOps), 1.0))
	}

	scalarOps := intOps + floatOps
	scalarCycles := int(math.Ceil(float64(scalarOps) / float64(spu.issueWidth)))

	vectorCycles := int(math.Ceil(float64(vectorOps) / float64(spu.VectorWidth)))
	if vectorCycles < 0 {
		vectorCycles = 0
	}

	cycles := scalarCycles
	if vectorCycles > cycles {
		cycles = vectorCycles
	}
	if cycles < 1 {
		cycles = 1
	}
	return cycles
}

// ScalarThroughput returns the number of scalar operations this cluster can
// retire per cycle.
func (spu *SPUCluster) ScalarThroughput() int {
	throughput := spu.NumIntegerALUs + spu.NumFloatFPUs
	if throughput <= 0 {
		throughput = 1
	}
	return throughput
}

// VectorThroughput approximates the number of vector lanes processed in a
// single cycle. FP16 workloads map two values per 32-bit lane by default.
func (spu *SPUCluster) VectorThroughput() int {
	width := spu.VectorWidth / 16
	if width <= 0 {
		width = 1
	}
	return width
}

// SpecialLatency models the latency of transcendental/function-unit
// operations within the cluster.
func (spu *SPUCluster) SpecialLatency() int {
	if spu.HasSpecialUnit {
		return 12
	}
	return 24
}
