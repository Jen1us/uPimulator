package assembler

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"uPIMulator/src/simulator/chiplet"
)

// ChipletModelSpec captures a high-level description of a workload that should
// be mapped onto the heterogeneous chiplet platform. Each stage in the
// sequence will be translated into one or more chiplet commands.
type ChipletModelSpec struct {
	Name     string             `json:"name"`
	Sequence []ChipletStageSpec `json:"sequence"`
}

// stageCommandSet bundles the command groups generated for a single stage.
// Groups allow us to preserve natural parallelism (e.g., multiple experts)
// without forcing sequential dependencies across the whole stage.
type stageCommandSet struct {
	Groups [][]chiplet.CommandDescriptor
	Chain  bool
}

// ChipletStageSpec describes a single step in the model execution.
type ChipletStageSpec struct {
	Type             string                 `json:"type"`
	Name             string                 `json:"name,omitempty"`
	Chiplet          int                    `json:"chiplet,omitempty"`
	Queue            int                    `json:"queue,omitempty"`
	BufferID         int                    `json:"buffer_id,omitempty"`
	SubOp            uint32                 `json:"sub_op,omitempty"`
	MeshSrcX         int                    `json:"mesh_src_x,omitempty"`
	MeshSrcY         int                    `json:"mesh_src_y,omitempty"`
	MeshDstX         int                    `json:"mesh_dst_x,omitempty"`
	MeshDstY         int                    `json:"mesh_dst_y,omitempty"`
	CacheLine        uint64                 `json:"cache_line,omitempty"`
	Rows             int                    `json:"rows,omitempty"`
	Cols             int                    `json:"cols,omitempty"`
	K                int                    `json:"k,omitempty"`
	Tokens           int                    `json:"tokens,omitempty"`
	Features         int                    `json:"features,omitempty"`
	Latency          int                    `json:"latency,omitempty"`
	StageLatency     int                    `json:"stage_latency,omitempty"`
	ExecuteLatency   int                    `json:"execute_latency,omitempty"`
	PostLatency      int                    `json:"post_latency,omitempty"`
	Bytes            int                    `json:"bytes,omitempty"`
	ActivationBytes  int                    `json:"activation_bytes,omitempty"`
	WeightBytes      int                    `json:"weight_bytes,omitempty"`
	PulseCount       int                    `json:"pulse_count,omitempty"`
	SliceBits        int                    `json:"slice_bits,omitempty"`
	AdcSamples       int                    `json:"adc_samples,omitempty"`
	PreCycles        int                    `json:"pre_cycles,omitempty"`
	PostCycles       int                    `json:"post_cycles,omitempty"`
	HostLoadKind     string                 `json:"host_load_kind,omitempty"`
	HostStoreKind    string                 `json:"host_store_kind,omitempty"`
	NonlinearKind    string                 `json:"nonlinear_kind,omitempty"`
	RandomizeExperts bool                   `json:"randomize_experts,omitempty"`
	ExpertSeed       int64                  `json:"expert_seed,omitempty"`
	Dependencies     []int                  `json:"deps,omitempty"`
	Repeat           int                    `json:"repeat,omitempty"`
	Parallel         bool                   `json:"parallel,omitempty"`
	Direction        string                 `json:"direction,omitempty"`
	Experts          []MoEExpertSpec        `json:"experts,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
	Aux              map[string]interface{} `json:"aux,omitempty"`
}

// MoEExpertSpec overrides stage-level defaults for an individual expert shard.
type MoEExpertSpec struct {
	Name            string                 `json:"name,omitempty"`
	Chiplet         int                    `json:"chiplet,omitempty"`
	ActivationBytes int                    `json:"activation_bytes,omitempty"`
	WeightBytes     int                    `json:"weight_bytes,omitempty"`
	PulseCount      int                    `json:"pulse_count,omitempty"`
	SliceBits       int                    `json:"slice_bits,omitempty"`
	AdcSamples      int                    `json:"adc_samples,omitempty"`
	PreCycles       int                    `json:"pre_cycles,omitempty"`
	PostCycles      int                    `json:"post_cycles,omitempty"`
	StageLatency    int                    `json:"stage_latency,omitempty"`
	ExecuteLatency  int                    `json:"execute_latency,omitempty"`
	PostLatency     int                    `json:"post_latency,omitempty"`
	ChunkIndex      int                    `json:"chunk_index,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	Aux             map[string]interface{} `json:"aux,omitempty"`
}

// LoadChipletModelSpec reads a JSON encoded specification from disk.
func LoadChipletModelSpec(path string) (*ChipletModelSpec, error) {
	if path == "" {
		return nil, errors.New("empty chiplet model path")
	}

	clean := filepath.Clean(path)
	data, err := os.ReadFile(clean)
	if err != nil {
		return nil, fmt.Errorf("read chiplet model: %w", err)
	}

	spec := new(ChipletModelSpec)
	if err := json.Unmarshal(data, spec); err != nil {
		return nil, fmt.Errorf("parse chiplet model %s: %w", clean, err)
	}
	if len(spec.Sequence) == 0 {
		return nil, fmt.Errorf("chiplet model %s contains no stages", clean)
	}
	return spec, nil
}

// BuildCommands converts the specification into a list of chiplet command
// descriptors. The config/topology provide defaults for missing dimensions.
func (spec *ChipletModelSpec) BuildCommands(config *chiplet.Config, topology *chiplet.Topology) ([]chiplet.CommandDescriptor, error) {
	if spec == nil {
		return nil, errors.New("nil chiplet model spec")
	}
	if len(spec.Sequence) == 0 {
		return nil, errors.New("chiplet model contains no stages")
	}

	defaultRows := 256
	defaultCols := 256
	if topology != nil {
		if topology.Digital.PeRows > 0 {
			defaultRows = topology.Digital.PeRows
		}
		if topology.Digital.PeCols > 0 {
			defaultCols = topology.Digital.PeCols
		}
	}

	var commands []chiplet.CommandDescriptor
	nextID := int32(0)
	prevID := int32(-1)
	stageCompletionIDs := make([][]int32, 0)

	for _, stage := range spec.Sequence {
		repeat := stage.Repeat
		if repeat <= 0 {
			repeat = 1
		}
		for i := 0; i < repeat; i++ {
			stageDeps := resolveStageDependencies(stage.Dependencies, stageCompletionIDs)
			set, err := buildStageCommands(stage, defaultRows, defaultCols, config, topology)
			if err != nil {
				return nil, err
			}

			if len(set.Groups) == 0 {
				stageCompletionIDs = append(stageCompletionIDs, prevIDSlice(prevID))
				continue
			}

			for groupIdx := range set.Groups {
				group := set.Groups[groupIdx]
				for cmdIdx := range group {
					set.Groups[groupIdx][cmdIdx].ID = nextID
					nextID++
				}
			}

			prevID = wireStageDependencies(set.Groups, stageDeps, prevID, set.Chain)

			for _, group := range set.Groups {
				commands = append(commands, group...)
			}

			completion := collectStageCompletion(set.Groups)
			if len(completion) == 0 {
				stageCompletionIDs = append(stageCompletionIDs, prevIDSlice(prevID))
			} else {
				stageCompletionIDs = append(stageCompletionIDs, completion)
				prevID = completion[len(completion)-1]
			}
		}
	}

	return commands, nil
}

func buildStageCommands(stage ChipletStageSpec, defaultRows int, defaultCols int, config *chiplet.Config, topology *chiplet.Topology) (stageCommandSet, error) {
	set := stageCommandSet{
		Groups: make([][]chiplet.CommandDescriptor, 0),
		Chain:  !stage.Parallel,
	}

	stageType := strings.ToLower(strings.TrimSpace(stage.Type))
	if stageType == "" {
		return set, errors.New("stage type is empty")
	}

	switch stageType {
	case "token_prep", "tokenprep":
		cmd := buildDigitalCommand(chiplet.CommandKindPeTokenPrep, stage, defaultRows, defaultCols)
		set.Groups = append(set.Groups, wrapWithHostTransfers(stage, []chiplet.CommandDescriptor{cmd}, config, topology, defaultRows, defaultCols))
	case "attention":
		cmd := buildDigitalCommand(chiplet.CommandKindPeAttentionHead, stage, defaultRows, defaultCols)
		if cmd.Latency == 0 {
			cmd.Latency = 64
		}
		set.Groups = append(set.Groups, wrapWithHostTransfers(stage, []chiplet.CommandDescriptor{cmd}, config, topology, defaultRows, defaultCols))
	case "gemm":
		set.Groups = append(set.Groups, buildRramLinearCommands(stage, config, topology))
	case "softmax":
		cmds := buildSoftmaxCommands(stage, defaultRows, defaultCols)
		set.Groups = append(set.Groups, wrapWithHostTransfers(stage, cmds, config, topology, defaultRows, defaultCols))
	case "layernorm":
		cmds := buildLayerNormCommands(stage, defaultRows, defaultCols)
		set.Groups = append(set.Groups, wrapWithHostTransfers(stage, cmds, config, topology, defaultRows, defaultCols))
	case "residual":
		cmd := buildDigitalCommand(chiplet.CommandKindPeVpuOp, stage, defaultRows, defaultCols)
		set.Groups = append(set.Groups, wrapWithHostTransfers(stage, []chiplet.CommandDescriptor{cmd}, config, topology, defaultRows, defaultCols))
	case "elementwise", "postprocess", "moe_merge", "activation":
		cmd := buildDigitalCommand(chiplet.CommandKindPeElementwise, stage, defaultRows, defaultCols)
		if cmd.Latency == 0 {
			cmd.Latency = 24
		}
		opLabel := strings.ToLower(strings.TrimSpace(stage.NonlinearKind))
		if opLabel == "" && stage.Metadata != nil {
			opLabel = strings.ToLower(strings.TrimSpace(metadataString(stage.Metadata, "op", "")))
		}
		if opLabel == "residual" || opLabel == "residual_add" {
			cmd.Kind = chiplet.CommandKindPeVpuOp
		} else if opLabel == "layernorm" {
			cmds := buildLayerNormCommands(stage, defaultRows, defaultCols)
			set.Groups = append(set.Groups, wrapWithHostTransfers(stage, cmds, config, topology, defaultRows, defaultCols))
			break
		}
		set.Groups = append(set.Groups, wrapWithHostTransfers(stage, []chiplet.CommandDescriptor{cmd}, config, topology, defaultRows, defaultCols))
	case "moe_gating":
		cmds := buildMoEGatingCommands(stage, defaultRows, defaultCols)
		set.Groups = append(set.Groups, wrapWithHostTransfers(stage, cmds, config, topology, defaultRows, defaultCols))
	case "transfer":
		set.Groups = append(set.Groups, []chiplet.CommandDescriptor{buildTransferCommand(stage, config, topology)})
	case "rram_linear", "moe_linear":
		if len(stage.Experts) > 0 {
			for _, expert := range stage.Experts {
				group := buildRramCommandsForExpert(stage, expert, config, topology)
				set.Groups = append(set.Groups, group)
			}
			if !stage.Parallel {
				set.Chain = true
			}
		} else {
			set.Groups = append(set.Groups, buildRramLinearCommands(stage, config, topology))
		}
	case "sync":
		cmd := chiplet.CommandDescriptor{
			Kind:    chiplet.CommandKindSync,
			Target:  chiplet.TaskTargetTransfer,
			Latency: int32(stage.Latency),
		}
		set.Groups = append(set.Groups, []chiplet.CommandDescriptor{cmd})
	default:
		return set, fmt.Errorf("unsupported stage type %s", stage.Type)
	}

	return set, nil
}

func buildMoEGatingCommands(stage ChipletStageSpec, defaultRows int, defaultCols int) []chiplet.CommandDescriptor {
	tokens := stage.Tokens
	if tokens <= 0 {
		tokens = defaultRows
	}
	features := stage.Features
	if features <= 0 {
		features = defaultCols
	}
	topK := 2
	if stage.Parallel && len(stage.Experts) > 0 {
		topK = len(stage.Experts)
	}
	if topK <= 0 {
		topK = 2
	}

	spuLatency := stage.Latency
	if spuLatency <= 0 {
		spuLatency = 20
	}
	reduceLatency := stage.ExecuteLatency
	if reduceLatency <= 0 {
		reduceLatency = 12
	}

	bytesPerActivation := 2
	kDim := stage.K
	if kDim <= 0 {
		kDim = features
	}
	activationBytes := stage.ActivationBytes
	if activationBytes <= 0 {
		activationBytes = tokens * features * bytesPerActivation
	}
	weightBytes := stage.WeightBytes
	if weightBytes <= 0 {
		weightBytes = features * kDim * bytesPerActivation
	}
	outputBytes := tokens * features * bytesPerActivation

	candidateExperts := extractExpertIDs(stage)
	if len(candidateExperts) == 0 {
		candidateExperts = []int{0}
	}
	selectExperts := stage.RandomizeExperts
	if !selectExperts && stage.Metadata != nil {
		selectExperts = metadataBool(stage.Metadata, "randomize_experts", false)
	}
	selectedExperts := []int{}
	if selectExperts {
		selectedExperts = chooseExperts(stage, candidateExperts, topK)
	}
	metaBase := map[string]interface{}{
		"top_k":             topK,
		"tokens":            tokens,
		"features":          features,
		"candidate_experts": candidateExperts,
		"activation_bytes":  activationBytes,
		"weight_bytes":      weightBytes,
		"output_bytes":      outputBytes,
	}
	if len(selectedExperts) > 0 {
		metaBase["selected_experts"] = selectedExperts
	}
	if stage.Chiplet >= 0 {
		metaBase["digital_chiplet"] = stage.Chiplet
	}
	if stage.Queue != 0 {
		metaBase["buffer_id"] = stage.Queue
	}

	spuMeta := cloneMetadata(metaBase)
	spuMeta["op"] = "moe_gating_scores"
	spuMeta["precision"] = "fp16"
	scalarOps := tokens * features
	if scalarOps <= 0 {
		scalarOps = maxInt(tokens, features)
		if scalarOps <= 0 {
			scalarOps = 1
		}
	}
	vectorOps := scalarOps
	if vectorOps <= 0 {
		vectorOps = maxInt(tokens, 1)
	}
	specialOps := maxInt(tokens, topK)
	if specialOps <= 0 {
		specialOps = 1
	}
	spuMeta["scalar_ops"] = scalarOps
	spuMeta["vector_ops"] = vectorOps
	spuMeta["special_ops"] = specialOps
	spuMeta["latency_cycles"] = spuLatency

	spuCmd := chiplet.CommandDescriptor{
		Kind:       chiplet.CommandKindPeSpuOp,
		Target:     chiplet.TaskTargetDigital,
		ChipletID:  -1,
		Queue:      int32(features),
		Aux0:       uint32(tokens),
		Aux1:       uint32(features),
		Aux2:       uint32(topK),
		Latency:    int32(spuLatency),
		ExecDomain: chiplet.ExecDomainSpu,
		Metadata:   spuMeta,
	}

	reduceMeta := cloneMetadata(metaBase)
	reduceMeta["op"] = "topk_select"
	reduceOps := tokens * topK
	if reduceOps <= 0 {
		reduceOps = maxInt(tokens, topK)
		if reduceOps <= 0 {
			reduceOps = 1
		}
	}
	reduceMeta["scalar_ops"] = reduceOps
	reduceMeta["reduce_ops"] = reduceOps
	reduceMeta["latency_cycles"] = reduceLatency

	reduceCmd := chiplet.CommandDescriptor{
		Kind:       chiplet.CommandKindPeReduce,
		Target:     chiplet.TaskTargetDigital,
		ChipletID:  -1,
		Queue:      int32(features),
		Aux0:       uint32(tokens),
		Aux1:       uint32(topK),
		Latency:    int32(reduceLatency),
		ExecDomain: chiplet.ExecDomainReduce,
		Metadata:   reduceMeta,
	}

	hostMeta := cloneMetadata(metaBase)
	hostMeta["op"] = "moe_gating_fetch"

	hostCmd := chiplet.CommandDescriptor{
		Kind:     chiplet.CommandKindHostGatingFetch,
		Target:   chiplet.TaskTargetHost,
		Latency:  1,
		MeshSrcX: -1,
		MeshSrcY: -1,
		MeshDstX: -1,
		MeshDstY: -1,
		BufferID: int32(stage.Queue),
		SubOp:    uint32(topK),
		Aux0:     uint32(tokens),
		Aux1:     uint32(features),
		Metadata: hostMeta,
	}

	attachStageMetadata(&spuCmd, stage, "moe_gating_scores", spuMeta)
	attachStageMetadata(&reduceCmd, stage, "moe_topk_select", reduceMeta)
	attachStageMetadata(&hostCmd, stage, "moe_gating_fetch", hostMeta)

	return []chiplet.CommandDescriptor{spuCmd, reduceCmd, hostCmd}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func extractExpertIDs(stage ChipletStageSpec) []int {
	if len(stage.Experts) == 0 {
		return nil
	}
	ids := make([]int, 0, len(stage.Experts))
	for _, expert := range stage.Experts {
		if expert.Chiplet >= 0 {
			ids = append(ids, expert.Chiplet)
		}
	}
	return ids
}

func buildDigitalCommand(kind chiplet.CommandKind, stage ChipletStageSpec, defaultRows int, defaultCols int) chiplet.CommandDescriptor {
	rows := firstNonZero(stage.Rows, stage.Tokens, defaultRows)
	cols := firstNonZero(stage.Cols, stage.Features, defaultCols)
	k := firstNonZero(stage.K, defaultCols/2)

	cmd := chiplet.CommandDescriptor{
		Kind:         kind,
		Target:       chiplet.TaskTargetDigital,
		ChipletID:    int32(stage.Chiplet),
		Queue:        int32(firstNonZero(stage.Queue, rows)),
		Aux0:         uint32(rows),
		Aux1:         uint32(cols),
		Aux2:         uint32(k),
		Latency:      int32(stage.Latency),
		PayloadAddr:  uint32(cols),
		PayloadBytes: uint32(k),
	}

	if cmd.ChipletID < 0 {
		cmd.ChipletID = -1
	}
	extras := map[string]interface{}{
		"rows":      rows,
		"cols":      cols,
		"inner_dim": k,
	}
	if stage.Tokens > 0 {
		extras["tokens"] = stage.Tokens
	}
	if stage.Features > 0 {
		extras["features"] = stage.Features
	}
	if stage.Latency > 0 {
		extras["latency_cycles"] = stage.Latency
	}
	if stage.Queue > 0 {
		extras["queue"] = stage.Queue
	}
	if stage.BufferID > 0 {
		extras["buffer_id"] = stage.BufferID
	}
	targetBuffer := metadataString(stage.Metadata, "target_buffer", "")
	if targetBuffer == "" {
		extras["target_buffer"] = defaultTargetBufferForKind(kind)
	}

	attachStageMetadata(&cmd, stage, kind.String(), extras)
	return cmd
}

func buildTransferCommand(stage ChipletStageSpec, config *chiplet.Config, topology *chiplet.Topology) chiplet.CommandDescriptor {
	bytes := stage.Bytes
	if bytes <= 0 && topology != nil {
		bytes = topology.Digital.PeCols * topology.Digital.PeRows * 2
	}
	if bytes <= 0 {
		bytes = 64 * 1024
	}

	stageDirection := strings.ToLower(strings.TrimSpace(stage.Direction))
	cmdKind := chiplet.CommandKindTransferSchedule
	target := chiplet.TaskTargetTransfer
	stageName := stageDirection
	flags := chiplet.TransferFlagDigitalToRram
	setupMetadata := make(map[string]interface{})

	switch stageDirection {
	case "host_to_digital", "host2d", "host_load", "kv_load":
		cmdKind = chiplet.CommandKindTransferHost2D
		stageName = "transfer_host2d"
	case "digital_to_host", "d2host", "host_store":
		cmdKind = chiplet.CommandKindTransferD2Host
		stageName = "transfer_d2host"
	case "rram_to_digital", "to_digital", "r2d", "reverse":
		flags = chiplet.TransferFlagRramToDigital
		stageName = "transfer_to_digital"
	default:
		stageName = "transfer_to_rram"
	}

	cmd := chiplet.CommandDescriptor{
		Kind:         cmdKind,
		Target:       target,
		ChipletID:    int32(stage.Chiplet),
		Queue:        int32(stage.Queue),
		PayloadBytes: uint32(bytes),
		Latency:      int32(stage.Latency),
		Flags:        flags,
	}

	if cmd.Kind == chiplet.CommandKindTransferHost2D && cmd.ChipletID < 0 {
		cmd.ChipletID = 0
	}
	if cmd.Kind == chiplet.CommandKindTransferD2Host && cmd.Queue < 0 {
		cmd.Queue = 0
	}

	if cmd.Metadata == nil {
		cmd.Metadata = make(map[string]interface{})
	}
	if flags == chiplet.TransferFlagDigitalToRram {
		src := stage.Queue
		if src < 0 {
			src = 0
		}
		dst := int(cmd.ChipletID)
		if dst < 0 {
			dst = 0
		}
		cmd.Metadata[chiplet.MetadataKeySrcDigital] = src
		cmd.Metadata[chiplet.MetadataKeyDstRram] = dst
	} else if cmd.Kind == chiplet.CommandKindTransferHost2D {
		dst := int(cmd.ChipletID)
		if dst < 0 {
			dst = 0
		}
		cmd.Metadata[chiplet.MetadataKeyDstDigital] = dst
		setupMetadata["host_direction"] = "load"
	} else if cmd.Kind == chiplet.CommandKindTransferD2Host {
		src := stage.Queue
		if src < 0 {
			src = 0
		}
		cmd.Metadata[chiplet.MetadataKeySrcDigital] = src
		setupMetadata["host_direction"] = "store"
	} else {
		src := stage.Queue
		if src < 0 {
			src = 0
		}
		dst := int(cmd.ChipletID)
		if dst < 0 {
			dst = 0
		}
		cmd.Metadata[chiplet.MetadataKeySrcRram] = src
		cmd.Metadata[chiplet.MetadataKeyDstDigital] = dst
	}

	if cmd.Latency == 0 {
		cmd.Latency = 16
	}

	if cmd.Kind == chiplet.CommandKindTransferHost2D {
		setupMetadata["host_dma"] = "load"
		if stage.CacheLine != 0 {
			cmd.CacheLine = stage.CacheLine
		}
	} else if cmd.Kind == chiplet.CommandKindTransferD2Host {
		setupMetadata["host_dma"] = "store"
		if stage.CacheLine != 0 {
			cmd.CacheLine = stage.CacheLine
		}
	}

	if cmd.Kind == chiplet.CommandKindTransferHost2D || cmd.Kind == chiplet.CommandKindTransferD2Host {
		if config != nil && config.HostDmaBandwidth > 0 {
			cmd.Latency = int32(stageLatencyFromBandwidth(bytes, int(config.HostDmaBandwidth)))
		}
	} else if flags == chiplet.TransferFlagDigitalToRram && config != nil && config.TransferBandwidthDr > 0 {
		cmd.Latency = int32(stageLatencyFromBandwidth(bytes, int(config.TransferBandwidthDr)))
	} else if flags == chiplet.TransferFlagRramToDigital && config != nil && config.TransferBandwidthRd > 0 {
		cmd.Latency = int32(stageLatencyFromBandwidth(bytes, int(config.TransferBandwidthRd)))
	}

	extraMeta := map[string]interface{}{
		"bytes":      bytes,
		"stage":      stageName,
		"host_stage": cmd.Kind == chiplet.CommandKindTransferHost2D || cmd.Kind == chiplet.CommandKindTransferD2Host,
	}
	if stageDirection != "" {
		extraMeta["direction"] = stageDirection
	}
	attachStageMetadata(&cmd, stage, stageName, extraMeta, setupMetadata)

	return cmd
}

func buildRramLinearCommands(stage ChipletStageSpec, config *chiplet.Config, topology *chiplet.Topology) []chiplet.CommandDescriptor {
	chipletID := stage.Chiplet
	if chipletID < 0 {
		chipletID = 0
	}

	rows := firstNonZero(stage.Rows, stage.Tokens, 0)
	cols := firstNonZero(stage.Cols, stage.Features, 0)
	k := firstNonZero(stage.K, stage.Features, 0)
	if topology != nil {
		if rows <= 0 {
			rows = topology.Digital.PeRows
		}
		if cols <= 0 {
			cols = topology.Digital.PeCols
		}
	}
	if rows <= 0 {
		rows = 128
	}
	if cols <= 0 {
		cols = 128
	}
	if k <= 0 {
		k = rows
	}

	bytesPerActivation := 2
	bitesPerWeight := 4
	actBytes := stage.ActivationBytes
	if actBytes <= 0 {
		actBytes = rows * k * bytesPerActivation
	}
	weightBytes := stage.WeightBytes
	if weightBytes <= 0 {
		weightBits := k * cols * bitesPerWeight
		weightBytes = (weightBits + 7) / 8
	}
	outputBytes := rows * cols * bytesPerActivation

	stageLatency := int32(stage.StageLatency)
	if stageLatency == 0 {
		stageLatency = int32(stage.Latency)
	}
	if stageLatency == 0 {
		stageLatency = 28
	}

	execLatency := int32(stage.ExecuteLatency)
	if execLatency == 0 {
		execLatency = int32(stage.Latency)
	}
	if execLatency == 0 {
		execLatency = 56
	}

	postLatency := int32(stage.PostLatency)
	if postLatency == 0 {
		postLatency = int32(stage.Latency)
	}
	if postLatency == 0 {
		postLatency = 12
	}

	if stage.Direction == "transfer_to_rram" || stage.Direction == "transfer" {
		// handled elsewhere
	}

	stageCmd := chiplet.CommandDescriptor{
		Kind:         chiplet.CommandKindRramStageAct,
		Target:       chiplet.TaskTargetRram,
		ChipletID:    int32(chipletID),
		Latency:      stageLatency,
		Aux0:         uint32(rows),
		Aux1:         uint32(cols),
		Aux2:         uint32(k),
		Aux3:         uint32(outputBytes),
		PayloadBytes: uint32(actBytes),
		PayloadAddr:  uint32(weightBytes),
	}

	execCmd := chiplet.CommandDescriptor{
		Kind:         chiplet.CommandKindRramExecute,
		Target:       chiplet.TaskTargetRram,
		ChipletID:    int32(chipletID),
		Latency:      execLatency,
		Aux0:         uint32(rows),
		Aux1:         uint32(cols),
		Aux2:         uint32(k),
		Aux3:         uint32(outputBytes),
		PayloadBytes: uint32(actBytes),
		PayloadAddr:  uint32(weightBytes),
	}

	postCmd := chiplet.CommandDescriptor{
		Kind:         chiplet.CommandKindRramPost,
		Target:       chiplet.TaskTargetRram,
		ChipletID:    int32(chipletID),
		Latency:      postLatency,
		Aux0:         uint32(rows),
		Aux1:         uint32(cols),
		Aux2:         uint32(k),
		Aux3:         uint32(outputBytes),
		PayloadBytes: uint32(actBytes),
		PayloadAddr:  uint32(weightBytes),
	}

	baseMeta := map[string]interface{}{
		"rows":             rows,
		"cols":             cols,
		"inner_dim":        k,
		"activation_bytes": actBytes,
		"weight_bytes":     weightBytes,
		"output_bytes":     outputBytes,
	}
	if stage.PulseCount > 0 {
		baseMeta["pulse_count"] = stage.PulseCount
	}
	if stage.SliceBits > 0 {
		baseMeta["slice_bits"] = stage.SliceBits
	}
	if stage.AdcSamples > 0 {
		baseMeta["adc_samples"] = stage.AdcSamples
	}
	if stage.PreCycles > 0 {
		baseMeta["pre_cycles"] = stage.PreCycles
	}
	if stage.PostCycles > 0 {
		baseMeta["post_cycles"] = stage.PostCycles
	}

	stageMeta := cloneMetadata(baseMeta)
	stageMeta["op"] = "rram_stage_act"
	stageMeta["latency_cycles"] = int(stageLatency)
	if stage.StageLatency > 0 {
		stageMeta["stage_latency"] = stage.StageLatency
	}

	execMeta := cloneMetadata(baseMeta)
	execMeta["op"] = "rram_execute"
	execMeta["latency_cycles"] = int(execLatency)
	if stage.ExecuteLatency > 0 {
		execMeta["execute_latency"] = stage.ExecuteLatency
	}

	postMeta := cloneMetadata(baseMeta)
	postMeta["op"] = "rram_post"
	postMeta["latency_cycles"] = int(postLatency)
	if stage.PostLatency > 0 {
		postMeta["post_latency"] = stage.PostLatency
	}

	attachStageMetadata(&stageCmd, stage, "rram_stage_act", stageMeta)
	attachStageMetadata(&execCmd, stage, "rram_execute", execMeta)
	attachStageMetadata(&postCmd, stage, "rram_post", postMeta)

	return []chiplet.CommandDescriptor{stageCmd, execCmd, postCmd}
}

func buildRramCommandsForExpert(base ChipletStageSpec, expert MoEExpertSpec, config *chiplet.Config, topology *chiplet.Topology) []chiplet.CommandDescriptor {
	stage := base

	stage.Metadata = mergeMetadata(cloneMetadata(base.Metadata), cloneMetadata(base.Aux))
	stage.Aux = nil

	if expert.Chiplet != 0 {
		stage.Chiplet = expert.Chiplet
	}
	if expert.ActivationBytes > 0 {
		stage.ActivationBytes = expert.ActivationBytes
	}
	if expert.WeightBytes > 0 {
		stage.WeightBytes = expert.WeightBytes
	}
	if expert.PulseCount > 0 {
		stage.PulseCount = expert.PulseCount
	}
	if expert.SliceBits > 0 {
		stage.SliceBits = expert.SliceBits
	}
	if expert.AdcSamples > 0 {
		stage.AdcSamples = expert.AdcSamples
	}
	if expert.PreCycles > 0 {
		stage.PreCycles = expert.PreCycles
	}
	if expert.PostCycles > 0 {
		stage.PostCycles = expert.PostCycles
	}
	if expert.StageLatency > 0 {
		stage.StageLatency = expert.StageLatency
	}
	if expert.ExecuteLatency > 0 {
		stage.ExecuteLatency = expert.ExecuteLatency
	}
	if expert.PostLatency > 0 {
		stage.PostLatency = expert.PostLatency
	}

	if expert.ChunkIndex > 0 {
		if stage.Metadata == nil {
			stage.Metadata = make(map[string]interface{})
		}
		stage.Metadata["chunk_index"] = expert.ChunkIndex
	}

	if len(expert.Metadata) > 0 || len(expert.Aux) > 0 {
		stage.Metadata = mergeMetadata(stage.Metadata, expert.Metadata, expert.Aux)
	}

	return buildRramLinearCommands(stage, config, topology)
}

func buildSoftmaxCommands(stage ChipletStageSpec, defaultRows, defaultCols int) []chiplet.CommandDescriptor {
	cmds := make([]chiplet.CommandDescriptor, 0, 5)

	reduceMax := buildDigitalCommand(chiplet.CommandKindPeReduce, stage, defaultRows, defaultCols)
	attachStageMetadata(&reduceMax, stage, "softmax_reduce_max", map[string]interface{}{"op": "softmax_reduce_max", "nonlinear_kind": "softmax"})
	cmds = append(cmds, reduceMax)

	subtract := buildDigitalCommand(chiplet.CommandKindPeSpuOp, stage, defaultRows, defaultCols)
	attachStageMetadata(&subtract, stage, "softmax_subtract", map[string]interface{}{"op": "softmax_sub"})
	cmds = append(cmds, subtract)

	exp := buildDigitalCommand(chiplet.CommandKindPeVpuOp, stage, defaultRows, defaultCols)
	attachStageMetadata(&exp, stage, "softmax_exp", map[string]interface{}{"op": "softmax_exp"})
	cmds = append(cmds, exp)

	reduceSum := buildDigitalCommand(chiplet.CommandKindPeReduce, stage, defaultRows, defaultCols)
	attachStageMetadata(&reduceSum, stage, "softmax_reduce_sum", map[string]interface{}{"op": "softmax_reduce_sum"})
	cmds = append(cmds, reduceSum)

	norm := buildDigitalCommand(chiplet.CommandKindPeSpuOp, stage, defaultRows, defaultCols)
	attachStageMetadata(&norm, stage, "softmax_normalize", map[string]interface{}{"op": "softmax_norm"})
	cmds = append(cmds, norm)

	return cmds
}

func buildLayerNormCommands(stage ChipletStageSpec, defaultRows, defaultCols int) []chiplet.CommandDescriptor {
	cmds := make([]chiplet.CommandDescriptor, 0, 4)

	reduceMean := buildDigitalCommand(chiplet.CommandKindPeReduce, stage, defaultRows, defaultCols)
	attachStageMetadata(&reduceMean, stage, "layernorm_reduce_mean", map[string]interface{}{"op": "layernorm_mean", "nonlinear_kind": "layernorm"})
	cmds = append(cmds, reduceMean)

	reduceVar := buildDigitalCommand(chiplet.CommandKindPeReduce, stage, defaultRows, defaultCols)
	attachStageMetadata(&reduceVar, stage, "layernorm_reduce_var", map[string]interface{}{"op": "layernorm_var", "nonlinear_kind": "layernorm"})
	cmds = append(cmds, reduceVar)

	norm := buildDigitalCommand(chiplet.CommandKindPeSpuOp, stage, defaultRows, defaultCols)
	attachStageMetadata(&norm, stage, "layernorm_norm", map[string]interface{}{"op": "layernorm_norm", "nonlinear_kind": "layernorm"})
	cmds = append(cmds, norm)

	affine := buildDigitalCommand(chiplet.CommandKindPeVpuOp, stage, defaultRows, defaultCols)
	attachStageMetadata(&affine, stage, "layernorm_affine", map[string]interface{}{"op": "layernorm_affine", "nonlinear_kind": "layernorm"})
	cmds = append(cmds, affine)

	return cmds
}

func chooseExperts(stage ChipletStageSpec, candidates []int, topK int) []int {
	if len(candidates) == 0 || topK <= 0 {
		return nil
	}
	if topK > len(candidates) {
		topK = len(candidates)
	}
	seed := stage.ExpertSeed
	if seed == 0 {
		seed = int64(len(stage.Name))<<32 | int64(stage.BufferID&0xffff)<<16 | int64(stage.Queue&0xffff)
	}
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	source := rand.NewSource(seed)
	rng := rand.New(source)
	chosen := make([]int, 0, topK)
	available := append([]int(nil), candidates...)
	for len(chosen) < topK && len(available) > 0 {
		index := rng.Intn(len(available))
		chosen = append(chosen, available[index])
		available = append(available[:index], available[index+1:]...)
	}
	return chosen
}

func wrapWithHostTransfers(stage ChipletStageSpec, body []chiplet.CommandDescriptor, config *chiplet.Config, topology *chiplet.Topology, defaultRows, defaultCols int) []chiplet.CommandDescriptor {
	if len(body) == 0 {
		return nil
	}
	group := make([]chiplet.CommandDescriptor, 0, len(body)+2)
	if loads := buildHostLoadCommands(stage, defaultRows, defaultCols, config, topology); len(loads) > 0 {
		group = append(group, loads...)
	}
	group = append(group, body...)
	if stores := buildHostStoreCommands(stage, defaultRows, defaultCols, config, topology); len(stores) > 0 {
		group = append(group, stores...)
	}
	return group
}

func buildHostLoadCommands(stage ChipletStageSpec, defaultRows, defaultCols int, config *chiplet.Config, topology *chiplet.Topology) []chiplet.CommandDescriptor {
	kind := strings.TrimSpace(strings.ToLower(stage.HostLoadKind))
	if kind == "" {
		return nil
	}
	bytes := stageBytesForHostKind(stage, kind, defaultRows, defaultCols)
	if bytes <= 0 {
		return nil
	}
	spec := ChipletStageSpec{
		Type:      "transfer",
		Name:      stage.Name + "_host_load",
		Chiplet:   stage.Chiplet,
		Queue:     stage.Queue,
		Bytes:     bytes,
		Direction: "host_to_digital",
		Metadata: map[string]interface{}{
			"host_kind":     kind,
			"target_buffer": targetBufferForHostKind(kind, false),
		},
	}
	cmd := buildTransferCommand(spec, config, topology)
	attachStageMetadata(&cmd, spec, "host_load")
	return []chiplet.CommandDescriptor{cmd}
}

func buildHostStoreCommands(stage ChipletStageSpec, defaultRows, defaultCols int, config *chiplet.Config, topology *chiplet.Topology) []chiplet.CommandDescriptor {
	kind := strings.TrimSpace(strings.ToLower(stage.HostStoreKind))
	if kind == "" {
		return nil
	}
	bytes := stageBytesForHostKind(stage, kind, defaultRows, defaultCols)
	if bytes <= 0 {
		return nil
	}
	spec := ChipletStageSpec{
		Type:      "transfer",
		Name:      stage.Name + "_host_store",
		Chiplet:   stage.Chiplet,
		Queue:     stage.Queue,
		Bytes:     bytes,
		Direction: "digital_to_host",
		Metadata: map[string]interface{}{
			"host_kind":     kind,
			"target_buffer": targetBufferForHostKind(kind, true),
		},
	}
	cmd := buildTransferCommand(spec, config, topology)
	attachStageMetadata(&cmd, spec, "host_store")
	return []chiplet.CommandDescriptor{cmd}
}

func stageActivationBytes(stage ChipletStageSpec, defaultRows, defaultCols int) int {
	if stage.ActivationBytes > 0 {
		return stage.ActivationBytes
	}
	rows := firstNonZero(stage.Rows, stage.Tokens, defaultRows)
	cols := firstNonZero(stage.Cols, stage.Features, defaultCols)
	if rows <= 0 {
		rows = defaultRows
	}
	if cols <= 0 {
		cols = defaultCols
	}
	bytes := rows * cols * 2
	if bytes <= 0 {
		bytes = defaultRows * 2
	}
	return bytes
}

func stageWeightBytes(stage ChipletStageSpec, defaultRows, defaultCols int) int {
	if stage.WeightBytes > 0 {
		return stage.WeightBytes
	}
	rows := firstNonZero(stage.Rows, stage.Tokens, defaultRows)
	cols := firstNonZero(stage.Cols, stage.Features, defaultCols)
	k := firstNonZero(stage.K, defaultCols)
	if rows <= 0 {
		rows = defaultRows
	}
	if cols <= 0 {
		cols = defaultCols
	}
	if k <= 0 {
		k = cols
	}
	bytes := rows * k * 2
	if bytes <= 0 {
		bytes = cols * 2
	}
	return bytes
}

func stageBytesForHostKind(stage ChipletStageSpec, kind string, defaultRows, defaultCols int) int {
	switch kind {
	case "kv_cache", "activation", "result":
		return stageActivationBytes(stage, defaultRows, defaultCols)
	case "weight", "expert_weight":
		return stageWeightBytes(stage, defaultRows, defaultCols)
	case "weight_meta":
		if stage.Metadata != nil {
			if v, ok := metadataInt64(stage.Metadata, "meta_bytes"); ok {
				return int(v)
			}
		}
		return stageActivationBytes(stage, defaultRows, defaultCols)
	default:
		return stageActivationBytes(stage, defaultRows, defaultCols)
	}
}

func targetBufferForHostKind(kind string, store bool) string {
	switch kind {
	case "kv_cache", "activation", "result":
		if store {
			return "scratch"
		}
		return "activation"
	case "weight", "expert_weight":
		return "weights"
	case "weight_meta":
		return "scratch"
	default:
		if store {
			return "scratch"
		}
		return "activation"
	}
}

func defaultTargetBufferForKind(kind chiplet.CommandKind) string {
	switch kind {
	case chiplet.CommandKindPeTokenPrep,
		chiplet.CommandKindPeAttentionHead,
		chiplet.CommandKindPeElementwise,
		chiplet.CommandKindPeSpuOp,
		chiplet.CommandKindPeVpuOp,
		chiplet.CommandKindPeReduce:
		return "activation"
	default:
		return "scratch"
	}
}

func attachStageMetadata(cmd *chiplet.CommandDescriptor, stage ChipletStageSpec, fallback string, extras ...map[string]interface{}) {
	if cmd == nil {
		return
	}

	components := make([]map[string]interface{}, 0, 3+len(extras))
	if len(cmd.Metadata) > 0 {
		components = append(components, cloneMetadata(cmd.Metadata))
	}
	if len(stage.Metadata) > 0 {
		components = append(components, cloneMetadata(stage.Metadata))
	}
	if len(stage.Aux) > 0 {
		components = append(components, cloneMetadata(stage.Aux))
	}
	if base := stageBaseMetadata(stage); len(base) > 0 {
		components = append(components, base)
	}
	for _, extra := range extras {
		if len(extra) == 0 {
			continue
		}
		components = append(components, extra)
	}

	meta := mergeMetadata(components...)
	if meta == nil {
		meta = make(map[string]interface{})
	}
	if _, ok := meta["stage"]; !ok {
		if tag := normalizedStageName(stage, fallback); tag != "" {
			meta["stage"] = tag
		}
	}
	if _, ok := meta["stage_type"]; !ok && stage.Type != "" {
		meta["stage_type"] = normalizeStageLabel(stage.Type)
	}

	cmd.Metadata = meta
	applyStageOverrides(cmd, stage, meta)
}

func stageBaseMetadata(stage ChipletStageSpec) map[string]interface{} {
	meta := make(map[string]interface{})
	if stage.Name != "" {
		meta["stage_name"] = stage.Name
	}
	if stage.Type != "" {
		meta["stage_kind"] = normalizeStageLabel(stage.Type)
	}
	if stage.Queue != 0 {
		meta["queue"] = stage.Queue
	}
	if stage.Chiplet >= 0 {
		meta["chiplet"] = stage.Chiplet
	}
	if stage.Bytes > 0 {
		meta["bytes_hint"] = stage.Bytes
	}
	if stage.Repeat > 1 {
		meta["repeat"] = stage.Repeat
	}
	if stage.Parallel {
		meta["parallel"] = true
	}
	if stage.BufferID != 0 {
		meta["buffer_id"] = stage.BufferID
	}
	if stage.SubOp != 0 {
		meta["sub_op"] = stage.SubOp
	}
	if stage.CacheLine != 0 {
		meta["cache_line"] = stage.CacheLine
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func mergeMetadata(parts ...map[string]interface{}) map[string]interface{} {
	var merged map[string]interface{}
	for _, part := range parts {
		if part == nil {
			continue
		}
		if len(part) == 0 {
			continue
		}
		if merged == nil {
			merged = make(map[string]interface{}, len(part))
		}
		for key, value := range part {
			merged[key] = value
		}
	}
	return merged
}

func normalizedStageName(stage ChipletStageSpec, fallback string) string {
	if tag := normalizeStageLabel(stage.Name); tag != "" {
		return tag
	}
	if tag := normalizeStageLabel(fallback); tag != "" {
		return tag
	}
	return normalizeStageLabel(stage.Type)
}

func normalizeStageLabel(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	v = strings.ReplaceAll(v, " ", "_")
	v = strings.ReplaceAll(v, "-", "_")
	return strings.ToLower(v)
}

func applyStageOverrides(cmd *chiplet.CommandDescriptor, stage ChipletStageSpec, meta map[string]interface{}) {
	if cmd == nil {
		return
	}

	if stage.BufferID != 0 {
		cmd.BufferID = int32(stage.BufferID)
	} else if v, ok := metadataInt64(meta, "buffer_id"); ok {
		cmd.BufferID = int32(v)
	}

	if stage.SubOp != 0 {
		cmd.SubOp = stage.SubOp
	} else if v, ok := metadataUint64(meta, "sub_op"); ok {
		cmd.SubOp = uint32(v)
	}

	if stage.CacheLine != 0 {
		cmd.CacheLine = stage.CacheLine
	} else if v, ok := metadataUint64(meta, "cache_line"); ok {
		cmd.CacheLine = v
	}

	cmd.MeshSrcX = int32(stage.MeshSrcX)
	cmd.MeshSrcY = int32(stage.MeshSrcY)
	cmd.MeshDstX = int32(stage.MeshDstX)
	cmd.MeshDstY = int32(stage.MeshDstY)

	if cmd.MeshSrcX == 0 && cmd.MeshSrcY == 0 {
		if x, ok := metadataInt64(meta, "mesh_src_x"); ok {
			cmd.MeshSrcX = int32(x)
		}
		if y, ok := metadataInt64(meta, "mesh_src_y"); ok {
			cmd.MeshSrcY = int32(y)
		}
	}
	if cmd.MeshDstX == 0 && cmd.MeshDstY == 0 {
		if x, ok := metadataInt64(meta, "mesh_dst_x"); ok {
			cmd.MeshDstX = int32(x)
		}
		if y, ok := metadataInt64(meta, "mesh_dst_y"); ok {
			cmd.MeshDstY = int32(y)
		}
	}
}

func metadataString(meta map[string]interface{}, key, fallback string) string {
	if meta == nil {
		return fallback
	}
	if value, ok := meta[key]; ok {
		if text, ok := value.(string); ok {
			trimmed := strings.TrimSpace(text)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return fallback
}

func metadataBool(meta map[string]interface{}, key string, fallback bool) bool {
	if meta == nil {
		return fallback
	}
	value, ok := meta[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		default:
			return fallback
		}
	case int, int32, int64:
		val, _ := toInt64(v)
		return val != 0
	case float32:
		return v != 0
	case float64:
		return v != 0
	default:
		return fallback
	}
}

func metadataInt(meta map[string]interface{}, key string, fallback int) int {
	if value, ok := metadataInt64(meta, key); ok {
		return int(value)
	}
	return fallback
}

func metadataInt64(meta map[string]interface{}, key string) (int64, bool) {
	if meta == nil {
		return 0, false
	}
	raw, ok := meta[key]
	if !ok {
		return 0, false
	}
	return toInt64(raw)
}

func metadataUint64(meta map[string]interface{}, key string) (uint64, bool) {
	value, ok := metadataInt64(meta, key)
	if !ok {
		return 0, false
	}
	if value < 0 {
		return 0, false
	}
	return uint64(value), true
}

func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > (1<<63 - 1) {
			return 0, false
		}
		return int64(v), true
	case float32:
		return int64(v), true
	case float64:
		return int64(v), true
	case string:
		if v == "" {
			return 0, false
		}
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func stageLatencyFromBandwidth(bytes int, bandwidth int) int {
	if bandwidth <= 0 {
		return 16
	}
	cycles := bytes / bandwidth
	if bytes%bandwidth != 0 {
		cycles++
	}
	if cycles <= 0 {
		cycles = 1
	}
	return cycles
}

func resolveStageDependencies(deps []int, stageCompletionIDs [][]int32) []int32 {
	if len(deps) == 0 {
		return nil
	}

	results := make([]int32, 0, len(deps))
	for _, dep := range deps {
		if dep < 0 || dep >= len(stageCompletionIDs) {
			continue
		}
		for _, id := range stageCompletionIDs[dep] {
			results = appendUniqueInts(results, id)
		}
	}
	return results
}

func wireStageDependencies(groups [][]chiplet.CommandDescriptor, stageDeps []int32, prevID int32, chain bool) int32 {
	lastID := prevID
	for groupIdx := range groups {
		group := groups[groupIdx]
		if len(group) == 0 {
			continue
		}

		first := &groups[groupIdx][0]

		if len(stageDeps) > 0 {
			first.Dependencies = appendUniqueInts(first.Dependencies, stageDeps...)
		}

		if chain {
			dep := prevID
			if groupIdx > 0 {
				prevGroup := groups[groupIdx-1]
				if len(prevGroup) > 0 {
					dep = prevGroup[len(prevGroup)-1].ID
				} else {
					dep = -1
				}
			}
			if dep >= 0 {
				first.Dependencies = appendUniqueInts(first.Dependencies, dep)
			}
		} else if len(stageDeps) == 0 && groupIdx == 0 && prevID >= 0 {
			first.Dependencies = appendUniqueInts(first.Dependencies, prevID)
		}

		for cmdIdx := 1; cmdIdx < len(group); cmdIdx++ {
			prevCmd := group[cmdIdx-1]
			group[cmdIdx].Dependencies = appendUniqueInts(group[cmdIdx].Dependencies, prevCmd.ID)
		}

		lastID = group[len(group)-1].ID
	}
	return lastID
}

func appendUniqueInts(dst []int32, values ...int32) []int32 {
	existing := make(map[int32]struct{}, len(dst))
	for _, v := range dst {
		existing[v] = struct{}{}
	}
	for _, v := range values {
		if _, ok := existing[v]; ok {
			continue
		}
		dst = append(dst, v)
		existing[v] = struct{}{}
	}
	return dst
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

func collectStageCompletion(groups [][]chiplet.CommandDescriptor) []int32 {
	completion := make([]int32, 0, len(groups))
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		completion = append(completion, group[len(group)-1].ID)
	}
	return completion
}

func prevIDSlice(prevID int32) []int32 {
	if prevID < 0 {
		return nil
	}
	return []int32{prevID}
}
