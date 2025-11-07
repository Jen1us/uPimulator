# uPIMulator 扩展开发手册

> 本文档在原始《uPIMulator (Go) 开发手册》的基础上，新增异构 Chiplet 架构的整体改造规划。第一部分完整保留现有内容，第二部分给出新的架构说明、任务分解与阶段性交付计划，后续开发可按章节推进并在此逐段对照完成。

---

## Part I —— 原始 Go 开发手册（原文备份）

# uPIMulator (Go) 开发手册

本文档为 **Go 版本** 的 uPIMulator 提供开发基准。除非特别说明，其余实现（`python_cpp/`、`golang_vm/`）均不再维护。

## 1. 作用域与目标
- 维护与扩展 Go 版 `golang/uPIMulator` 的功能、性能与可移植性。
- 支撑 HPCA 2024 论文复现、PrIM 基准仿真以及后续架构探索。
- 提供引入新硬件模型（例如 RRAM CIM）和新基准的参考流程。

## 2. 目录结构

```
golang/
├── README.md             # 用户使用说明（保留）
├── DEVELOPMENT.md        # 本开发手册
└── uPIMulator/
    ├── benchmark/        # PrIM 基准数据与构建脚本
    ├── bin/              # 仿真输出（构建/运行时生成）
    ├── build/            # go build 产物
    ├── docker/           # 编译器/SDK Dockerfile 与资源
    ├── script/           # 构建、验证、可视化脚本
    ├── sdk/              # 运行时库源码
    └── src/              # Go 源码
        ├── assembler/    # 数据准备
        ├── compiler/     # Docker 编译入口
        ├── linker/       # 汇编解析与链接
        ├── simulator/    # Host & DPU 模拟
        ├── misc/         # 工具类（配置、CLI、IO、统计）
        └── main.go       # 入口
```

> **约定**：新增文件应位于 `golang/uPIMulator/` 内；不要在仓库根目录或其它版本目录放置 Go 版专用代码。

## 3. 构建与运行

### 3.1 构建
```bash
cd golang/uPIMulator/script
python build.py
```
- 使用本地 Go 1.21.5+ 环境。
- 脚本会清理并重建 `build/` 目录，生成 `build/uPIMulator`。

### 3.2 运行示例
```bash
cd golang/uPIMulator
rm -rf bin && mkdir bin
./build/uPIMulator \
  --root_dirpath <绝对路径>/golang/uPIMulator \
  --bin_dirpath  <绝对路径>/golang/uPIMulator/bin \
  --benchmark VA \
  --num_channels 1 \
  --num_ranks_per_channel 1 \
  --num_dpus_per_rank 1 \
  --num_tasklets 16 \
  --data_prep_params 1024
```
- 所有路径需使用绝对路径。
- 首次运行会触发 Docker 编译镜像并构建基准二进制，耗时较长。

### 3.3 常用脚本
- `script/run_validation.sh`：批量运行预设配置。
- `script/visualize.py`：对日志进行可视化分析（需 Python 环境）。

## 4. 仿真流水线概览

1. **命令行解析**（`misc/command_line_parser.go`）  
   读取 CLI 选项并写入 `args.txt`/`options.txt`。

2. **配置校验**（`misc/command_line_validator.go` 与 `misc/config_validator.go`）  
   确保参数与平台常量合法。

3. **编译**（`compiler/compiler.go`）  
   - 通过 Docker 构建 SDK (`sdk/build.py`) 和基准 (`benchmark/build.py`)。
   - 基于 `--num_dpus`、`--num_tasklets` 生成目标文件。

4. **链接**（`linker/linker.go`）  
   - 多线程解析基准与 SDK 的 relocatable 对象。
   - 解析链接脚本，输出 `bin/main.S`。

5. **数据准备**（`assembler/assembler.go`）  
   - 依照 benchmark-specific 逻辑生成 MRAM/WRAM 输入输出镜像。
   - 写入 `bin/` 目录（如 `input_*`, `output_*`, `num_executions.txt`）。

6. **仿真**（`simulator/simulator.go`）  
   - Host 与 Channel/DPU 拓扑建立。
   - 多线程执行逻辑（`core/thread_pool.go`）调度 DPU 周期。
   - 仿真结束后生成 `bin/log.txt` 统计文件。

## 5. 源码模块说明

### 5.1 `misc/`
- `CommandLineParser` / `CommandLineValidator`：统一 CLI 接口，扩展参数从此入手。
- `ConfigLoader`：集中存放硬件常量（地址宽度、MRAM 大小等）。
- `StatFactory`：用于统计指标累积，新增统计点时先调用 `Init` 设置名字，再 `Increment`/`Set`。

### 5.2 `compiler/`
- `compiler.Compiler`：封装 Docker 构建流程。  
  修改编译链（如更换 Docker 镜像、添加额外脚本）需在此扩展。

### 5.3 `linker/`
- `LexJob`/`ParseJob`/`AnalyzeLivenessJob`：通过 `core.ThreadPool` 并行处理汇编解析。
- `linker.Linker`：负责 symbol 解析、链接脚本执行与最终汇编输出。

### 5.4 `assembler/`
- 每个 benchmark 对应 `prim/*.go` 文件，负责根据 `--data_prep_params`、`--num_tasklets` 等参数生成输入输出数据。
- 新增 benchmark 时：
  1. 复制并修改一份 `prim` 模板。
  2. 在 `assembler.go` `Init` 中注册。
  3. 更新 `benchmark/` 下的源代码与 `benchmark/build.py`。

### 5.5 `simulator/`
- `host/`：负责执行流程调度、DMA 任务生成与 DPU 启动。
- `channel/`、`rank/`：抽象 PIM 内存拓扑，管理 DPU 集合。
- `dpu/`：
  - `logic/`：线程调度器、流水线、ALU、DMA 模块；执行指令语义。
  - `sram/`：IRAM/WRAM/Atomic 模块。
  - `dram/`：MRAM 控制器、行缓冲与调度。
- `CycleJob` 等利用 `core.ThreadPool` 实现并发仿真。

## 6. 开发流程

1. **需求分析**  
   - 明确是否涉及 CLI 参数、benchmark 数据、仿真器核心或统计输出。
2. **接口设计**  
   - 调整 `CommandLineParser`、`ConfigLoader` 等公共入口。
   - 新增
[... omitted 0 of 200 lines ...]

改造清单

> 以下步骤以 Go 版本代码为准，所有路径均相对于 `golang/uPIMulator/`。

### 8.0 RRAM CIM 结构与建模假设
- **阵列规模**：单 tile 128×128 交叉阵列，默认 MLC cell（多电导态）以 2bit 精度初始化，可通过 `--rram_cell_precision` 调整（支持 1/2/4bit 等）。  
- **典型外围模块**：  
  1. *Wordline/Bitline Drivers* —— DAC 将输入向量电压注入 wordline，bitline 上采样电流。  
  2. *Selector / Access Device* —— 抑制 sneak current（1T1R、1S1R 等）。  
  3. *Sense Amp / ADC* —— 将 bitline 电流积分并量化为数字结果；ADC 精度受 `rram_cell_precision` 影响。  
  4. *Charge Integrator* —— CIM MAC 期间累积 128 个单元电流并输出。  
  5. *Program Engine* —— 控制 `rram_program_pulses` 次 SET/RESET 脉冲，延迟由 `rram_write_latency`、`rram_read_latency` 指定。  
- **CIM 指令语义**：一次 RRAM CIM 指令会在单个逻辑周期内，将一列 128 个输入（由阵列左侧 DAC 提供）同时注入，完成整列权重的向量-矩阵乘累加。执行延迟由流水线/周期规则决定，读出结果写入寄存器或 WRAM。  
- **建模要求**：以上模块需在 `rram/` 子目录中抽象为阵列、控制器、定时逻辑，统计项至少覆盖脉冲次数、读写延迟、CIM 指令吞吐。
- **统一地址空间**：当 `memory_type=rram` 时，所有原 MRAM 相关的 CLI 参数、二进制镜像与地址段均映射到 RRAM；MRAM 控制器不会被实例化，WRAM 依旧作为激活/结果缓冲区（host 与 RRAM 之间的 staging 缓冲）。

### 8.1 命令行与配置体系
- `src/main.go`  
  - 在 `InitCommandLineParser()` 中添加 `--memory_type`（值如 `mram`/`rram`）以及 RRAM 独有的时序、吞吐、能耗参数（如 `--rram_read_latency`、`--rram_pulse_num` 等）。  
  - 主流程在写入 `args.txt`/`options.txt` 前无需改动；仿真开始前需根据 `memory_type` 将参数传递到后续模块（可通过 `CommandLineParser` 读取）。
- `src/misc/command_line_validator.go`  
  - 校验 `memory_type` 取值合法，新增参数需与 DPU 个数、tasklet 等组合检查（例如禁止在 `rram` 模式下使用未实现的 verbose level）。  
  - 对脉冲数、延迟等新参数添加取值范围断言。
- `src/misc/config_loader.go` / `config_validator.go`  
  - 扩展结构：新增 `RramDataWidth()`、`RramOffset()`、`RramSize()`、`RramTiming()` 等 getter。  
  - 在 `Validate()` 中加入 RRAM 与其他内存区域的 overlap 检查、尺寸/带宽有效性校验。  
  - 若 RRAM 共用地址空间，需要更新 stack/heap 分配策略。
- `src/misc/stat_factory.go`（如需新增统计 key）  
  - 扩充默认计数器映射，例如预定义 `rram_pulse_count`、`rram_write_latency`。

### 8.2 DPU 结构与内存模型
- 新建目录 `src/simulator/dpu/rram/`，至少包含：
  - `memory_controller.go`：仿照 `dram/memory_controller.go` 实现命令队列、行缓冲/Bitline 脉冲调度。  
  - `array.go` / `cell.go`：描述 RRAM 阵列存取、脉冲写入、模拟误差累积（如导入噪声模型）。  
  - `timing.go`：集中处理脉冲持续时间、reset/set 延迟、写退火等参数。
- `src/simulator/dpu/dpu.go`  
  - 在 `Init()` 内根据 `memory_type` 分支实例化 MRAM (`dram`) 或 RRAM 控制器；相应地连接到 `MemoryController` / `RowBuffer` / `MemoryScheduler`。  
  - 若 RRAM 与 MRAM 共存，需要在 DPU 结构体新增指针成员并在 `Fini()` 释放。
- `src/simulator/dpu/dram/memory_controller.go` 等现有文件  
  - 抽象出通用接口（例如定义 `MemoryController` 接口），让 DPU 可以统一调度。  
  - 或在 RRAM 控制器里复用 DRAM 队列结构，但要小心字线粒度和最小访问单位的差异。

### 8.3 指令集与流水线支持
- `src/linker/kernel/instruction` 目录
  - 新增 `RRAM_LOAD_COL`、`RRAM_CIM_MAC` 两个 opcode，并在 lexer/parser/instruction assigner 中注册关键字，使汇编器识别。
  - `RRAM_LOAD_COL rc, ra, rb`：把 WRAM 中 128×FP16 激活向量加载到 RRAM 激活缓冲；`RRAM_CIM_MAC rc, ra, rb`：触发阵列整列 MAC，输出 FP16 写回 WRAM `ra`。
- `src/simulator/dpu/logic/logic.go`
  - 在 `Execute*` 系列函数中为新指令添加 case，调度到 RRAM 控制器。  
  - 根据脉冲数/阵列宽度设置流水线阻塞（例如通过 `cycle_rule` 增加新的 hazard 类型）。
- `src/simulator/dpu/logic/alu.go`
  - 若 RRAM CIM 需要新的算术功能（如模拟权重误差、读出积分），在此实现实际数据计算或调用 RRAM 模块接口。
- `src/simulator/dpu/logic/pipeline.go` / `cycle_rule.go`
  - 新增 pipeline stage 或延迟槽来模拟 CIM 操作持续时间。  
  - 在 revolver 调度、资源冲突判定中考虑 RRAM 指令占用（例如禁止同周期多次触发列选通）。
- `src/simulator/dpu/logic/thread_scheduler.go`
  - 若 RRAM 操作需要所有 tasklet 同步，可在此增加 barrier/flag 控制。

### 8.4 DMA、数据准备与基准
- `src/simulator/host/dma_transfer_to_mram_job.go` 等 DMA 文件  
  - 复制或重构为 `dma_transfer_to_rram_job.go`，按 RRAM 的字节粒度和对齐要求处理 Host↔RRAM 数据交换。  
  - 在 `host/host.go` 中根据 `memory_type` 选择合适的 DMA Job 队列。
- `src/assembler/assembler.go` 与 `src/assembler/prim/*.go`  
  - 针对 RRAM 基准新增 `Assemblable` 实现：在 `Init()` 注册，比如 `this.assemblables["RRAM_GEMV"] = new(prim.RramGemv)`。  
  - 数据准备需要考虑权重矩阵、脉冲编码（binary/ternary）、需要时可在 `encoding/` 下增加新转换器。
- `benchmark/` 目录  
  - 添加 RRAM 基准源代码与 `CMakeLists.txt`。若复用已有 bench，可通过编译选项区分。  
  - 更新 `benchmark/build.py`，在解析 CLI 参数后对 RRAM 模式做特化（例如启用不同的 SDK 库）。
- `sdk/`  
  - 若 RRAM 指令需要新的 runtime 支持（如 host API 调用、库函数），在 `sdk/` 下补充源文件，并在 `sdk/build.py` 注册编译目标。

### 8.5 统计、日志与可视化
- `src/simulator/simulator.go` + 各 `StatFactory` 使用点  
  - 给 RRAM 控制器/逻辑注入统计，如 `rram_pulse_total`、`rram_read_latency_cycle` 等；在 `Dump()` 时写入 `log.txt`。  
  - 如需区分数据路径，在 `log.txt` 中新增 section 名称，确保 `visualize.py` 能识别。
- `tools/upmem_profiler`  
  - 在解析器中加入新字段解析逻辑，并扩展图表脚本（例如生成 CIM 能耗对比）。
- `tools/upmem_reg_model`（如需）  
  - 若 RRAM 影响主机↔DPU 通信模型，应更新回归模型输入列。

### 8.6 测试与验证
- 为 RRAM 增加 smoke test：
  - 在 `script/run_validation.sh` 中添加 RRAM 配置，确保构建/仿真不回归。  
  - 可在仓库根或 `script/` 下准备最小权重矩阵案例，验证脉冲统计与结果正确性。
- 建议编写单元测试（Go `testing`）：
  - `rram/memory_controller_test.go`：覆盖脉冲数、延迟。  
  - `logic/rram_instruction_test.go`：验证指令执行正确。

### 8.7 文档同步
- 更新 `README.md`（用户向）和本 `DEVELOPMENT.md` 的相关章节，说明新的 CLI 参数与必备依赖。
- 若提供论文或报告，记录 RRAM 模型假设、误差来源和验证方法。

完成以上步骤后，RRAM CIM 方可在仿真流程中与 MRAM 配置并行存在或替代，并具备完整的数据准备、指令支持与日志分析能力。

## 9. 调试与排错

- **编译失败**：检查 Docker 是否安装；若镜像缺失，可重新 `docker build` 并确认权限。
- **仿真崩溃（panic）**：定位到 panic 消息与堆栈；常见原因是 CLI 参数超出范围或数据准备未覆盖所有 DPU/tasklet。
- **日志缺失**：确认 `bin_dirpath` 目录存在且可写，检查 `FileDumper` 写入是否被异常提前终止。
- **性能下降**：查看 `log.txt` 内线程调度与 MemoryController 的统计，确定瓶颈（revolver、DMA、RF backpressure 等）。

## 10. 后续工作清单

- [ ] 统一 `script/` 内构建、验证脚本的 CLI 参数与 `main.go` 一致性。
- [ ] 为关键模块补充自动化测试样例（可使用 Go `testing` 或最小 benchmark）。
- [ ] 整理 `log.txt` 字段说明，编写 Markdown/Excel 对应表格。
- [ ] 在 RRAM CIM 合入后，添加对能耗/误差建模的可视化示例。

---

维护者更新此文档时，请确保与最新代码保持一致。所有 Go 版的新需求、约定与限制应先在此文档说明，再进入开发流程。

---

## Part II —— 异构 Chiplet 架构扩展规划

> 本部分针对“Host CPU + 多数字 Chiplet + RRAM CIM Chiplet”架构，给出从建模到验证的完整任务清单。建议按阶段推进，每个阶段结束前更新本文件中的对勾项并同步代码与脚本。

### 1. 顶层目标与约束
- 支持 Host CPU 完成 tokenizer、embedding 和片间调度，提供对 transformer/MoE 级工作负载的端到端仿真能力。
- 建模 **4× Digital Chiplet**（每个含 4× PE + 4× SPU + 片内 Buffer）与 **8× RRAM CIM Chiplet**（每个含 16×16 Tile，Tile 内含 16×16 SA）。
- 提供跨 chiplet 的统一通信/同步接口，区分逻辑与模拟频率域。
- 兼容现有 CLI、编译链与统计框架，允许用户选择传统 UPMEM 模式或新 Chiplet 模式。

### 2. 开发阶段划分
1. **Phase 0：基础设施准备**
   - [ ] 梳理现有代码中硬编码的“channel/rank/dpu”假设，准备抽象层。
   - [x] 在 `misc/` 内增设全局模式枚举（UPMEM, CHIPLET）及配置同步。
2. **Phase 1：配置与命令行扩展**
   - [x] 为 Chiplet 模式新增 CLI 参数（chiplet 数量、PE/SA 规格、缓存容量、时钟）。
   - [x] 扩充 `ConfigLoader`/`ConfigValidator`，增加 chiplet 拓扑、互联带宽、tokenizer 资源等字段。
   - [x] 输出 `bin/options.txt` 时保留新参数，便于回溯（沿用现有 `StringifyOptions` 流程自动完成）。
3. **Phase 2：Host 调度层重构**
   - [x] 在 `simulator` 根目录新增 `platform` 抽象，根据模式实例化 UPMEM 平台或 Chiplet 平台。
   - [ ] 扩展 Host 以支持：
     - tokenizer/embedding 前处理流水线；
     - 批处理任务图管理（算子 DAG → 片上任务）；
     - 片间通信（数字 ↔ RRAM）事件表。
   - [x] 定义新的 `ChipletScheduler`，负责多 Chiplet 资源分配与同步屏障。（当前提供 `BasicScheduler`：FIFO 队列 + 任务抽象，待后续加入依赖/资源模型。）
   - [x] 在 `chiplet` 包中建立拓扑信息结构，供调度器与后续模块复用。
   - [x] 引入 `Task`/`TaskQueue` 抽象，平台通过 `SubmitTask` 将任务传递至 `HostTaskStager`，再由调度器统一接管；后续 Host 重构将基于此接口驱动执行。
   - [x] 初步提供 `HostOrchestrator` 占位实现（首次循环产生数字/RRAM bootstrap task），后续可替换为 tokenizer/算子 DAG 驱动的真实逻辑。
   - [x] `BasicScheduler` 支持 `TaskExecutor` 回调，`ChipletPlatform` 作为执行端区分 Digital/RRAM 任务并记录 placeholder 计数。
   - [x] `Dump()` 输出 `bin/chiplet_log.txt`，记录平台/Chiplet 层任务执行计数（后续可扩展更多统计）。
   - [x] 数字/RRAM Chiplet 增加基础周期计数器，`Cycle()` 针对每个 Chiplet 递减 pending cycles 并在日志中记录。
   - [x] `SubmitTask`/调度循环在 Chiplet 忙碌时推迟任务，累积 `task_deferrals` 统计，避免资源尚未空闲即重复调度。
   - [x] 记录任务等待时间（`task_wait_cycles_total`、`task_wait_samples`、`max_wait_cycles`），为后续调度/映射优化提供依据。
   - [x] Host Orchestrator 引入任务队列与反馈节流机制，根据等待时长动态调整任务产出节奏；添加 placeholder OpGraph 拓扑。
   - [x] 统计并输出 Chiplet 吞吐量指标（每周期任务数、最大/平均 throughput、busy cycles 与利用率）。
   - [x] 新增按 Chiplet 维度的任务延迟/推迟计数，便于定位局部瓶颈。
   - [x] 生成 `chiplet_cycle_log.csv` 记录逐周期的任务量、推迟次数、平均等待和利用率，用于后续可视化分析。
   - [x] Orchestrator DAG 补充 transfer 节点，平台统计 transfer 吞吐与 deferral。
   - [x] 数字/RRAM Chiplet 维护 buffer 占用，transfer 任务基于 payload 调整源/目的 buffer，并累计 `total_transfer_bytes` / `cycleTransferBytes`（CSV 中记录 transfer_exec/transfer_bytes）。
   - [x] transfer 任务按 `transferBandwidthBytes` 估算延迟（默认 4KB/cycle，可由拓扑推导），汇总日志新增最大/平均 transfer throughput 与带宽；CLI 提供 `--chiplet_transfer_bw`，贯通 ConfigLoader/Orchestrator/Platform。
4. **Phase 3：数字 Chiplet 建模**
   - [x] 在 `simulator/chiplet/digital/` 下建立模块，包含 `chiplet.go`、`pe.go`、`spu.go`、`buffer.go`。
   - [x] 为 `Chiplet` 引入任务描述（Tile GEMM / Elementwise / Tokenize），接入 `ChipletPlatform` 调度。
   - [x] 实现 Buffer 容量/带宽约束与占用统计，支持 transfer 任务写回。
   - [x] PE/SPU 提供周期估算接口（tile systolic 模型、向量吞吐）并驱动任务阶段（load/compute/store/spu）。
   - [x] **PE（脉动阵列）细化**
     - [x] 使用 tile 级流水线推进，按阵列并行度拆分 wave，并记录 Busy 周期与剩余 tile。
     - [x] 依据 Problem/Tile 维度估算带宽需求，追踪 MAC 计数与阵列利用率统计。
     - [x] 与 Host 描述保持一致，落盘 `macs_total`、PE busy 序列等指标。
   - [x] **SPU 集群细化**
     - [x] 引入标量/向量吞吐及特殊函数延迟模型，按寄存器压力调整周期。
     - [x] 统计 scalar/vector/special micro-op 数量与集群 busy 周期，输出到日志。
     - [x] 支持 Host 通过 payload 配置指令强度，映射到 SPU 周期估算。
   - [x] **Buffer/互联提升**
     - [x] 将激活/权重/结果缓冲与 transfer 任务带宽统一建模，维护实时占用。
     - [x] 记录 tensor 尺寸、数据量并在任务描述中传递，保障容量校验。
     - [x] 日志增加 buffer 占用、SPU/PE 利用率及传输统计。
5. **Phase 4：RRAM CIM Chiplet 拓展**
   - [x] 拆分 `simulator/chiplet/rram/` 目录，补齐 `chiplet.go`、`tile.go`、`sense_array.go`、`controller.go`、`programmer.go`。
   - [x] Tile 级调度器：分配空闲 Tile/SA、维护任务队列与并行度上限。
   - [x] SenseArray 行为：以 FP16 组件驱动补码切片，按 Slice 周期推进并汇总 `ISum/PSum/ASum`。
   - [x] 脉冲/编程模型：依据 `slice_bits` 统计脉冲数量、ADC 采样与预/后处理周期。
   - [x] 统计体系：记录 `rram_pulse_count`、`rram_read_latency_total`、`rram_adc_invocations` 等指标并写入 `chiplet_log.txt`。
   - [x] 数字 ↔ RRAM 接口：Host payload 提供 FP16 组件、scale/zero_point、脉冲参数，完成激活/结果对接。
   - [x] 与 `ChipletPlatform` 协同：RRAM 任务执行结束后通知 orchestrator，更新 buffer occupancy 与 transfer 字节计数。
   - [x] Host 侧提供 FP16 组件，驱动 `Preprocessor.Prepare` 自动生成 `P_Sum/A_Sum` 与预处理脉冲参数。
   - [x] Postprocessor 结果与参考值的差异写入日志（含均值/最大误差），提供误差模型挂载点。
   - [x] 单元测试：在 `simulator/chiplet/rram` 下添加最小 Tile/SenseArray 行为测试，校验脉冲统计与结果范围。
6. **Phase 5：统一时间推进与统计**
   - [x] 设计多时钟域框架：为 Digital/RRAM/互联定义独立频率，扩展 `Simulator.Cycle` 以支持事件驱动推进。
   - [x] 将 Chiplet/DMA 统计纳入统一 `StatFactory`，对接 Phase4 新增的 RRAM 指标。
   - [x] 更新 `log.txt` / `chiplet_log.txt`，区分 Host、Digital、RRAM、互联系统的吞吐与误差信息。
   - [x] 准备 Smoke Test：Chiplet 模式下运行最小 GEMM+CIM+回写流程，验证多时钟域正确性。
7. **Phase 6：软件栈与指令扩展**
   - [x] 设计 Chiplet ISA/命令描述，扩展 `linker/kernel/instruction`；
     - 新增 `PE_CMD_GEMM/PE_CMD_ATTENTION_HEAD/PE_CMD_ELEMENTWISE/PE_CMD_TOKEN_PREP`、`RRAM_CMD_STAGE_ACT/RRAM_CMD_EXECUTE/RRAM_CMD_POST`、`XFER_CMD_SCHEDULE` 与 `CHIPLET_CMD_SYNC` 等专用 opcode，统一挂载在 `DMA_RRI` 后缀。
     - 在 `simulator/chiplet/command.go` 定义 `CommandKind` 与 `CommandDescriptor`，明确 `Target/ChipletID/PayloadAddr/PayloadBytes/Aux/Flags` 字段，后续由 Host 将其翻译为 `Task`。
     - `simulator/dpu/logic` 补充 Chiplet 命令占位分支，避免在 UPMEM 模式下误触发；`CommandKindFromOpCode` 为未来编译链解析提供映射入口。
   - [x] 更新 `compiler` 与 `assembler`，支持生成 Chiplet 任务（如 GEMM kernel、softmax kernel）；
     - `assembler.AssembleChipletCommands()` 在 Chiplet 模式下生成示例任务序列 (`bin/chiplet_commands.json`)，含数字/传输/RRAM 指令依赖链。
     - `compiler.EmitChipletKernelManifest()` 输出 `chiplet_kernels.json`，记录可用 Chiplet opcode 与内核标签，供后续编译链扩展。
     - 现可通过 CLI 选项 `--chiplet_model_path` 指定 JSON 形式的 Transformer/MoE 模型描述，`assembler` 会按需求映射线性层到 RRAM、其余算子到数字 Chiplet 并输出命令序列。
     - JSON 规格新增 `deps`/`parallel`/`experts` 字段：可声明 stage 级依赖、控制多专家并行，针对每个专家独立设置 `chiplet`、buffer 大小与延迟，示例见 `assets/models/moe_example.json`。
     - 提供 `tools/generate_chiplet_model.py`，可将 HuggingFace/Qwen 配置直接转换为 Chiplet JSON，支撑 Qwen1.5-MoE-A2.7B 端到端测试。
    - [x] `HostOrchestrator.Advance()` 支持按配置批量发射任务，结合 `deps` 图在单周期内下发多条 MoE 专家命令，提升 Chiplet 并行度。
    - [x] Host Orchestrator 增加水位线驱动的流式批次补充：提供 `--chiplet_host_stream_total_batches/low_watermark/high_watermark` CLI，支持双缓冲/多缓冲与资源释放后的自动补批。
   - [x] 构建算子库：Attention、MoE gating、SwiGLU 等，映射至 PE/SPU/RRAM；
     - `simulator/chiplet/operators` 新增 `Library`，可生成 Attention/MoE/SwiGLU 组合的命令序列，`assembler` 默认输出已改用该库。
   - [x] 考虑 tokenizer/embedding 的运行时接口（可在 Host 侧模拟或调用外部库）。
     - 引入 `simulator/host/tokenizer` 包，定义 `Tokenizer` 接口与 `StaticTokenizer` 实现；`ChipletPlatform` 暴露 `SetTokenizer` 便于后续接入外部词表。
8. **Phase 7：验证与基准**
   - [x] 准备算子级单元测试（矩阵乘、CIM MAC、函数单元）；
     - 在 `simulator/chiplet/operators/library_test.go` 校验 Attention/MoE/SwiGLU/Transformer 宏序列及序列化；`chiplet_phase7_test.go` 通过执行 Transformer+MoE 序列验证数字/传输/RRAM 指标。
   - [x] 构建最小 Transformer 模块（单头 Attention + FFN）benchmark；
     - `prim/transformer.go` 提供 Chiplet 模式数据准备骨架，`assembler` 在 `benchmark=TRANSFORMER` 时生成 `chiplet_commands.json`。
   - [x] 设计 MoE 子模型（含专家调度），验证跨 Chiplet 数据流；
     - `operators.MoEGatingBlock()` 覆盖 gating→transfer→RRAM→回写→合并，`chiplet_phase7_test` 运行 Transformer+MoE 序列确认统计。
   - [x] 扩充 `script/run_validation.sh`，加入 Chiplet 模式 smoke test。
9. **Phase 8：可视化与文档**
   - [x] 更新 `tools/` 下分析脚本，绘制 Chiplet 利用率、CIM 精度、带宽使用；
     - 新增 `tools/chiplet_profiler.py`，解析 `chiplet_log.txt` 并输出 summary/JSON，便于快速可视化。
   - [x] 在 `README.md` 中添加 Chiplet 模式示例和配置指南；
     - README 增加 `Chiplet Mode Quick Start`，包含运行命令与 profiler 用法。
   - [x] 将阶段成果记录在 `docs/`（如新增 `docs/chiplet_architecture.md`、`docs/roadmap.md`）。
     - 新增 `docs/chiplet_architecture.md` 总结平台结构、Benchmark 与分析流程。
     - 更新 `docs/chiplet_architecture.md` 与 `tools/README.md`，补全 Qwen1.5-MoE-A2.7B 指令生成→仿真→分析流程，并记录 Host 流式水位线参数。

### 3. 模块级任务分解

#### 3.1 Host CPU 层
- [ ] tokenizer/embedding 模块：可以直接调用 Python 预处理或在 Go 中实现，需缓存词表与权重。
- [ ] 任务图调度：实现 DAG 节点（算子）到 Chiplet 任务的映射，支持依赖解析与流水化。
- [ ] 数据分片：定义 token batch ↔ tensor block 的对齐策略与元数据。
- [ ] 片间通信：设计 `Message` 结构（payload + latency + priority），替换传统 `channel` 队列。

#### 3.2 数字 Chiplet
- **PE 子系统**
  - [ ] 设计脉动阵列配置接口（tile 大小、滑窗策略、权重复用）。
  - [ ] 建立指令/命令格式，例如 `PE_CMD_GEMM`, `PE_CMD_ATTENTION_HEAD`。
  - [ ] 支持 FP16 输入、累加到 FP32/FP16 的可选模式。
  - [ ] 统计：MAC 次数、阵列利用率、buffer 溢出次数。
- **SPU 子系统**
  - [ ] 指令集涵盖整数/浮点 ALU、SIMD、特殊函数（exp/log/sqrt/ReLU/SwiGLU）。
  - [ ] 建模寄存器堆（32×64bit scalar、32×128bit fp）与发射队列。
  - [ ] 支持函数单元多周期延迟及冲突仲裁。
  - [ ] 统计：指令 mix、函数单元忙时、寄存器冲突次数。
- **Buffer & DMA**
  - [ ] 建立层次：片内共享 SRAM、PE 输入缓存、权重缓存。
  - [ ] Host/Chiplet、Chiplet/Chiplet 之间的 DMA 渠道与时间模型。
  - [ ] 纠错/重放机制（可选），解决带宽不足或冲突。

> 当前已在 `simulator/chiplet/digital` 包中提供 `Chiplet`、`PEArray`、`SPUCluster` 与 `Buffer` 的骨架结构，供后续填充行为模型。

##### 更新记录（2025-11-03）
- [x] 完成簇内任务四阶段队列化调度：`waiting→load→compute→store→spu` 队列支持带宽/阵列资源仲裁，并在阶段完成后自动迁移，详见 `simulator/chiplet/digital/chiplet.go`。
- [x] Load/Store 阶段引入双缓冲语义：提交任务时预占 activation/weight/scratch buffer，store 阶段按字节释放 scratch 容量，确保并发任务不会长时间占满 SRAM。
- [x] `Chiplet.Busy()`、`reserveResources()`、`consumeStore()` 等接口同步更新，现可正确反馈待执行任务与资源占用；`go test ./...` 已验证通过。
- [x] 新增簇级带宽与阵列利用率统计：每周期聚合 load/store 字节、PE/SPU 活跃阵列与完成任务数，并扩展 `chiplet_cycle_log.csv`/`chiplet_log.txt` 供分析脚本使用。
- [x] RRAM Tile/SenseArray 初步建模：支持预处理→脉冲→后处理分阶段推进，记录实际脉冲/ADC 计数并回写 `Stats`；`phase5_smoke_test` 暴露 `CHIPLET_TEST_EXPORT_DIR` 以导出样例日志。
- [x] `tools/chiplet_profiler.py` 增加 `--cycle-log` 支持，自动汇总每周期吞吐/带宽；`phase5_smoke_test` 校验 CSV 表头与新增聚合指标。

#### 3.3 RRAM CIM Chiplet
- [ ] Chiplet 控制器：负责 Tile 分配、上下文管理、结果回传。
- [ ] Tile 调度：平衡 16×16 Tile 之间的负载，支持批量并行。
- [ ] SA 行为：
  - [ ] 128×128 2bit cell 布局，映射权重编码与 DAC 输入。
  - [ ] 128 个 2bit DAC + 12bit ADC 时序模型。
  - [ ] CIM MAC 过程：激活→DAC→电流积分→ADC→数字输出。
  - [ ] 漂移/噪声模型（如可选高斯噪声、多脉冲累积误差）。
- [ ] 与数字 Chiplet 的接口：包括激活加载、权重编程、CIM 结果写回。
- [ ] 统计：脉冲次数、ADC 调用、CIM MAC 数、误差情况。

> 当前已在 `simulator/chiplet/rram` 包中提供 Tile/SenseArray 及 Chiplet 构造函数，后续可在此基础上实现编程、脉冲、误差模型。

#### 3.4 软件栈与工具
- [ ] ISA 扩展：定义 Chiplet 指令编码、寄存器描述符、特殊操作。
- [ ] 编译链接：将算子图编译成 Chiplet 指令流，提供自动分块脚本。
- [ ] 数据准备：生成 tokenizer 词表、embedding 权重、MoE 专家权重并分配到 Chiplet。
- [ ] 验证脚本：自动对比仿真结果与参考实现（如 PyTorch）输出。

### 4. 里程碑与交付建议
- **Milestone A（Phase 0–2）**：完成配置扩展与 Host 重构，提供最小 Chiplet 平台 skeleton，可运行空 workload。
- **Milestone B（Phase 3）**：数字 Chiplet PE/SPU/Buffer 模块可运行小规模 GEMM/激活函数。
- **Milestone C（Phase 4–5）**：RRAM Chiplet 与时间推进框架就绪，可执行一次完整的 “数字→RRAM→数字” 循环。
- **Milestone D（Phase 6）**：完成指令/编译链扩展，可编译并运行基础 Transformer block。
- **Milestone E（Phase 7–8）**：MoE/Transformer 工作负载验证完毕，文档与可视化工具完善。

### 5. 协作与管理建议
- 每完成一个 Phase，更新本文件对应勾选项并在 `git` 中留存里程碑分支。
- 复杂模块（如 SPU 指令集、RRAM SA 模型）建议单独撰写设计文档，置于 `docs/chiplet/`。
- 统一使用当前 `StatFactory` 统计体系，避免重复实现；必要时扩展其接口。
- 保持与现有 UPMEM 模式的兼容性，确保原有单元测试与基准仍可运行。

---

> 后续完成各阶段后，可在本文件 Part II 中直接标记对应任务，并新建章节记录实施细节或经验总结。