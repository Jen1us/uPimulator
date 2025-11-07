package digital

// VPUUnit models a dedicated vector processing unit that can execute wide SIMD
// instructions independently of the SPU clusters.
type VPUUnit struct {
	vectorLanes int
	issueWidth  int
	latency     int
}

// NewVPUUnit constructs a VPU model with the provided characteristics. Lanes
// are expressed as the number of FP16 values processed per cycle while the
// issue width captures how many micro-ops can be dispatched each cycle.
func NewVPUUnit(vectorLanes, issueWidth, latency int) VPUUnit {
	if vectorLanes <= 0 {
		vectorLanes = 128
	}
	if issueWidth <= 0 {
		issueWidth = 2
	}
	if latency <= 0 {
		latency = 4
	}
	return VPUUnit{
		vectorLanes: vectorLanes,
		issueWidth:  issueWidth,
		latency:     latency,
	}
}

// VectorThroughput returns the number of FP16 values processed per cycle.
func (unit VPUUnit) VectorThroughput() int {
	if unit.vectorLanes <= 0 {
		return 1
	}
	return unit.vectorLanes
}

// IssueWidth reports the number of micro-ops the unit can sustain in flight.
func (unit VPUUnit) IssueWidth() int {
	if unit.issueWidth <= 0 {
		return 1
	}
	return unit.issueWidth
}

// LatencyCycles exposes the pipeline latency for dependent operations.
func (unit VPUUnit) LatencyCycles() int {
	if unit.latency <= 0 {
		return 1
	}
	return unit.latency
}
