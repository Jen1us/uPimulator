# Chiplet 底层 ISA 规格（草案）

本文档配合《chiplet_isa_refactor_plan.md》，列出在 Phase 1 中新增的 opcode、执行域和字段，便于后续阶段实现和编写命令脚本。

## 1. 执行域 ExecDomain

| ExecDomain | 说明 |
| --- | --- |
| `ExecDomainPeArray` | 数字 chiplet 的 systolic/PE 阵列 |
| `ExecDomainSpu` | 数字 chiplet 的标量单元（SPU） |
| `ExecDomainVpu` | 数字 chiplet 的向量单元（VPU） |
| `ExecDomainReduce` | 数字 chiplet 的归约单元/树 |
| `ExecDomainCim` | RRAM/CIM 控制器 |
| `ExecDomainDma` | 互联/Host DMA |
| `ExecDomainHost` | Host CPU/管理核执行 |

## 2. 指令族

### 2.1 Digital ISA

| Opcode | ExecDomain | 说明 |
| --- | --- | --- |
| `pe_cmd_gemm` | `PeArray` | 传统 systolic GEMM（保留兼容） |
| `pe_cmd_attention_head` | `PeArray` | 兼容旧实现 |
| `pe_cmd_gemm` (legacy) | `PeArray` | 同上 |
| `pe_cmd_spu_op` | `Spu` | 标量四则/指数/特殊函数，`sub_op` 指明操作 |
| `pe_cmd_vpu_op` | `Vpu` | 向量逐元素算子 |
| `pe_cmd_reduce` | `Reduce` | sum/max/argmax 等归约 |
| `pe_cmd_buffer_alloc` / `release` | `Host/Digital` | 显式 buffer 管理 |
| `pe_cmd_barrier` | `Host/Digital` | pipeline 同步 |
| `pe_cmd_spu_op` (`moe_gating_scores` 等) | `Spu` | 用于 MoE gating、LayerNorm 等标量运算 |

### 2.2 RRAM ISA

| Opcode | ExecDomain | 说明 |
| --- | --- | --- |
| `rram_cmd_weight_load` | `Dma/Cim` | 将权重块加载到指定 tile/SA |
| `rram_cmd_stage_act` | `Cim` | 预处理激活/输入 staging |
| `rram_cmd_execute` | `Cim` | 脉冲执行阶段 |
| `rram_cmd_post` | `Cim` | ADC & 后处理 |

### 2.3 互联/传输

| Opcode | ExecDomain | 说明 |
| --- | --- | --- |
| `xfer_cmd_c2d` | `Dma` | CIM → Digital 搬运（含 hop/能耗） |
| `xfer_cmd_d2c` | `Dma` | Digital → CIM 搬运 |
| `xfer_cmd_host2d` | `Dma` | Host/DRAM → Digital/KV cache load |
| `xfer_cmd_d2host` | `Dma` | Digital → Host/DRAM store |

### 2.4 Host 伪指令

| Opcode | ExecDomain | 说明 |
| --- | --- | --- |
| `host_cmd_embed_lookup` | `Host` | token embedding/pos encoding |
| `host_cmd_router_prep` | `Host` | MoE gating 准备（读 buffer） |
| `host_cmd_gating_fetch` | `Host` | 获取 gating 结果 |
| `host_cmd_lm_head` | `Host/Digital` | 最后一层 logits |
| `host_cmd_sync` | `Host` | 全局 barrier 等控制 |

---

## 3. CommandDescriptor 字段说明

| 字段 | 含义 |
| --- | --- |
| `ExecDomain` | 对应执行单元，默认 `ExecDomainUndefined` 表示沿用旧逻辑 |
| `MeshSrcX/Y`, `MeshDstX/Y` | 传输路径坐标，用于估算 hop 和功耗 |
| `CacheLine` | DRAM/KV cache 物理地址（字节） |
| `BufferID` | 数字 chiplet buffer 句柄（scratch/activation） |
| `SubOp` | 对于 SPU/VPU/REDUCE 等指令，指定子操作 ID |
| `Metadata` | 任意键值对，供 JSON/Orchestrator 传递额外参数（如 `{"op":"moe_gating_scores","top_k":2}`） |

旧字段（`ChipletID/Queue` 等）仍可使用；未指定的新字段默认 0 或空。

---

> 后续阶段可在本文件追加子章节，描述 `SubOp` 枚举、Buffer ID 约定、权重映射格式等细节。
