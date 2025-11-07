package simulator

import (
	"reflect"
	"testing"

	"uPIMulator/src/misc"
	"uPIMulator/src/simulator/chiplet"
	digitalpkg "uPIMulator/src/simulator/chiplet/digital"
)

func newTestPlatformForGating() *ChipletPlatform {
	loader := new(misc.ConfigLoader)
	loader.Init()
	config := chiplet.LoadConfig(loader)
	topology := chiplet.BuildTopology(config)
	return &ChipletPlatform{
		config:          config,
		topology:        topology,
		gatingQueues:    make(map[gatingKey][]*moeGatingSnapshot),
		moeEventMetrics: make(map[int]*moeEventMetrics),
	}
}

func TestBuildDigitalDescriptorForMoEGatingScores(t *testing.T) {
	t.Parallel()

	platform := newTestPlatformForGating()

	rows := 96
	cols := 64
	topK := 2
	activationBytes := rows * cols * 2
	outputBytes := rows * topK * 2

	cmd := &chiplet.CommandDescriptor{
		Kind: chiplet.CommandKindPeSpuOp,
		Aux0: uint32(rows),
		Aux1: uint32(cols),
		Aux2: uint32(topK),
		Metadata: map[string]interface{}{
			"op":               "moe_gating_scores",
			"tokens":           rows,
			"features":         cols,
			"top_k":            topK,
			"activation_bytes": activationBytes,
			"output_bytes":     outputBytes,
		},
	}

	desc := platform.buildDigitalDescriptorFromCommand(cmd, 0)
	if desc == nil {
		t.Fatalf("expected descriptor for moe_gating_scores")
	}
	if desc.Description != "moe_gating_scores" {
		t.Fatalf("unexpected description: %s", desc.Description)
	}
	if desc.ExecUnit != digitalpkg.ExecUnitSpu {
		t.Fatalf("expected ExecUnitSpu, got %v", desc.ExecUnit)
	}
	if desc.InputBytes != int64(activationBytes) {
		t.Fatalf("expected input bytes %d, got %d", activationBytes, desc.InputBytes)
	}
	if desc.OutputBytes != int64(outputBytes) {
		t.Fatalf("expected output bytes %d, got %d", outputBytes, desc.OutputBytes)
	}
	if desc.ScalarOps <= 0 || desc.VectorOps <= 0 {
		t.Fatalf("expected positive scalar/vector ops, got %d / %d", desc.ScalarOps, desc.VectorOps)
	}
}

func TestBuildDigitalDescriptorForMoETopKSelect(t *testing.T) {
	t.Parallel()

	platform := newTestPlatformForGating()

	rows := 96
	topK := 2
	scoreBytes := rows * 64 * 2
	outputBytes := rows * topK * 2

	cmd := &chiplet.CommandDescriptor{
		Kind:  chiplet.CommandKindPeReduce,
		Aux0:  uint32(rows),
		SubOp: uint32(topK),
		Metadata: map[string]interface{}{
			"op":               "topk_select",
			"tokens":           rows,
			"top_k":            topK,
			"activation_bytes": scoreBytes,
			"output_bytes":     outputBytes,
			"reduce_ops":       rows * topK,
		},
	}

	desc := platform.buildDigitalDescriptorFromCommand(cmd, 0)
	if desc == nil {
		t.Fatalf("expected descriptor for topk_select")
	}
	if desc.Description != "topk_select" {
		t.Fatalf("unexpected description: %s", desc.Description)
	}
	if desc.ExecUnit != digitalpkg.ExecUnitSpu {
		t.Fatalf("expected ExecUnitSpu, got %v", desc.ExecUnit)
	}
	if desc.InputBytes != int64(scoreBytes) {
		t.Fatalf("expected input bytes %d, got %d", scoreBytes, desc.InputBytes)
	}
	if desc.OutputBytes != int64(outputBytes) {
		t.Fatalf("expected output bytes %d, got %d", outputBytes, desc.OutputBytes)
	}
	if desc.ScalarOps != rows*topK {
		t.Fatalf("expected scalar ops %d, got %d", rows*topK, desc.ScalarOps)
	}
	if desc.VectorOps != 0 {
		t.Fatalf("expected zero vector ops, got %d", desc.VectorOps)
	}
}

func TestMoeStatsTracking(t *testing.T) {
	t.Parallel()

	platform := newTestPlatformForGating()
	platform.currentCycle = 10

	rows := 32
	features := 16
	bufferID := 7

	topkCmd := &chiplet.CommandDescriptor{
		Kind:      chiplet.CommandKindPeReduce,
		Target:    chiplet.TaskTargetDigital,
		ChipletID: 0,
		Queue:     int32(features),
		Aux0:      uint32(rows),
		Aux1:      2,
		Metadata: map[string]interface{}{
			"op":        "topk_select",
			"buffer_id": bufferID,
			"tokens":    rows,
			"features":  features,
		},
	}
	platform.recordGatingSnapshotFromCommand(0, topkCmd)

	hostCmd := &chiplet.CommandDescriptor{
		Kind:      chiplet.CommandKindHostGatingFetch,
		Target:    chiplet.TaskTargetHost,
		ChipletID: 0,
		BufferID:  int32(bufferID),
		SubOp:     2,
		Metadata: map[string]interface{}{
			"op": "moe_gating_fetch",
		},
	}
	task := &chiplet.Task{NodeID: 99, Target: chiplet.TaskTargetHost, Payload: hostCmd}
	platform.handleHostTask(task)

	if platform.moeEventsTotal != 1 {
		t.Fatalf("expected 1 moe event, got %d", platform.moeEventsTotal)
	}
	if platform.moeSnapshotHits != 1 {
		t.Fatalf("expected snapshot hit, got %d", platform.moeSnapshotHits)
	}

	barrierCmd := &chiplet.CommandDescriptor{
		Kind:      chiplet.CommandKindPeBarrier,
		Target:    chiplet.TaskTargetDigital,
		ChipletID: 0,
		Latency:   4,
		Metadata: map[string]interface{}{
			"op":          "moe_barrier",
			"parent_node": 99,
		},
	}
	platform.currentCycle = 25
	platform.recordMoeBarrierMetrics(barrierCmd)

	if platform.moeLatencySamples != 1 {
		t.Fatalf("expected latency sample recorded")
	}
	if platform.moeSessionsCompleted != 1 {
		t.Fatalf("expected session completion")
	}
	if _, exists := platform.moeEventMetrics[99]; exists {
		t.Fatalf("expected moe event metrics cleared")
	}
}

func TestHostGatingFetchConsumesSnapshot(t *testing.T) {
	t.Parallel()

	platform := newTestPlatformForGating()
	platform.gatingQueues = make(map[gatingKey][]*moeGatingSnapshot)
	platform.currentCycle = 123

	topkCmd := &chiplet.CommandDescriptor{
		Kind: chiplet.CommandKindPeReduce,
		Aux0: 4,
		Aux1: 2,
		Metadata: map[string]interface{}{
			"op":                "topk_select",
			"buffer_id":         7,
			"top_k":             2,
			"tokens":            4,
			"features":          64,
			"candidate_experts": []int{3, 1, 2},
			"gating_scores":     []float64{0.1, 0.9, 0.2},
			"activation_bytes":  1024,
			"weight_bytes":      512,
			"output_bytes":      256,
		},
	}

	platform.recordGatingSnapshotFromCommand(0, topkCmd)

	hostCmd := &chiplet.CommandDescriptor{
		Kind:      chiplet.CommandKindHostGatingFetch,
		ChipletID: 0,
		BufferID:  7,
		Metadata: map[string]interface{}{
			"top_k":     2,
			"buffer_id": 7,
		},
	}

	orchestrator := new(chiplet.HostOrchestrator)
	orchestrator.Init(platform.config, platform.topology, "")
	platform.orchestrator = orchestrator

	task := &chiplet.Task{
		NodeID:  42,
		Payload: hostCmd,
	}

	platform.handleHostTask(task)

	event, ok := orchestrator.ConsumeHostEvent(42)
	if !ok || event == nil {
		t.Fatalf("expected host event to be registered")
	}
	expectedSelected := []int{1, 2}
	if !reflect.DeepEqual(event.SelectedExperts, expectedSelected) {
		t.Fatalf("selected experts mismatch: got %v want %v", event.SelectedExperts, expectedSelected)
	}
	if len(event.CandidateExperts) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(event.CandidateExperts))
	}
	if event.ActivationBytes != 1024 || event.WeightBytes != 512 || event.OutputBytes != 256 {
		t.Fatalf("unexpected bytes: %+v", event)
	}
	if leftover := platform.consumeGatingSnapshot(0, 7); leftover != nil {
		t.Fatalf("snapshot queue should be empty after consumption")
	}
}
