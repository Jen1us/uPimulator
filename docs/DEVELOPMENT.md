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
   - 新增模块需考虑线程安全（尤其在仿真循环内部）。
3. **实现与自测**  
   - 使用 `script/build.py` + 手动运行样例验证。
   - 针对关键逻辑（如内存控制器、流水线）建议编写最小 benchmark 或单元测试（可放在 `script/` 下）。
4. **日志校验**  
   - 确认 `bin/log.txt` / `args.txt` / `options.txt` 是否符合预期。
   - 对比 `tools/` 提供的 Excel 模板或分析脚本。

## 7. 代码规范
- 保持 ASCII 编码；遵循 Go 官方格式化（`gofmt`）。
- 在关键复杂逻辑前添加简短注释；避免冗余说明。
- Panic 仅用于非法参数、内部一致性错误；用户输入错误应在 CLI 校验阶段阻止。
- 新增 CLI 参数需更新：
  - `misc.CommandLineParser`（默认值、描述）
  - `mis.CommandLineValidator`（校验逻辑）
  - `README.md`/本 `DEVELOPMENT.md` 内的说明（如相关）

## 8. 引入 RRAM CIM 的详细改造清单

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

## 11. 当前已知问题（2025-03）

1. **队列粒度仍停留在 Chiplet 层**：`chiplet_commands.json` 中的 `queue`/`chiplet` 标记只区分 cluster，`digital/chiplet.go` 在 `reserveResources` 时也默认整个 cluster 的 PE 池被占用，导致 `DigitalChiplet[*]_pe[1..3]_busy_cycles` 长期为 0。要实现真实并行，需要：
   - 在命令生成阶段提供 PE-array 级别的 `queue_id`；
   - 在数字调度器内根据 `queue_id` 将任务绑定到某个 array，并允许其它 array 同时服务后续 chunk。
2. **Chunk 仍串行推进**：`append_chunked_stage()` 默认为 chunk 建立线性依赖，再叠加 orchestrator 的 per-cycle 限额，使得 chunk 之间几乎无法并行，`task_deferrals` 居高不下。后续需要放松 chunk 依赖（只保留真正的数据依赖），并在 `HostOrchestrator` 的资源估算里引入“chunk 共享权值/缓冲”的概念，从而让不同 chiplet/cluster 可以并行处理 chunk。

> 以上问题会直接影响后续的面积/功耗/时延建模，需要在开始精细硬件分析前排期修复或在报告中注明假设。

---

维护者更新此文档时，请确保与最新代码保持一致。所有 Go 版的新需求、约定与限制应先在此文档说明，再进入开发流程。
