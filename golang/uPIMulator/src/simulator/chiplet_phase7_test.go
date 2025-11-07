package simulator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"uPIMulator/src/misc"
	"uPIMulator/src/simulator/chiplet"
	"uPIMulator/src/simulator/chiplet/operators"
)

func TestChipletPhase7OperatorExecution(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	loader := new(misc.ConfigLoader)
	loader.Init()
	config := chiplet.LoadConfig(loader)
	topology := chiplet.BuildTopology(config)

	lib := operators.NewLibrary(config, topology)
	transformer := lib.TransformerBlock()
	moe := lib.MoEGatingBlock()
	commands := operators.Compose(transformer, moe)
	data, err := json.MarshalIndent(commands, "", "  ")
	if err != nil {
		t.Fatalf("marshal commands: %v", err)
	}
	path := filepath.Join(tempDir, "chiplet_commands.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write commands: %v", err)
	}

	parser := new(misc.CommandLineParser)
	parser.Init()
	parser.AddOption(misc.STRING, "bin_dirpath", tempDir, tempDir)
	parser.AddOption(misc.INT, "chiplet_progress_interval", "0", "disable progress logging for tests")
	parser.AddOption(misc.INT, "chiplet_stats_flush_interval", "0", "disable periodic stats flush for tests")

	platform := new(ChipletPlatform)
	platform.Init(parser)
	defer platform.Fini()

	maxCycles := 15000
	for i := 0; i < maxCycles; i++ {
		platform.Cycle()
		if platform.executedDigitalTasks > 0 && platform.executedRramTasks > 0 && platform.executedTransferTasks > 0 {
			break
		}
	}
	platform.Dump()

	if platform.executedDigitalTasks == 0 {
		t.Fatalf("expected digital tasks executed")
	}
	if platform.executedRramTasks == 0 {
		t.Fatalf("expected rram tasks executed")
	}
	if platform.digitalScalarOps <= 0 {
		t.Fatalf("expected digital scalar ops > 0")
	}
	if platform.digitalVectorOps <= 0 {
		t.Fatalf("expected digital vector ops > 0")
	}
	if platform.executedTransferTasks == 0 {
		t.Fatalf("expected transfer tasks executed")
	}
}
