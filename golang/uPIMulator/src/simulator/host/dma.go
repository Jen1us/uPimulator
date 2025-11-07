package host

import (
	"uPIMulator/src/simulator/host/ramulator"
)

// DMATransferKind distinguishes Host<->Digital directions.
type DMATransferKind int

const (
	DMATransferHostToDigital DMATransferKind = iota
	DMATransferDigitalToHost
)

// DMAController tracks host DMA bandwidth usage and statistics.
type DMAController struct {
	bytesPerCycle      int64
	totalTransfers     int64
	totalBytes         int64
	totalHops          int64
	hostToDigitalBytes int64
	digitalToHostBytes int64
	ramulator          *ramulator.Client
}

// NewDMAController constructs a DMA controller with the provided throughput (bytes / cycle).
func NewDMAController(bytesPerCycle int64, ramClient *ramulator.Client) *DMAController {
	if bytesPerCycle <= 0 {
		bytesPerCycle = 8192
	}
	return &DMAController{
		bytesPerCycle: bytesPerCycle,
		ramulator:     ramClient,
	}
}

// BytesPerCycle returns the configured throughput.
func (d *DMAController) BytesPerCycle() int64 {
	if d == nil {
		return 0
	}
	return d.bytesPerCycle
}

// EstimateCycles computes the number of cycles required to transfer the requested bytes.
// Optional hop count can be provided to account for extra routing latency (added linearly).
func (d *DMAController) EstimateCycles(bytes int64, hops int, metadata map[string]interface{}) int {
	if bytes < 0 {
		bytes = 0
	}
	if d != nil && d.ramulator != nil && d.ramulator.Enabled() {
		if cycles, ok := d.ramulator.Estimate(bytes, metadata); ok && cycles > 0 {
			if hops > 0 {
				cycles += hops
			}
			return cycles
		}
	}
	bandwidth := d.BytesPerCycle()
	if bandwidth <= 0 {
		bandwidth = 8192
	}
	cycles := int((bytes + bandwidth - 1) / bandwidth)
	if cycles <= 0 {
		cycles = 1
	}
	if hops > 0 {
		cycles += hops
	}
	return cycles
}

// Record registers a completed DMA transfer for statistics.
func (d *DMAController) Record(kind DMATransferKind, bytes int64, hops int) {
	if d == nil {
		return
	}
	if bytes < 0 {
		bytes = 0
	}
	if hops < 0 {
		hops = 0
	}
	d.totalTransfers++
	d.totalBytes += bytes
	d.totalHops += int64(hops)
	switch kind {
	case DMATransferHostToDigital:
		d.hostToDigitalBytes += bytes
	case DMATransferDigitalToHost:
		d.digitalToHostBytes += bytes
	}
}

// Totals expose aggregate statistics for logging.
func (d *DMAController) Totals() (transfers int64, bytes int64, hops int64) {
	if d == nil {
		return 0, 0, 0
	}
	return d.totalTransfers, d.totalBytes, d.totalHops
}

func (d *DMAController) HostToDigitalBytes() int64 {
	if d == nil {
		return 0
	}
	return d.hostToDigitalBytes
}

func (d *DMAController) DigitalToHostBytes() int64 {
	if d == nil {
		return 0
	}
	return d.digitalToHostBytes
}
