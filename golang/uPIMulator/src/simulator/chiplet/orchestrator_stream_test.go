package chiplet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHostOrchestratorStreamingBatches(t *testing.T) {
	t.Helper()

	commands := []CommandDescriptor{
		{
			ID:        0,
			Kind:      CommandKindPeElementwise,
			Target:    TaskTargetDigital,
			ChipletID: 0,
			Queue:     0,
			Latency:   4,
			Metadata: map[string]interface{}{
				"stage": "stream_unit",
			},
		},
	}

	tempDir := t.TempDir()
	commandPath := filepath.Join(tempDir, "stream_commands.json")
	data, err := json.Marshal(commands)
	if err != nil {
		t.Fatalf("marshal commands: %v", err)
	}
	if err := os.WriteFile(commandPath, data, 0o644); err != nil {
		t.Fatalf("write commands: %v", err)
	}

	config := &Config{
		NumDigitalChiplets:      1,
		NumRramChiplets:         1,
		DigitalPeRows:           128,
		DigitalPeCols:           128,
		TransferBandwidthDr:     4096,
		TransferBandwidthRd:     4096,
		HostDmaBandwidth:        8192,
		DigitalActivationBuffer: 1 << 30,
		DigitalScratchBuffer:    1 << 30,
		RramInputBuffer:         1 << 30,
		RramOutputBuffer:        1 << 30,
		HostStreamTotalBatches:  3,
		HostStreamLowWatermark:  0,
		HostStreamHighWatermark: 1,
	}
	topology := BuildTopology(config)

	orchestrator := new(HostOrchestrator)
	orchestrator.Init(config, topology, commandPath)
	t.Cleanup(func() { orchestrator.Fini() })

	batchesSeen := make(map[int]struct{})
	mutated := false

	for iter := 0; iter < 10 && len(batchesSeen) < 2; iter++ {
		tasks := orchestrator.Advance()
		if len(tasks) == 0 {
			continue
		}
		for _, task := range tasks {
			cmd, ok := task.Payload.(*CommandDescriptor)
			if !ok || cmd == nil {
				t.Fatalf("expected command payload, got %T", task.Payload)
			}
			if cmd.Metadata == nil {
				t.Fatalf("missing metadata for batch task")
			}
			batchValue, ok := cmd.Metadata["stream_batch_id"]
			if !ok {
				t.Fatalf("command missing stream_batch_id metadata")
			}
			batchID := asInt(batchValue)
			batchesSeen[batchID] = struct{}{}

			if !mutated {
				cmd.Metadata["mutated"] = true
				mutated = true
			} else if batchID != 0 {
				if _, exists := cmd.Metadata["mutated"]; exists {
					t.Fatalf("metadata mutation leaked into batch %d", batchID)
				}
			}

			orchestrator.NotifyTaskCompletion(task.NodeID)
		}
	}

	if len(batchesSeen) < 2 {
		t.Fatalf("expected streaming to instantiate multiple batches, saw %d", len(batchesSeen))
	}
}

func asInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		panic("unexpected type for int conversion")
	}
}
