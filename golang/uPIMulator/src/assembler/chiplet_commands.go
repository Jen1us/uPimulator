package assembler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"uPIMulator/src/misc"
	"uPIMulator/src/simulator/chiplet"
	"uPIMulator/src/simulator/chiplet/operators"
)

// AssembleChipletCommands emits a default command sequence when the simulator
// runs in chiplet mode. The output feeds the Phase6 chiplet pipeline and can be
// replaced with compiler-generated descriptors in the future.
func (this *Assembler) AssembleChipletCommands() {
	if misc.RuntimePlatformMode() != misc.PlatformModeChiplet {
		return
	}

	commands := this.chipletCommandSequence()
	if len(commands) == 0 {
		return
	}

	if err := os.MkdirAll(this.bin_dirpath, 0o755); err != nil {
		panic(err)
	}

	path := filepath.Join(this.bin_dirpath, "chiplet_commands.json")
	data, err := json.MarshalIndent(commands, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}

	fmt.Printf("[chiplet] 已写出命令序列：%s（共 %d 条）\n", path, len(commands))
}

func (this *Assembler) chipletCommandSequence() []chiplet.CommandDescriptor {
	configLoader := new(misc.ConfigLoader)
	configLoader.Init()
	chipletConfig := chiplet.LoadConfig(configLoader)
	topology := chiplet.BuildTopology(chipletConfig)

	modelPath := strings.TrimSpace(this.chipletModelPath)
	if modelPath != "" {
		spec, err := LoadChipletModelSpec(modelPath)
		if err != nil {
			panic(err)
		}
		commands, err := spec.BuildCommands(chipletConfig, topology)
		if err != nil {
			panic(err)
		}
		return commands
	}

	return this.defaultChipletCommandSequence(chipletConfig, topology)
}

func (this *Assembler) defaultChipletCommandSequence(chipletConfig *chiplet.Config, topology *chiplet.Topology) []chiplet.CommandDescriptor {
	library := operators.NewLibrary(chipletConfig, topology)

	benchmark := strings.ToUpper(this.benchmark)
	switch benchmark {
	case "TRANSFORMER":
		return library.TransformerPipeline(6)
	default:
		attention := library.AttentionBlock()
		moe := library.MoEGatingBlock()
		swiglu := library.SwiGluBlock()
		return operators.Compose(attention, moe, swiglu)
	}
}
