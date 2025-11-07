package assembler

import (
	"os"
	"path/filepath"
	"testing"

	"uPIMulator/src/simulator/chiplet"
)

func TestChipletModelBuildCommands(t *testing.T) {
	t.Parallel()

	specJSON := `{
  "name": "transformer_moe_block",
  "sequence": [
    {"type": "token_prep", "tokens": 128, "features": 256, "latency": 8},
    {"type": "attention", "rows": 256, "cols": 256, "k": 128, "latency": 64, "deps": [0]},
    {"type": "softmax", "rows": 128, "cols": 128, "latency": 24, "deps": [1]},
    {"type": "transfer", "bytes": 65536, "latency": 14, "direction": "digital_to_rram", "deps": [2]},
    {
      "type": "moe_linear",
      "parallel": true,
      "pulse_count": 32,
      "adc_samples": 96,
      "pre_cycles": 12,
      "post_cycles": 8,
      "stage_latency": 20,
      "execute_latency": 60,
      "post_latency": 15,
      "activation_bytes": 49152,
      "weight_bytes": 98304,
      "experts": [
        {"chiplet": 0},
        {"chiplet": 1, "activation_bytes": 45056, "weight_bytes": 90112, "execute_latency": 64}
      ],
      "deps": [3]
    },
    {"type": "transfer", "bytes": 49152, "latency": 12, "direction": "rram_to_digital", "deps": [4]},
    {"type": "moe_merge", "rows": 96, "cols": 32, "latency": 22, "deps": [5]}
  ]
}`

	tempDir := t.TempDir()
	specPath := filepath.Join(tempDir, "model.json")
	if err := os.WriteFile(specPath, []byte(specJSON), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	spec, err := LoadChipletModelSpec(specPath)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	config := &chiplet.Config{
		NumDigitalChiplets:      2,
		NumRramChiplets:         1,
		DigitalPesPerChiplet:    2,
		DigitalPeRows:           256,
		DigitalPeCols:           256,
		DigitalSpusPerChiplet:   2,
		TransferBandwidthDr:     8192,
		TransferBandwidthRd:     8192,
		RramTilesPerDim:         1,
		RramSasPerTileDim:       1,
		RramSaRows:              128,
		RramSaCols:              128,
		RramCellBits:            2,
		RramDacBits:             2,
		RramAdcBits:             12,
		DigitalActivationBuffer: 8 << 20,
		DigitalScratchBuffer:    8 << 20,
		RramInputBuffer:         8 << 20,
		RramOutputBuffer:        8 << 20,
	}
	topology := chiplet.BuildTopology(config)

	commands, err := spec.BuildCommands(config, topology)
	if err != nil {
		t.Fatalf("build commands: %v", err)
	}

	if got := len(commands); got < 12 {
		t.Fatalf("expected at least 12 commands, got %d", got)
	}

	if findKind(commands, chiplet.CommandKindPeTokenPrep) < 0 {
		t.Fatalf("token prep command missing")
	}
	if findKind(commands, chiplet.CommandKindPeAttentionHead) < 0 {
		t.Fatalf("attention head command missing")
	}
	if countKind(commands, chiplet.CommandKindPeReduce) == 0 {
		t.Fatalf("reduce commands missing (softmax/layernorm)")
	}

	xferDown := findTransfer(commands, chiplet.TransferFlagDigitalToRram)
	if xferDown < 0 {
		t.Fatalf("digital->rram transfer missing")
	}
	firstStage := findNextKind(commands, chiplet.CommandKindRramStageAct, xferDown)
	if firstStage <= xferDown {
		t.Fatalf("rram stage should appear after transfer, got %d (transfer %d)", firstStage, xferDown)
	}
	firstExec := findNextKind(commands, chiplet.CommandKindRramExecute, firstStage)
	firstPost := findNextKind(commands, chiplet.CommandKindRramPost, firstExec)
	if firstExec <= firstStage || firstPost <= firstExec {
		t.Fatalf("rram stage/execute/post ordering broken: stage=%d exec=%d post=%d", firstStage, firstExec, firstPost)
	}

	xferUp := findTransfer(commands, chiplet.TransferFlagRramToDigital)
	if xferUp < 0 {
		t.Fatalf("rram->digital transfer missing")
	}
	if !containsDep(commands[xferUp].Dependencies, commands[firstPost].ID) {
		t.Fatalf("rram->digital transfer should depend on rram post command")
	}

	elemIdx := findKind(commands, chiplet.CommandKindPeElementwise)
	if elemIdx < 0 {
		t.Fatalf("elementwise command missing")
	}
}

func containsDep(deps []int32, target int32) bool {
	for _, dep := range deps {
		if dep == target {
			return true
		}
	}
	return false
}

func findKind(cmds []chiplet.CommandDescriptor, kind chiplet.CommandKind) int {
	for idx, cmd := range cmds {
		if cmd.Kind == kind {
			return idx
		}
	}
	return -1
}

func findNextKind(cmds []chiplet.CommandDescriptor, kind chiplet.CommandKind, start int) int {
	for idx := start + 1; idx < len(cmds); idx++ {
		if cmds[idx].Kind == kind {
			return idx
		}
	}
	return -1
}

func countKind(cmds []chiplet.CommandDescriptor, kind chiplet.CommandKind) int {
	total := 0
	for _, cmd := range cmds {
		if cmd.Kind == kind {
			total++
		}
	}
	return total
}

func findTransfer(cmds []chiplet.CommandDescriptor, flag uint32) int {
	for idx, cmd := range cmds {
		if cmd.Kind == chiplet.CommandKindTransferSchedule && cmd.Flags == flag {
			return idx
		}
		if cmd.Kind == chiplet.CommandKindTransferC2D && flag == chiplet.TransferFlagDigitalToRram {
			return idx
		}
		if cmd.Kind == chiplet.CommandKindTransferD2C && flag == chiplet.TransferFlagRramToDigital {
			return idx
		}
		if cmd.Kind == chiplet.CommandKindTransferHost2D && flag == chiplet.TransferFlagDigitalToRram {
			continue
		}
		if cmd.Kind == chiplet.CommandKindTransferD2Host && flag == chiplet.TransferFlagRramToDigital {
			continue
		}
	}
	return -1
}
