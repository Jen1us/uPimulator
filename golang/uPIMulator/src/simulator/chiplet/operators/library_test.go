package operators

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"uPIMulator/src/misc"
	"uPIMulator/src/simulator/chiplet"
)

func TestLibraryAttentionProducesDependencies(t *testing.T) {
	loader := new(misc.ConfigLoader)
	loader.Init()
	config := chiplet.LoadConfig(loader)
	topology := chiplet.BuildTopology(config)

	lib := NewLibrary(config, topology)
	attn := lib.AttentionBlock()
	if attn.Kind != OperatorKindAttention {
		t.Fatalf("expected attention kind, got %v", attn.Kind)
	}
	if len(attn.Commands) == 0 {
		t.Fatalf("expected non-empty command set")
	}
	for i, cmd := range attn.Commands {
		if cmd.ID != int32(i) {
			t.Fatalf("expected sequential ids, got %d at %d", cmd.ID, i)
		}
		if i > 0 {
			deps := cmd.Dependencies
			if len(deps) == 0 || deps[0] != int32(i-1) {
				t.Fatalf("expected dependency on previous command for index %d", i)
			}
		}
	}

	moe := lib.MoEGatingBlock()
	swiglu := lib.SwiGluBlock()
	combined := Compose(attn, moe, swiglu)
	expectedLen := len(attn.Commands) + len(moe.Commands) + len(swiglu.Commands)
	if len(combined) != expectedLen {
		t.Fatalf("expected %d commands, got %d", expectedLen, len(combined))
	}
	// ensure dependencies connect across boundaries
	last := combined[len(combined)-1]
	if len(last.Dependencies) == 0 {
		t.Fatalf("expected terminal command to have dependencies")
	}
}

func TestComposeSerialization(t *testing.T) {
	loader := new(misc.ConfigLoader)
	loader.Init()
	config := chiplet.LoadConfig(loader)
	topology := chiplet.BuildTopology(config)
	lib := NewLibrary(config, topology)

	commands := Compose(lib.AttentionBlock(), lib.SwiGluBlock())
	if len(commands) == 0 {
		t.Fatalf("expected commands")
	}

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "commands.json")
	data, err := json.Marshal(commands)
	if err != nil {
		t.Fatalf("marshal commands: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write commands: %v", err)
	}
	loaded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read commands: %v", err)
	}
	if len(loaded) != len(data) {
		t.Fatalf("expected %d bytes, got %d", len(data), len(loaded))
	}
}

func TestMoEBlockTargets(t *testing.T) {
	loader := new(misc.ConfigLoader)
	loader.Init()
	config := chiplet.LoadConfig(loader)
	topology := chiplet.BuildTopology(config)
	lib := NewLibrary(config, topology)

	moe := lib.MoEGatingBlock()
	if moe.Kind != OperatorKindMoEBlock {
		t.Fatalf("expected moe block kind, got %v", moe.Kind)
	}

	var digital, transfer, rram int
	for _, cmd := range moe.Commands {
		switch cmd.Target {
		case chiplet.TaskTargetDigital:
			digital++
		case chiplet.TaskTargetTransfer:
			transfer++
		case chiplet.TaskTargetRram:
			rram++
		}
	}

	if digital == 0 || transfer == 0 || rram == 0 {
		t.Fatalf("expected moe block to include digital/transfer/rram commands, got d=%d t=%d r=%d", digital, transfer, rram)
	}
}

func TestTransformerBlock(t *testing.T) {
	loader := new(misc.ConfigLoader)
	loader.Init()
	config := chiplet.LoadConfig(loader)
	topology := chiplet.BuildTopology(config)
	lib := NewLibrary(config, topology)

	block := lib.TransformerBlock()
	if block.Kind != OperatorKindTransformer {
		t.Fatalf("expected transformer kind, got %v", block.Kind)
	}
	if len(block.Commands) == 0 {
		t.Fatalf("expected transformer block commands")
	}
	for i, cmd := range block.Commands {
		if cmd.ID != int32(i) {
			t.Fatalf("expected sequential id, got %d at %d", cmd.ID, i)
		}
		if i > 0 {
			deps := cmd.Dependencies
			if len(deps) == 0 {
				t.Fatalf("expected dependency for command %d", i)
			}
		}
	}
}
