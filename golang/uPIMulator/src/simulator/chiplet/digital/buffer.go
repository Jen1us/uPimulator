package digital

import "math"

// Buffer represents an on-chip SRAM region with finite capacity and bandwidth.
// The current model is deliberately lightweight: capacity tracking ensures that
// workloads respect storage limits, while a simple bandwidth metric allows the
// scheduler to derive coarse latency estimates for load/store phases.
type Buffer struct {
	Name      string
	capacity  int64
	bandwidth int64
	occupancy int64
}

// NewBuffer constructs a buffer with the provided capacity (bytes) and
// bandwidth (bytes per cycle). A zero or negative bandwidth defaults to 1
// byte/cycle to avoid divide-by-zero when estimating latency.
func NewBuffer(name string, capacity int64, bandwidth int64) *Buffer {
	if bandwidth <= 0 {
		bandwidth = 1
	}
	if capacity < 0 {
		capacity = 0
	}

	return &Buffer{
		Name:      name,
		capacity:  capacity,
		bandwidth: bandwidth,
		occupancy: 0,
	}
}

// Capacity returns the buffer capacity in bytes.
func (b *Buffer) Capacity() int64 {
	return b.capacity
}

// Occupancy returns the current committed bytes.
func (b *Buffer) Occupancy() int64 {
	return b.occupancy
}

// Bandwidth returns the nominal per-cycle throughput.
func (b *Buffer) Bandwidth() int64 {
	return b.bandwidth
}

// CanHold checks whether the buffer can accommodate the requested bytes given
// the current occupancy.
func (b *Buffer) CanHold(bytes int64) bool {
	if bytes < 0 {
		return true
	}
	return b.occupancy+bytes <= b.capacity
}

// Reserve increments the occupancy by the requested amount when capacity
// permits. Returns false if the reservation would exceed the capacity.
func (b *Buffer) Reserve(bytes int64) bool {
	if bytes < 0 {
		return false
	}
	if !b.CanHold(bytes) {
		return false
	}
	b.occupancy += bytes
	return true
}

// Release decrements occupancy, clamping at zero.
func (b *Buffer) Release(bytes int64) {
	if bytes <= 0 {
		return
	}
	b.occupancy -= bytes
	if b.occupancy < 0 {
		b.occupancy = 0
	}
}

// ApplyDelta adjusts occupancy by delta while enforcing bounds. Negative deltas
// correspond to releases. Returns true if the new occupancy is valid.
func (b *Buffer) ApplyDelta(delta int64) bool {
	next := b.occupancy + delta
	if next < 0 {
		return false
	}
	if next > b.capacity {
		return false
	}
	b.occupancy = next
	return true
}

// TransferCycles returns ceil(bytes/bandwidth). Zero-byte transfers report one
// cycle to keep the scheduling model conservative.
func (b *Buffer) TransferCycles(bytes int64) int {
	if bytes <= 0 {
		return 1
	}
	cycles := int(math.Ceil(float64(bytes) / float64(b.bandwidth)))
	if cycles < 1 {
		return 1
	}
	return cycles
}
