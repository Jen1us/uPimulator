package chiplet

import "uPIMulator/src/linker/kernel/instruction"

// CommandKind enumerates the high-level chiplet commands exposed through the
// Chiplet ISA extension. It mirrors the dedicated opcodes introduced in the
// linker and allows host/runtime code to reason about intent without parsing
// textual payloads.
type CommandKind int

const (
	CommandKindInvalid CommandKind = iota
	// Legacy/Phase-2 commands (保留兼容性) --------------------------------------
	CommandKindPeGemm
	CommandKindPeAttentionHead
	CommandKindPeElementwise
	CommandKindPeTokenPrep
	CommandKindRramStageAct
	CommandKindRramExecute
	CommandKindRramPost
	CommandKindTransferSchedule
	CommandKindSync
	// Digital ISA --------------------------------------------------------------
	CommandKindPeSpuOp
	CommandKindPeVpuOp
	CommandKindPeReduce
	CommandKindPeBufferAlloc
	CommandKindPeBufferRelease
	CommandKindPeBarrier
	// RRAM ISA -----------------------------------------------------------------
	CommandKindRramWeightLoad
	// Interconnect/DMA ---------------------------------------------------------
	CommandKindTransferC2D
	CommandKindTransferD2C
	CommandKindTransferHost2D
	CommandKindTransferD2Host
	// Host-level伪指令 ---------------------------------------------------------
	CommandKindHostEmbedLookup
	CommandKindHostRouterPrep
	CommandKindHostLmHead
	CommandKindHostSynchronize
	CommandKindHostGatingFetch
)

// ExecDomain 用于描述命令应在何种执行单元完成，便于统计与限流。
type ExecDomain int

const (
	ExecDomainUndefined ExecDomain = iota
	ExecDomainPeArray
	ExecDomainSpu
	ExecDomainVpu
	ExecDomainReduce
	ExecDomainCim
	ExecDomainDma
	ExecDomainHost
)

// CommandDescriptor captures the ISA-visible command structure. A Chiplet
// command is described by a compact header stored in WRAM/MRAM which the
// runtime will translate into scheduler tasks.
type CommandDescriptor struct {
	ID           int32                  `json:"id"`
	Kind         CommandKind            `json:"kind"`
	Target       TaskTarget             `json:"target"`
	ExecDomain   ExecDomain             `json:"exec_domain,omitempty"`
	ChipletID    int32                  `json:"chiplet_id"`
	Queue        int32                  `json:"queue"`
	PayloadAddr  uint32                 `json:"payload_addr"`
	PayloadBytes uint32                 `json:"payload_bytes"`
	Aux0         uint32                 `json:"aux0"`
	Aux1         uint32                 `json:"aux1"`
	Aux2         uint32                 `json:"aux2"`
	Aux3         uint32                 `json:"aux3"`
	Flags        uint32                 `json:"flags"`
	Latency      int32                  `json:"latency"`
	MeshSrcX     int32                  `json:"mesh_src_x,omitempty"`
	MeshSrcY     int32                  `json:"mesh_src_y,omitempty"`
	MeshDstX     int32                  `json:"mesh_dst_x,omitempty"`
	MeshDstY     int32                  `json:"mesh_dst_y,omitempty"`
	CacheLine    uint64                 `json:"cache_line_addr,omitempty"`
	BufferID     int32                  `json:"buffer_id,omitempty"`
	SubOp        uint32                 `json:"sub_op,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	Dependencies []int32                `json:"deps,omitempty"`
}

const (
	TransferFlagDirectionMask uint32 = 0x1
	TransferFlagDigitalToRram uint32 = 0
	TransferFlagRramToDigital uint32 = 1
)

// Metadata keys for transfer endpoints and hop metrics.
const (
	MetadataKeySrcDigital   = "src_digital"
	MetadataKeyDstDigital   = "dst_digital"
	MetadataKeySrcRram      = "src_rram"
	MetadataKeyDstRram      = "dst_rram"
	MetadataKeyTransferHops = "transfer_hops"
)

// String returns a human-readable identifier for debugging/logging.
func (k CommandKind) String() string {
	switch k {
	case CommandKindPeGemm:
		return "pe_cmd_gemm"
	case CommandKindPeAttentionHead:
		return "pe_cmd_attention_head"
	case CommandKindPeElementwise:
		return "pe_cmd_elementwise"
	case CommandKindPeTokenPrep:
		return "pe_cmd_token_prep"
	case CommandKindRramStageAct:
		return "rram_cmd_stage_act"
	case CommandKindRramExecute:
		return "rram_cmd_execute"
	case CommandKindRramPost:
		return "rram_cmd_post"
	case CommandKindTransferSchedule:
		return "xfer_cmd_schedule"
	case CommandKindSync:
		return "chiplet_cmd_sync"
	case CommandKindPeSpuOp:
		return "pe_cmd_spu_op"
	case CommandKindPeVpuOp:
		return "pe_cmd_vpu_op"
	case CommandKindPeReduce:
		return "pe_cmd_reduce"
	case CommandKindPeBufferAlloc:
		return "pe_cmd_buffer_alloc"
	case CommandKindPeBufferRelease:
		return "pe_cmd_buffer_release"
	case CommandKindPeBarrier:
		return "pe_cmd_barrier"
	case CommandKindRramWeightLoad:
		return "rram_cmd_weight_load"
	case CommandKindTransferC2D:
		return "xfer_cmd_c2d"
	case CommandKindTransferD2C:
		return "xfer_cmd_d2c"
	case CommandKindTransferHost2D:
		return "xfer_cmd_host2d"
	case CommandKindTransferD2Host:
		return "xfer_cmd_d2host"
	case CommandKindHostEmbedLookup:
		return "host_cmd_embed_lookup"
	case CommandKindHostRouterPrep:
		return "host_cmd_router_prep"
	case CommandKindHostLmHead:
		return "host_cmd_lm_head"
	case CommandKindHostSynchronize:
		return "host_cmd_sync"
	case CommandKindHostGatingFetch:
		return "host_cmd_gating_fetch"
	default:
		return "chiplet_cmd_invalid"
	}
}

// CommandKindFromOpcode maps a linker opcode into the corresponding command
// kind. The mapping is kept close to the chiplet package to ease future
// decoding from binary descriptors.
func CommandKindFromOpcode(opcode string) CommandKind {
	switch opcode {
	case "pe_cmd_gemm":
		return CommandKindPeGemm
	case "pe_cmd_attention_head":
		return CommandKindPeAttentionHead
	case "pe_cmd_elementwise":
		return CommandKindPeElementwise
	case "pe_cmd_token_prep":
		return CommandKindPeTokenPrep
	case "rram_cmd_stage_act":
		return CommandKindRramStageAct
	case "rram_cmd_execute":
		return CommandKindRramExecute
	case "rram_cmd_post":
		return CommandKindRramPost
	case "xfer_cmd_schedule":
		return CommandKindTransferSchedule
	case "chiplet_cmd_sync":
		return CommandKindSync
	case "pe_cmd_spu_op":
		return CommandKindPeSpuOp
	case "pe_cmd_vpu_op":
		return CommandKindPeVpuOp
	case "pe_cmd_reduce":
		return CommandKindPeReduce
	case "pe_cmd_buffer_alloc":
		return CommandKindPeBufferAlloc
	case "pe_cmd_buffer_release":
		return CommandKindPeBufferRelease
	case "pe_cmd_barrier":
		return CommandKindPeBarrier
	case "rram_cmd_weight_load":
		return CommandKindRramWeightLoad
	case "xfer_cmd_c2d":
		return CommandKindTransferC2D
	case "xfer_cmd_d2c":
		return CommandKindTransferD2C
	case "xfer_cmd_host2d":
		return CommandKindTransferHost2D
	case "xfer_cmd_d2host":
		return CommandKindTransferD2Host
	case "host_cmd_embed_lookup":
		return CommandKindHostEmbedLookup
	case "host_cmd_router_prep":
		return CommandKindHostRouterPrep
	case "host_cmd_lm_head":
		return CommandKindHostLmHead
	case "host_cmd_sync":
		return CommandKindHostSynchronize
	case "host_cmd_gating_fetch":
		return CommandKindHostGatingFetch
	default:
		return CommandKindInvalid
	}
}

// CommandKindFromOpCode maps an opcode from the linker instruction set to its
// semantic command kind.
func CommandKindFromOpCode(op instruction.OpCode) CommandKind {
	switch op {
	case instruction.PE_CMD_GEMM:
		return CommandKindPeGemm
	case instruction.PE_CMD_ATTENTION_HEAD:
		return CommandKindPeAttentionHead
	case instruction.PE_CMD_ELEMENTWISE:
		return CommandKindPeElementwise
	case instruction.PE_CMD_TOKEN_PREP:
		return CommandKindPeTokenPrep
	case instruction.RRAM_CMD_STAGE_ACT:
		return CommandKindRramStageAct
	case instruction.RRAM_CMD_EXECUTE:
		return CommandKindRramExecute
	case instruction.RRAM_CMD_POST:
		return CommandKindRramPost
	case instruction.XFER_CMD_SCHEDULE:
		return CommandKindTransferSchedule
	case instruction.CHIPLET_CMD_SYNC:
		return CommandKindSync
	default:
		return CommandKindInvalid
	}
}
