package operators

import (
	"fmt"
	"uPIMulator/src/simulator/chiplet"
)

// OperatorKind enumerates the high-level logical operators that can be
// constructed via the chiplet command ISA. The values intentionally differ
// from chiplet.CommandKind to capture multi-command macros (e.g. Attention).
type OperatorKind int

const (
	OperatorKindUnknown OperatorKind = iota
	OperatorKindAttention
	OperatorKindMoEBlock
	OperatorKindSwiGlu
	OperatorKindTransformer
	OperatorKindTokenizer
	OperatorKindEmbedding
)

// OperatorDescriptor aggregates a sequence of chiplet commands plus metadata
// describing the logical operator.
type OperatorDescriptor struct {
	Name     string
	Kind     OperatorKind
	Commands []chiplet.CommandDescriptor
}

// Library provides canned operator macros that map to Chiplet command
// descriptors. It uses the chiplet config/topology to size the commands.
type Library struct {
	config   *chiplet.Config
	topology *chiplet.Topology
}

// NewLibrary constructs an operator library sized for the given platform.
func NewLibrary(config *chiplet.Config, topology *chiplet.Topology) *Library {
	return &Library{config: config, topology: topology}
}

// AttentionBlock describes a minimal attention pipeline (token prep ->
// transfer到RRAM进行QKV线性 -> RRAM执行 -> transfer回Digital -> elementwise).
func (lib *Library) AttentionBlock() OperatorDescriptor {
	rows := 256
	cols := 256
	k := 128
	if lib.topology != nil {
		if lib.topology.Digital.PeRows > 0 {
			rows = lib.topology.Digital.PeRows
		}
		if lib.topology.Digital.PeCols > 0 {
			cols = lib.topology.Digital.PeCols
		}
	}

	bytesPerActivation := 2 // FP16
	bitsPerWeight := 4      // INT4
	activationBytes := rows * k * bytesPerActivation
	weightBits := k * cols * bitsPerWeight
	weightBytes := (weightBits + 7) / 8
	outputBytes := rows * cols * bytesPerActivation
	weightMeta := map[string]interface{}{
		"weight_tag": "attention_w",
		"tile_id":    0,
		"array_id":   0,
	}
	commands := []chiplet.CommandDescriptor{
		{
			Kind:      chiplet.CommandKindPeTokenPrep,
			Target:    chiplet.TaskTargetDigital,
			ChipletID: -1,
			Queue:     int32(rows),
			Aux0:      uint32(rows),
			Aux1:      uint32(cols),
			Aux2:      uint32(k / 2),
			Latency:   8,
		},
		{
			Kind:         chiplet.CommandKindTransferSchedule,
			Target:       chiplet.TaskTargetTransfer,
			Queue:        0,
			ChipletID:    0,
			PayloadBytes: uint32(activationBytes),
			Flags:        chiplet.TransferFlagDigitalToRram,
			Latency:      16,
		},
		{
			Kind:         chiplet.CommandKindRramWeightLoad,
			Target:       chiplet.TaskTargetRram,
			ChipletID:    0,
			PayloadAddr:  uint32(weightBytes),
			PayloadBytes: uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(k),
			Latency:      24,
			Metadata:     cloneMetadata(weightMeta),
		},
		{
			Kind:         chiplet.CommandKindRramStageAct,
			Target:       chiplet.TaskTargetRram,
			ChipletID:    0,
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(k),
			Aux3:         uint32(outputBytes),
			Latency:      32,
			Metadata:     cloneMetadata(weightMeta),
		},
		{
			Kind:         chiplet.CommandKindRramExecute,
			Target:       chiplet.TaskTargetRram,
			ChipletID:    0,
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(k),
			Aux3:         uint32(outputBytes),
			Latency:      64,
			Metadata:     cloneMetadata(weightMeta),
		},
		{
			Kind:         chiplet.CommandKindRramPost,
			Target:       chiplet.TaskTargetRram,
			ChipletID:    0,
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(k),
			Aux3:         uint32(outputBytes),
			Latency:      12,
			Metadata:     cloneMetadata(weightMeta),
		},
		{
			Kind:         chiplet.CommandKindTransferSchedule,
			Target:       chiplet.TaskTargetTransfer,
			Queue:        0,
			ChipletID:    0,
			PayloadBytes: uint32(outputBytes),
			Flags:        chiplet.TransferFlagRramToDigital,
			Latency:      16,
		},
		{
			Kind:      chiplet.CommandKindPeElementwise,
			Target:    chiplet.TaskTargetDigital,
			ChipletID: -1,
			Queue:     int32(cols),
			Aux0:      uint32(rows),
			Aux1:      uint32(cols),
			Latency:   32,
		},
	}

	assignIds(commands, 0)
	chainDependencies(commands)

	return OperatorDescriptor{
		Name:     "attention_block",
		Kind:     OperatorKindAttention,
		Commands: commands,
	}
}

// MoEGatingBlock emits a simplified MoE pipeline: digital gating, expert
// transfers, CIM execution and merge.
func (lib *Library) MoEGatingBlock() OperatorDescriptor {
	rows := 96
	cols := 64
	k := 32
	if lib.topology != nil {
		if lib.topology.Digital.PeRows > 0 {
			rows = lib.topology.Digital.PeRows / 2
			if rows <= 0 {
				rows = lib.topology.Digital.PeRows
			}
		}
		if lib.topology.Digital.PeCols > 0 {
			cols = lib.topology.Digital.PeCols / 2
			if cols <= 0 {
				cols = lib.topology.Digital.PeCols
			}
		}
	}

	bytesPerActivation := 2
	bitsPerWeight := 4
	activationBytes := rows * k * bytesPerActivation
	weightBits := k * cols * bitsPerWeight
	weightBytes := (weightBits + 7) / 8
	outputBytes := rows * cols * bytesPerActivation

	candidateExperts := make([]int, 0)
	if lib.topology != nil && lib.topology.Rram.NumChiplets > 0 {
		for i := 0; i < lib.topology.Rram.NumChiplets; i++ {
			candidateExperts = append(candidateExperts, i)
		}
	} else {
		for i := 0; i < 4; i++ {
			candidateExperts = append(candidateExperts, i)
		}
	}

	scalarOps := rows * cols
	if scalarOps <= 0 {
		scalarOps = rows
		if scalarOps <= 0 {
			scalarOps = cols
		}
		if scalarOps <= 0 {
			scalarOps = 1
		}
	}
	vectorOps := scalarOps
	specialOps := rows
	if specialOps <= 0 {
		specialOps = 1
	}

	metaBase := map[string]interface{}{
		"top_k":             2,
		"tokens":            rows,
		"features":          cols,
		"candidate_experts": candidateExperts,
		"buffer_id":         cols,
		"digital_chiplet":   0,
		"activation_bytes":  activationBytes,
		"weight_bytes":      weightBytes,
		"output_bytes":      outputBytes,
	}
	weightMeta := map[string]interface{}{
		"weight_tag": "moe_w",
		"tile_id":    0,
		"array_id":   0,
	}

	commands := []chiplet.CommandDescriptor{
		{
			Kind:      chiplet.CommandKindPeSpuOp,
			Target:    chiplet.TaskTargetDigital,
			ChipletID: -1,
			Queue:     int32(cols),
			Aux0:      uint32(rows),
			Aux1:      uint32(cols),
			Aux2:      2,
			Latency:   24,
			Metadata: func() map[string]interface{} {
				meta := cloneMetadata(metaBase)
				meta["op"] = "moe_gating_scores"
				meta["precision"] = "fp16"
				meta["scalar_ops"] = scalarOps
				meta["vector_ops"] = vectorOps
				meta["special_ops"] = specialOps
				return meta
			}(),
		},
		{
			Kind:      chiplet.CommandKindPeReduce,
			Target:    chiplet.TaskTargetDigital,
			ChipletID: -1,
			Queue:     int32(cols),
			Aux0:      uint32(rows),
			Aux1:      2,
			Latency:   16,
			Metadata: func() map[string]interface{} {
				meta := cloneMetadata(metaBase)
				meta["op"] = "topk_select"
				meta["reduce_kind"] = "topk"
				reduceOps := rows * 2
				if reduceOps <= 0 {
					reduceOps = 2
				}
				meta["reduce_ops"] = reduceOps
				meta["scalar_ops"] = reduceOps
				return meta
			}(),
		},
		{
			Kind:     chiplet.CommandKindHostGatingFetch,
			Target:   chiplet.TaskTargetHost,
			Latency:  1,
			SubOp:    2,
			Aux0:     uint32(rows),
			Aux1:     uint32(cols),
			BufferID: int32(cols),
			Metadata: func() map[string]interface{} {
				meta := cloneMetadata(metaBase)
				meta["op"] = "moe_gating_fetch"
				return meta
			}(),
		},
		{
			Kind:         chiplet.CommandKindTransferSchedule,
			Target:       chiplet.TaskTargetTransfer,
			Queue:        0,
			ChipletID:    0,
			PayloadBytes: uint32(activationBytes),
			Flags:        chiplet.TransferFlagDigitalToRram,
			Latency:      12,
		},
		{
			Kind:         chiplet.CommandKindRramWeightLoad,
			Target:       chiplet.TaskTargetRram,
			ChipletID:    0,
			PayloadAddr:  uint32(weightBytes),
			PayloadBytes: uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(k),
			Latency:      18,
			Metadata:     cloneMetadata(weightMeta),
		},
		{
			Kind:         chiplet.CommandKindRramStageAct,
			Target:       chiplet.TaskTargetRram,
			ChipletID:    0,
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(k),
			Aux3:         uint32(outputBytes),
			Latency:      28,
			Metadata:     cloneMetadata(weightMeta),
		},
		{
			Kind:         chiplet.CommandKindRramExecute,
			Target:       chiplet.TaskTargetRram,
			ChipletID:    0,
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(k),
			Aux3:         uint32(outputBytes),
			Latency:      56,
			Metadata:     cloneMetadata(weightMeta),
		},
		{
			Kind:         chiplet.CommandKindRramPost,
			Target:       chiplet.TaskTargetRram,
			ChipletID:    0,
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(k),
			Aux3:         uint32(outputBytes),
			Latency:      10,
			Metadata:     cloneMetadata(weightMeta),
		},
		{
			Kind:         chiplet.CommandKindTransferSchedule,
			Target:       chiplet.TaskTargetTransfer,
			Queue:        0,
			ChipletID:    0,
			PayloadBytes: uint32(outputBytes),
			Flags:        chiplet.TransferFlagRramToDigital,
			Latency:      12,
		},
		{
			Kind:      chiplet.CommandKindPeElementwise,
			Target:    chiplet.TaskTargetDigital,
			ChipletID: -1,
			Queue:     96,
			Aux0:      96,
			Aux1:      32,
			Aux2:      4,
			Latency:   22,
		},
	}

	assignIds(commands, 0)
	chainDependencies(commands)

	return OperatorDescriptor{
		Name:     "moe_block",
		Kind:     OperatorKindMoEBlock,
		Commands: commands,
	}
}

// SwiGluBlock produces a pair of elementwise stages representing the SwiGLU
// activation (GEMM result split -> elementwise ops -> combine).
func (lib *Library) SwiGluBlock() OperatorDescriptor {
	commands := []chiplet.CommandDescriptor{
		{
			Kind:      chiplet.CommandKindPeElementwise,
			Target:    chiplet.TaskTargetDigital,
			ChipletID: -1,
			Queue:     128,
			Aux0:      128,
			Aux1:      128,
			Aux2:      8,
			Latency:   20,
		},
		{
			Kind:      chiplet.CommandKindPeElementwise,
			Target:    chiplet.TaskTargetDigital,
			ChipletID: -1,
			Queue:     128,
			Aux0:      128,
			Aux1:      128,
			Aux2:      16,
			Latency:   18,
		},
	}

	assignIds(commands, 0)
	chainDependencies(commands)

	return OperatorDescriptor{
		Name:     "swiglu",
		Kind:     OperatorKindSwiGlu,
		Commands: commands,
	}
}

// TransformerBlock chains attention and a SwiGLU FFN to approximate a minimal
// single-head transformer layer.
func (lib *Library) TransformerBlock() OperatorDescriptor {
	attention := lib.AttentionBlock()
	swiglu := lib.SwiGluBlock()
	commands := Compose(attention, swiglu)

	return OperatorDescriptor{
		Name:     "transformer_block",
		Kind:     OperatorKindTransformer,
		Commands: commands,
	}
}

// TransformerPipeline composes multiple transformer blocks sequentially. This
// is useful when no external specification is provided yet the user still
// wants to exercise a deeper pipeline.
func (lib *Library) TransformerPipeline(layers int) []chiplet.CommandDescriptor {
	if layers <= 1 {
		return lib.TransformerBlock().Commands
	}

	result := make([]chiplet.CommandDescriptor, 0)
	var lastID int32 = -1
	nextID := int32(0)
	for layer := 0; layer < layers; layer++ {
		block := cloneCommands(lib.TransformerBlock().Commands)
		for i := range block {
			block[i].ID += nextID
			if block[i].Queue >= 0 {
				block[i].Queue += int32(layer) * 16
			}
			if lastID >= 0 && i == 0 {
				block[i].Dependencies = appendUnique(block[i].Dependencies, lastID)
			}
		}
		if len(block) > 0 {
			lastID = block[len(block)-1].ID
		}
		nextID += int32(len(block))
		result = append(result, block...)
	}
	return result
}

func Compose(ops ...OperatorDescriptor) []chiplet.CommandDescriptor {
	result := make([]chiplet.CommandDescriptor, 0)
	var lastID *int32
	nextID := int32(0)
	for _, op := range ops {
		cmds := cloneCommands(op.Commands)
		assignIds(cmds, nextID)
		if lastID != nil && len(cmds) > 0 {
			cmds[0].Dependencies = appendUnique(cmds[0].Dependencies, *lastID)
		}
		if len(cmds) > 0 {
			id := cmds[len(cmds)-1].ID
			lastID = &id
		}
		nextID += int32(len(cmds))
		result = append(result, cmds...)
	}
	return result
}

func assignIds(cmds []chiplet.CommandDescriptor, start int32) {
	for i := range cmds {
		cmds[i].ID = start + int32(i)
	}
}

func chainDependencies(cmds []chiplet.CommandDescriptor) {
	for i := 1; i < len(cmds); i++ {
		prev := cmds[i-1].ID
		cmds[i].Dependencies = appendUnique(cmds[i].Dependencies, prev)
	}
}

func appendUnique(dst []int32, value int32) []int32 {
	for _, v := range dst {
		if v == value {
			return dst
		}
	}
	return append(dst, value)
}

func cloneCommands(cmds []chiplet.CommandDescriptor) []chiplet.CommandDescriptor {
	copySlice := make([]chiplet.CommandDescriptor, len(cmds))
	for i, cmd := range cmds {
		cmdCopy := cmd
		if len(cmd.Dependencies) > 0 {
			deps := make([]int32, len(cmd.Dependencies))
			copy(deps, cmd.Dependencies)
			cmdCopy.Dependencies = deps
		}
		copySlice[i] = cmdCopy
	}
	return copySlice
}

func cloneMetadata(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// DescribeKind returns a printable label for an OperatorKind.
func DescribeKind(kind OperatorKind) string {
	switch kind {
	case OperatorKindAttention:
		return "attention"
	case OperatorKindMoEBlock:
		return "moe_block"
	case OperatorKindSwiGlu:
		return "swiglu"
	case OperatorKindTransformer:
		return "transformer_block"
	case OperatorKindTokenizer:
		return "tokenizer"
	case OperatorKindEmbedding:
		return "embedding"
	default:
		return fmt.Sprintf("kind_%d", kind)
	}
}
