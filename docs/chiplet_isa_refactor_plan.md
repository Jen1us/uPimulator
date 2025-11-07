# Chiplet ISA 重构与Transformer-MoE仿真增强开发计划

本文档作为后续多阶段开发的统一基线，梳理当前仿真框架、目标架构、实施步骤、涉及文件与验证方式。无论在本对话还是新会话中继续推进，都应依照本计划同步状态。

---

## 0. 背景与动机

- **现状**  
  - `chiplet/command.go` 中的 `CommandKind` 与 `chiplet_commands.json` 仅覆盖少量抽象指令（如 `pe_cmd_attention_head`、`rram_cmd_execute`、`xfer_cmd_schedule`），未区分底层算子/数据流。  
  - Orchestrator 将 Transformer-MoE 的复杂流程折叠到固定 DAG，缺乏对 KV cache、MoE gating、动态专家调度、LOAD/STORE 的建模。  
  - Digital 与 RRAM chiplet 的执行模型较粗：数字侧只有 GEMM/Elementwise Token Prep 等聚合操作；RRAM 侧三阶段命令与权重管理、功耗统计不完善。  
  - 互联仅区分 digital↔rram 的简单搬运，未考虑 2D mesh、hop 数、DMA 成本。  
- **目标**  
  - 定义符合“最小计算单元”原则的底层 ISA，覆盖 SPU/VPU 标量/向量操作、PE/ RRAm GEMM、LOAD/STORE、C2D/D2C 传输、Host 控制、MoE gating。  
  - 支持在线动态专家调度：gating 由 Digital 计算后返回 Host，Host 根据 top-k 结果增量生成指令。  
  - 建模 DRAM ↔ Chiplet 的交互（KV cache load/store、embedding、最终 logits）。  
  - 扩展统计 / 日志输出，体现能耗、传输时延、buffer 占用、mesh 交通。  

---

## 1. 当前架构速览（供参考）

| 模块 | 角色 | 关键文件 |
| --- | --- | --- |
| 指令定义 | `CommandKind` / `CommandDescriptor` | `src/simulator/chiplet/command.go` |
| Orchestrator | 解析命令、发射任务、资源限流、streaming | `src/simulator/chiplet/orchestrator.go` |
| 数字 Chiplet | PE 阵列、SPU、缓冲资源 | `src/simulator/chiplet/digital/...` |
| RRAM Chiplet | Tile/SA 控制器、能耗统计 | `src/simulator/chiplet/rram/...` |
| 互联与仿真平台 | `ChipletPlatform` 管理时钟域、任务队列、传输 | `src/simulator/chiplet_platform.go` |
| 结果与日志 | `chiplet_results.csv`, `chiplet_log.txt`, `run_debug.log` | 仿真输出 |

---

## 2. 目标 ISA（概述）

- **Digital 指令族**  
  - `PE_GEMM`、`SPU_OP`、`VPU_OP`、`REDUCE_OP`、`BUFFER_ALLOC/RELEASE`、`PIPELINE_BARRIER`  
  - Softmax/LayerNorm 等归约通过 `REDUCE_OP` 指令描述，但执行资源复用 SPU/VPU（无独立 REDUCE 硬件单元）
- **RRAM 指令族**  
  - `CIM_WEIGHT_LOAD`  
  - `CIM_GEMM_STAGE` / `CIM_GEMM_EXECUTE` / `CIM_GEMM_POST`  
  - （可选）`CIM_IDLE` 或 `CIM_STATUS_QUERY`
- **互联/传输指令**  
  - `C2D_XFER`, `D2C_XFER`  
  - `HOST2D_LOAD`, `D2HOST_STORE`（KV cache、输入输出）  
  - 可扩展 `BROADCAST`, `REDUCE`, `D2HOST_NOTIFY`
- **Host 控制指令/伪指令**  
  - `HOST_EMBED_LOOKUP`, `HOST_ROUTER_PREP`, `HOST_PIPELINE_CTRL`, `HOST_LM_HEAD`, `HOST_SYNCHRONIZE`

详细字段（地址、tile/SA 坐标、hop 数、字节数、激活/权重大小、量化参数等）将在 Phase 1 完成定义。

---

## 3. 开发阶段与待办

### Phase 1：指令集与数据结构重塑

| 子任务 ID | Todo | 细节说明 | 主要文件/目录 | 验收标准 |
| --- | --- | --- | --- | --- |
| 1.1 | 扩展 `CommandKind` 枚举 | - 新增 Digital/ RRAm/ 互联/ Host 指令 opcode（PE_GEMM, SPU_OP, …）<br>- 为每个 opcode 定义字符串映射与 `CommandKindFromOpcode` | `src/simulator/chiplet/command.go` | go build 通过；`CommandKind.String()` 输出正确 |
| 1.2 | 扩展 `CommandDescriptor` 字段 | - 新增 mesh 坐标、KV cache 地址、buffer 句柄、操作子类型等字段<br>- 保持 JSON 兼容（旧字段默认值） | 同上 | 新字段有注释；旧 JSON 可解析 |
| 1.3 | 升级 `Task` 结构 | - `Task` 新增 opcode、执行域、payload map、host/dma flag<br>- 更新 `TaskTarget` 说明 | `src/simulator/chiplet/tasks.go` | 现有 orchestrator 代码编译通过 |
| 1.4 | JSON 解析适配 | - 调整读取 `chiplet_kernels.json`、`chiplet_commands.json` 的逻辑，忽略未知字段<br>- 提供最小示例命令文件验证 | `misc`、`tools` (如有) | `go test ./...` 解析测试通过 |
| 1.5 | 指令文档/样例 | - 新增 `docs/isa_spec.md`（或在本文最后附录）列出各指令字段<br>- 提供示例 JSON 片段 | `docs/isa_spec.md` | 文档经代码审查；后续阶段引用 |

**阶段出口**：指令/数据结构完成，旧命令仍可运行，新字段可被读取。

### Phase 2：Orchestrator 基础设施升级

| 子任务 ID | Todo | 细节说明 | 主要文件/目录 | 验收标准 |
| --- | --- | --- | --- | --- |
| ✅ 2.1 | opcode → Task 生成表 | - 引入 `handler` map 或 switch，根据 opcode 构造 Task<br>- 支持 HOST/DMA target | `src/simulator/chiplet/orchestrator.go` | 新指令可生成 Task（可加 mock 测试） |
| ☐ 2.2 | 资源限流拆分 | - 抽象 `OutstandingTracker`，分别跟踪 Digital/RRAM/互联/DMA 资源<br>- `NotifyTaskCompletion`/`canIssueNode` 统一增减，并将统计输出到 `chiplet_log.txt`/`chiplet_results.csv` | `src/simulator/chiplet/orchestrator.go`, `chiplet_platform.go`, `misc/stat_factory.go` | 日志新增字段并验证计数随任务增减 |
| ✅ 2.3 | 动态 DAG 插入 | - ReadyQueue 支持 runtime append；提供 `AppendCommandGroup` 将新节点插入 DAG<br>- 单测覆盖“执行完 gating → 追加专家流水” | `src/simulator/chiplet/orchestrator.go`, `src/simulator/chiplet/tasks.go` | 测试：完成 gating 节点后新节点进入 readyQueue 并被调度 |
| ✅ 2.4 | Gating 回传通道 | - 基于 `HostEvent` 解析 gating buffer，生成专家任务（D2C→CIM→C2D→VPU）<br>- 先以 mock 专家 ID 验证流程，后续再接真实数据 | `chiplet_orchestrator.go`, `chiplet_platform.go`, `src/simulator/host` | mock 流程可运行，专家任务被动态追加 |
| ✅ 2.5 | Streaming 调整 | - 在新指令体系下重新定义高/低水位策略，可暂时关闭 streaming<br>- 重新启用后通过简单 workload 验证 | `chiplet/orchestrator.go` | streaming=off 时运行不崩溃；重新启用后可跑样例 |

**阶段出口**：Orchestrator 能识别新指令并分配资源；关键路径有单测。

### Phase 3：Digital Chiplet 微架构重构

| 子任务 ID | Todo | 细节说明 | 主要文件/目录 | 验收标准 |
| --- | --- | --- | --- | --- |
| ✅ 3.1 | 指令解析与队列 | - `SubmitDescriptor` 根据 opcode 选择 PE/SPU/VPU/REDUCE pipeline（REDUCE 复用 SPU/VPU 资源）<br>- 各 pipeline 维护独立待执行队列 | `digital/chiplet.go` | 单测：不同指令入队正确 |
| ✅ 3.2 | PE_GEMM 执行模型扩展 | - 保留 computeCluster，但允许配置 tile 大小、并发数<br>- 统计每周期 active PE | `digital/chiplet.go`, `digital/pe.go` | `chiplet_phase` 测试通过 |
| 3.3 | SPU/VPU/REDUCE 模块 | - 新增 SPU/VPU 模拟类（吞吐、延迟、能耗）<br>- `REDUCE_OP` 指令复用 SPU/VPU 吞吐实现 sum/max/argmax | `digital/spu.go`, `digital/chiplet.go` | 单测：SPU/VPU/REDUCE 指令计数正确 |
| 3.4 | Buffer 管理 API | - 实现 `AllocBuffer(name, bytes)` / `FreeBuffer` / 查询占用<br>- 提供 KV cache/传输调用接口，记录实时/峰值 buffer 占用并写入日志 | `digital/buffer.go` | 日志有 buffer 占用与峰值 |
| 3.5 | 能耗与日志 | - PE、SPU、VPU、REDUCE（映射到 SPU/VPU）分别统计能耗并计入 `chiplet_log`/`chiplet_results`<br>- 为后续 Phase 5 的带宽/能耗分析提供基础数据 | `digital/chiplet.go`, `chiplet_platform.go` | 仿真输出看到新增能耗统计 |

**阶段出口**：数字 chiplet 能依据新 ISA 正常运行，统计完备。

### Phase 4：RRAM 三阶段与权重管理强化

| 子任务 ID | Todo | 细节说明 | 主要文件/目录 | 验收标准 |
| --- | --- | --- | --- | --- |
| ✅ 4.1 | 权重映射表 | - 维护 `(chiplet,tile,SA)` → 权重 chunk 信息，暴露命中查询 API<br>- 支持懒加载 / 逐层卸载，记录峰值存储 | `rram/chiplet.go`, `rram/controller.go`, `chiplet_platform.go` | 单测：load 后查到映射，重复 load 命中 |
| ✅ 4.2 | `CIM_WEIGHT_LOAD` 执行 | - 引入专用 weight load 指令，模拟 DMA latency/能耗并统计命中率<br>- 更新 Stage/Execute/Post 元数据共享同一 weight tag | `rram/chiplet.go`, `rram/controller.go`, `chiplet_platform.go`, `chiplet/operators/library.go` | 日志记录 load 次数/字节、命中率 |
| ✅ 4.3 | Stage/Execute/Post 队列 | - `Tile`/`SenseArray` 维护 stage/execute/post 独立待执行队列，`TaskSpec.Phase` 控制只消耗对应周期<br>- Stats 仅在 Execute 计入脉冲/ADC，Stage/Post 记录各自周期并在 Post 阶段输出 summary | `rram/chiplet.go`, `rram/tile.go`, `chiplet_platform.go` | `run_debug.log` 可看到 Stage→Execute→Post 依次调度，`chiplet_log` 统计 Stage/Execute/Post 的独立周期 |
| ✅ 4.4 | 能耗模型扩展 | - 分别累计 Stage/Execute/Post 动态能耗与权重加载能耗；写入 `chiplet_log` 聚合字段 | `rram/chiplet.go`, `chiplet_platform.go` | `chiplet_log.txt`/`chiplet_results.csv` 出现 stage/execute/post/weight 能耗统计 |
| ✅ 4.5 | 状态查询接口 | - Chiplet 暴露 `IsReady`、`BufferPeak` 与当前/峰值缓冲占用；平台日志输出 input/output 峰值字节 | `rram/chiplet.go`, `chiplet_platform.go` | Host 可读取缓冲峰值/占用，`chiplet_log.txt` 展示对应字段 |

**阶段出口**：RRAM 能完整执行 stage/execute/post + load，能耗/状态准确。

### Phase 5：互联 + Host DMA & KV Cache

| 子任务 ID | Todo | 细节说明 | 主要文件/目录 | 验收标准 |
| --- | --- | --- | --- | --- |
| ✅ 5.1 | Mesh 拓扑抽象 | - 在 topology/config 中为每个 chiplet 分配 (x,y)<br>- 提供 hop 计算函数 | `chiplet/topology.go`, `chiplet_platform.go` | 单测验证 hop 计算 |
| ✅ 5.2 | C2D/D2C 执行模型 | - `handleTransferTask` 根据指令类型移动 buffer<br>- 延迟/功耗基于 hop 和字节 | `chiplet_platform.go` | `run_debug.log` 打印 hop/latency |
| ✅ 5.3 | Host DMA 模块 | - 新建 `host/dma.go`，统一管理 DRAM 带宽<br>- 处理 `HOST2D_LOAD` / `D2HOST_STORE` | `src/simulator/host/` | KV cache load/store 可统计 |
| ✅ 5.4 | KV Cache 管理 | - 管理 per-layer KV cache 元数据、命中率<br>- 提供 API 供 orchestrator 使用 | `host/cache.go` | 仿真运行时 KV 状态正确 |
| 5.5 | 传输日志与限流 | - StatFactory 记录每种传输字节数/延迟<br>- Mesh 带宽不足时触发限流事件 | `chiplet_platform.go`, `misc/stat_factory.go` | 日志出现传输统计及限流计数 |

> **近期进展**：
- 新增脚本 `tools/transfer_debug_analyzer.py`/`tools/cycle_log_summarizer.py`，用于快速校验传输路径与周期统计；KV cache 接入后 `chiplet_log` 输出 load/store/hit/miss/evict 聚合字段，并在 cycle log 增加 `kv_*` 列。
`handleTransferTask` 已接入 Host↔Digital DMA 阶段，配套统计的 `host_dma_load_bytes`/`host_dma_store_bytes` 列同步写入 cycle log 与 StatFactory，并在失败时按缓冲调整回滚。C2D/D2C 路径现按 hop 权重记录能耗并将 `src/dst` 端点写入 `[chiplet-debug] transfer` 日志；`phase5_smoke_test` 校验列头已更新，确保单测覆盖 host DMA 字段。

**阶段出口**：互联/DRAM 交互完整可用，KV cache load/store 得以模拟。

### Phase 6：MoE 动态调度与流水整合

| 子任务 ID | Todo | 细节说明 | 主要文件/目录 | 验收标准 |
| --- | --- | --- | --- | --- |
| ✅ 6.1 | Gating 指令实现 | - Digital 通过 SPU/VPU + REDUCE 指令计算 gating（归约复用 SPU/VPU 资源），输出到缓冲 | `digital/chiplet.go`, `chiplet_platform.go` | `moe_gating_scores`/`topk_select` 经过 SPU/REDUCE 执行并写入缓冲 |
| 6.2 | Host Gating Fetch | - 专门指令/回调读取 gating buffer，解析 top-k | `orchestrator.go`, `host/` | ◻️ 回调与 Host 执行流待实现 |
| ✅ 6.3 | 动态专家指令生成 | - 根据 top-k 生成 (D2C→CIM→C2D→VPU) pipeline<br>- 维护 per-token 状态直到专家都完成 | `orchestrator.go`, `chiplet_platform.go` | 仿真日志显示动态插入指令 |
| ✅ 6.4 | Pipeline Barrier | - 实现 barrier/依赖，确保专家输出齐备后 Residual/LN 执行 | `chiplet_platform.go`, `host/` | 流程无死锁；日志可见 barrier 事件 |
| ✅ 6.5 | MoE 统计 | - 记录命中率、专家延迟、失败次数等 | `chiplet_platform.go`, `misc/stat_factory.go` | `chiplet_results.csv` 添加 MoE 指标 |

**阶段出口**：MoE top-2 流程可运行，支持动态调度和统计。

> **近期进展**：数字芯粒已支持 `moe_gating_scores` + `topk_select` 指令，SPU/REDUCE 阶段可按 metadata 统计算力与缓冲；后续 6.2 将接入 Host 侧读取并生成专家任务。

### Phase 7：命令生成链路 & 数据驱动仿真

| 子任务 ID | Todo | 细节说明 | 主要文件/目录 | 验收标准 |
| --- | --- | --- | --- | --- |
| ✅ 7.1 | Kernel 模板生成 | - 更新构建脚本输出新的 kernel 列表（含指令字段默认值） | `compiler/compiler.go`, `bin/chiplet_kernels.json` | 样例 kernel 可被解析 |
| ✅ 7.2 | 指令流水构建器 | - `chiplet_model.go` builder 支持 Host↔Digital/RRAM 传输、Stage metadata/stage deps；`CommandDescriptor` 自动带 mesh/buffer 字段 | `assembler/chiplet_model.go`, `tools/generate_chiplet_model.py` | 生成的 `chiplet_commands.json` 包含 host2d/d2host/metadata，Orchestrator 可解析 |
| ✅ 7.3 | 模型样例 | - 更新 `demo*`/`qwen*`/`test`/`pytorch_demo` JSON，默认含 host load/store & chunk metadata；文档列出示例和生成命令 | `bin/`, `golang/docs/chiplet_architecture.md` | 样例可直接驱动 Chiplet 平台，文档说明生成/使用步骤 |
| 7.4 | Streaming/Batch 支持 | - 重启 streaming 模式，适配新指令 | `orchestrator.go` | streaming 运行样例 |

**阶段出口**：指令生成链路可用，能驱动真实模型。

### Phase 8：测试、验证与性能分析

| 子任务 ID | Todo | 细节说明 | 主要文件/目录 |
| --- | --- | --- | --- |
| ✅ 8.1 | 单元测试扩展 | - 新增 `orchestrator_stream_test.go` 验证 streaming 批次 metadata 深拷贝、DMA 限额 | `src/simulator/chiplet/orchestrator_stream_test.go` |
| ✅ 8.2 | 集成/回归脚本 | - 新增 `tools/chiplet_regression.py` 校验 cycle log、run_debug、results；可选要求传输/stream/host DMA | `tools/chiplet_regression.py` |
| 8.3 | 日志/可视化更新 | - 更新 `log_dump.json` schema、`visualize.py` 支持新指标 | `golang/uPIMulator/log_dump.json`, `script/visualize.py` |
| ✅ 8.4 | 性能功耗分析 | - 新增 `tools/chiplet_perf_analyzer.py` & `docs/perf_analysis.md`，提取 Mesh 带宽/HostDMA/KV/能耗摘要 | `tools/chiplet_perf_analyzer.py`, `docs/perf_analysis.md` |
| ✅ 8.5 | 文档收尾 | - 更新《Chiplet Architecture Overview》运行/分析流程、Perf 指南及工具 README；同步本计划状态 | `golang/docs/chiplet_architecture.md`, `docs/perf_analysis.md`, `golang/uPIMulator/tools/README.md` |

**阶段出口**：测试体系完整、文档齐备，具备交付质量。

---

## 4. 时间与依赖建议

- Phase 1–2：可并行准备（指令定义 vs orchestrator 架构），建议先完成 Phase 1 后立即开始 Phase 2。  
- Phase 3 与 Phase 4 彼此独立但都依赖 Phase 2。  
- Phase 5 需要 Phase 2 基础；KV cache/Host DMA 的逻辑也会在 Phase 6 使用。  
- Phase 6 依赖 Phase 3/4/5 的功能。  
- Phase 7 需在核心 ISA 落定后进行；Phase 8 最后执行。  

---

## 5. 代码与文档改动提示

- **核心源码目录**：`src/simulator/chiplet`, `src/simulator/chiplet_platform.go`, `src/simulator/host`, `src/misc`（如需更新配置解析）。  
- **配置/数据**：`bin/chiplet_kernels.json`, `bin/chiplet_commands.json`, `bin/options.txt`；若生成脚本位于 `tools/`，需同步调整。  
- **文档**：建议在 `docs/` 新增子文档如 `docs/isa_spec.md`, `docs/moe_pipeline.md`，描述指令字段与执行顺序，方便新会话继续。  
- **测试**：扩展 `chiplet_phase*_test.go`、新增 host DMA/MoE 路由单元测试。  

---

## 6. 日志与验证要点

实施每一阶段后，应关注以下输出/统计变化：

- `run_debug.log`：新的 `[chiplet-debug]` 行是否携带 opcode、地址、bytes 等信息。  
- `chiplet_log.txt` / `chiplet_results.csv`：新增指令计数、能耗、buffer 占用、mesh hop 等统计项是否正确。  
- `cycle_dump.json`：需要扩展字段以记录 KV cache 命中率、专家分布、传输延迟。  
- Host 相关日志：确认 gating 结果回传、指令动态插入流程无死锁。  

---

## 7. 后续协作注意事项

- 每完成一个 Phase，更新本文档（或在新文件补充“进度/更改”），以记录剩余任务。  
- 若在新会话继续推进，应在开头明确当前所在 Phase、未完成任务，以及编译/测试状态。  
- 对于指令字段的变更，请同步更新所有生成脚本和文档，避免不同阶段命令格式不一致。  
- 如果需要在阶段间引入临时桥接（例如旧 ISA 与新 ISA 的兼容层），需在文档中标注，以防后续遗漏清理。

---

> **提示**：本计划默认以 Transformer-MoE 为主线，同样适用于其他需要 RRAM + 数字协同的模型。必要时可在 Phase 7 增加“多模型配置支持”的子任务。
