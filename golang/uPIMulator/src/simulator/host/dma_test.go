package host

import (
	"testing"

	"uPIMulator/src/simulator/host/ramulator"
)

// TestDMAControllerFallback verifies that when the Ramulator client is present
// but returns (0,false), the controller falls back to the bandwidth-based model.
func TestDMAControllerFallback(t *testing.T) {
	// bytesPerCycle default will be 8192 if <=0
	ramClient := &ramulator.Client{}
	d := NewDMAController(0, ramClient)

	bytes := int64(4096)
	hops := 2
	cycles := d.EstimateCycles(bytes, hops, nil)
	expected := 3 // ceil(4096/8192)=1 + hops 2
	if cycles != expected {
		t.Fatalf("expected %d cycles, got %d", expected, cycles)
	}

	if transfers, totalBytes, _ := d.Totals(); transfers != 0 || totalBytes != 0 {
		t.Fatalf("unexpected stats updated before record: transfers=%d bytes=%d", transfers, totalBytes)
	}

	d.Record(DMATransferHostToDigital, bytes, hops)
	transfers, totalBytes, totalHops := d.Totals()
	if transfers != 1 || totalBytes != bytes || totalHops != int64(hops) {
		t.Fatalf("unexpected stats after record: transfers=%d bytes=%d hops=%d", transfers, totalBytes, totalHops)
	}
}
