# Chiplet Stage 拆解与数据流约定

为了让 Chiplet 仿真具备可复现的 Host⇄Chiplet、Digital⇄RRAM 交互行为，所有模型阶段都必须遵循统一的输入/输出与算子拆解规则。本文件约束了常见算子的 stage 组合及元数据字段，供 `tools/generate_chiplet_model.py` 与 `assembler/chiplet_model.go` 协同使用。

## Host 传输语义

| 名称 | 方向 | 典型用途 | Stage 约束 |
| --- | --- | --- | --- |
| `host2d_kv_cache` | Host → Digital | KV Cache Q/K/V 激活加载 | `type: "transfer"`, `direction: "host_to_digital"`, `metadata.host_kind = "kv_cache"` |
| `host2d_weight` | Host → Digital | 首次载入线性层/专家权重 | 同上，`host_kind = "weight"` |
| `host2d_meta` | Host → Digital | Scale / Zero-Point / LUT | `host_kind = "weight_meta"` |
| `d2host_kv_cache` | Digital → Host | 写回 KV Cache | `direction: "digital_to_host"`, `host_kind = "kv_cache"` |
| `host2c_weight` | Host → Digital → RRAM | CIM 权重灌入 | 由生成器插入 Host→Digital + Digital→RRAM 两个 stage，metadata 指定 `weight_tag` |

> **实现提示**：仍复用 `transfer` stage；通过 `metadata.host_kind` 区分业务语义，ChipletPlatform 会在 host DMA 统计中记录这些字节。

## 算子拆解

### Attention（每个 block）

1. `host2d_kv_cache`：加载输入 token。
2. `attention`：Digital 芯粒做 Q/K/V GEMM。
3. `softmax`：拆解为 `reduce_max` → `spu_sub` → `vpu_exp` → `reduce_sum` → `spu_div`。
4. `attention`（2nd）/`postprocess`：输出投影与 residual。
5. `d2host_kv_cache`（可选）：写回 KV Cache。

### Feed-Forward / SwiGLU

1. `host2d_kv_cache`（或来自上一阶段）。
2. `rram_linear` / `attention_head`：主矩阵乘。
3. `nonlinear`：SwiGLU/GELU 分别映射到 VPU element-wise。
4. `residual`：VPU element-wise add。

### LayerNorm / Softmax 细化

| 阶段 | Chiplet 命令 |
| --- | --- |
| `reduce_mean` | `pe_cmd_reduce`，`metadata.op = "layernorm_mean"` |
| `reduce_var` | `pe_cmd_reduce`，`metadata.op = "layernorm_var"` |
| `layernorm_norm` | `pe_cmd_spu_op` |
| `layernorm_affine` | `pe_cmd_vpu_op` |
| `softmax_max` | `pe_cmd_reduce` |
| `softmax_sub` | `pe_cmd_spu_op` |
| `softmax_exp` | `pe_cmd_vpu_op` |
| `softmax_sum` | `pe_cmd_reduce` |
| `softmax_norm` | `pe_cmd_spu_op` |

### MoE Gating

1. `moe_gating_scores`：SPU/VPU 计算 gating logits。
2. `topk_select`：`pe_cmd_reduce`，`metadata.reduce_kind = "topk"`。
3. 随机选择 `top_k` 专家：assembler 从 `candidate_experts` 中随机（种子来自 stage 名 + 层号）挑选，结果写入 `metadata.selected_experts`。
4. `moe_linear`：专家矩阵乘任务被派发至 `selected_experts` 对应的 RRAM 芯粒。
5. `moe_merge`：Digital 芯粒聚合回结果。

## Stage 元数据字段

为避免新增大量布尔字段，沿用现有 `stage.Metadata` 传递控制信息：

| Key | 类型 | 用途 |
| --- | --- | --- |
| `host_kind` | string | host 传输语义 |
| `nonlinear_kind` | string | `layernorm`/`softmax` 等子算子 |
| `randomize_experts` | bool | MoE gating 是否随机分配专家 |
| `selected_experts` | []int |（assembler 填充）最终选中的专家列表 |
| `weight_tag`/`tile_id`/`array_id` | string/int | CIM 权重定位 |

生成器负责在 stage 序列中显式插入 host transfer 与非线性拆分；assembler 仅按 `type+metadata` 拼装 Chiplet 指令。这样既能确保 host DMA/kv cache 统计被触发，也方便后续扩展其它算子。***
