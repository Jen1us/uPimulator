# Golang 联合仿真平台技术报告（Chiplet + BookSim2 + Ramulator2）

## 摘要

本文面向 `golang/` 目录下的联合仿真平台，给出一份“成果报告式”的总体说明。平台以 Go 语言实现的周期级执行驱动模拟器为核心，面向“数字 Chiplet + RRAM CIM Chiplet + 片上互联 + Host 存储系统”的端到端建模需求：一方面提供可复用的软件算子库与硬件微架构库，另一方面通过可插拔的 BookSim2（片上网络）与 Ramulator2（Host DMA/DRAM）实现跨域时序耦合，并输出可用于性能与 PPA（能耗/面积）评估的统计结果。基于三种典型 NoC 拓扑（Mesh/CMesh/FlatFly）开展对比实验，结果表明互联拓扑与配置会显著影响端到端总周期：Mesh 8×8 最优（4316 cycles），CMesh 4×4,c=4 次之（4766 cycles），FlatFly k=4,n=2 最差（6768 cycles）。

## 1. 概述

### 1.1 背景与目标

芯粒（Chiplet）异构集成在提升算力密度与能效的同时，也使性能瓶颈从单一计算单元扩展到“计算—存储—互联”的系统级协同。本联合仿真平台的目标是：

（1）在周期级粒度上评估数字计算与 RRAM CIM 的协同效率；  
（2）在端到端路径上显式纳入片上网络与 Host 存储系统的时序影响；  
（3）对算子级/阶段级的性能、带宽占用、能耗与面积进行可重复的量化评估；  
（4）支持在不同 NoC 拓扑与微结构参数下开展敏感性分析与方案对比。

### 1.2 名词与术语

- **执行驱动周期级仿真**：以“任务/事件完成”为触发推进系统状态，在周期维度上对关键资源（计算阵列、互联、DMA 等）进行占用与排队建模。
- **命令（command）/阶段（stage）**：平台内部的高层执行单元，用于描述 Attention/MoE 等算子在 Host、数字侧、RRAM 侧以及互联上的分解与依赖关系。
- **片上网络（NoC）**：用于数字 Chiplet 与 RRAM Chiplet 之间（或更一般的芯粒间）数据传输的互连网络；本文采用 BookSim2 提供延迟估算。
- **性能评估平台**：指仿真输出的数据产出、指标定义与后处理工具链，用于把周期级仿真过程转化为可读的性能与 PPA 结论。

### 1.3 覆盖范围与假设

本平台面向架构探索与方案对比，强调对端到端瓶颈与相对趋势的刻画，而非对某一具体实现的逐周期“签核级”复现。其覆盖范围与关键假设如下：

（1）**覆盖范围**：包含 Host 编排调度、数字 Chiplet 计算、RRAM CIM 计算、跨域数据传输与统计汇总；其中片上网络与 Host 存储系统可分别通过 BookSim2 与 Ramulator2 进行可插拔式接入。  
（2）**关键假设**：NoC 时序由 BookSim2 返回的估算延迟驱动，并通过互联占用窗口耦合到端到端关键路径；能耗/面积为参数化估计结果，用于方案级比较与敏感性分析。  
（3）**适用边界**：结果对 NoC/DRAM 配置与微结构参数敏感，应在统一配置与相同工作负载条件下进行横向对比；数值正确性验证建议结合参考模型与关键算子抽样校验。

## 2. 仿真平台组成与职责

### 2.1 平台总体结构

平台可抽象为“工作负载描述—执行引擎—可插拔时序模型—评估输出”四层（图 2-1）：

```
工作负载描述（算子库/模型规格） -> Host 编排与依赖调度 -> Chiplet 执行引擎（多时钟域）
                                                |-> 数字 Chiplet 微架构模型
                                                |-> RRAM CIM 微架构模型
                                                |-> 片上网络时序（BookSim2，可选）
                                                |-> Host DMA/DRAM 时序（Ramulator2，可选）
输出：日志/统计/能耗面积估计 -> 后处理工具 -> 性能与 PPA 结论
```

### 2.2 组成模块说明

平台由以下部分组成，各部分职责边界清晰、可独立替换：

（1）**工作负载生成与装配**：将 Attention、MoE、SwiGLU 等算子分解为带依赖的命令序列，形成可执行的任务图。  
（2）**Host 编排器**：对任务图进行依赖解析与调度，控制任务在数字侧、RRAM 侧、互联与 Host 侧资源之间的发射顺序与并行性。  
（3）**Chiplet 执行引擎（核心）**：以多时钟域方式推进数字计算、RRAM 计算与互联传输，负责资源占用、排队、完成事件通知与统计汇总。  
（4）**片上网络仿真（重点）**：将跨 Chiplet 传输映射为 NoC 消息，调用 BookSim2 返回的估算延迟，并将该延迟耦合进互联占用窗口，从而影响全局完成时间。  
（5）**Host DMA/DRAM 仿真（可选）**：对 Host↔数字侧数据搬移给出更真实的服务时间与排队效应，补足仅用固定带宽近似带来的偏差。  
（6）**性能与 PPA 评估（重点）**：输出总周期、吞吐、带宽、利用率、RRAM 脉冲/ADC 采样等关键指标，并据此估算能耗与面积，支持脚本化后处理与对比分析。

### 2.3 典型执行流程（以数字↔RRAM 协同为例）

在典型的 Transformer 推理流水中，常见的数据与控制路径包括：

（1）Host 在数字侧准备输入激活（token_prep/embedding 等）；  
（2）数字侧发起对 RRAM CIM 的线性层请求，触发数字↔RRAM 的多次数据传输；  
（3）RRAM 侧执行 CIM 阵列计算，并进行必要的预处理/后处理（含反量化、累加、激活等）；  
（4）结果回传数字侧继续完成 softmax、layernorm、残差与 elementwise 等计算；  
（5）MoE 场景下，Host 侧还会参与 gating 参数获取与 expert 结果合并等事件调度。

## 3. 片上网络仿真与性能评估（重点）

### 3.1 NoC 建模范围与接口

本平台将**跨 Chiplet 的数据搬移**抽象为互联传输任务，并将其作为性能瓶颈的显式来源之一。建模关注点主要包括：

（1）**端点与规模**：数字 Chiplet 与 RRAM Chiplet 作为 NoC 终端（terminal），支持固定规模下的拓扑替换对比。  
（2）**消息规模**：以传输字节数描述消息长度，适配 KB 级激活/权重片段的搬移场景。  
（3）**互联占用**：互联被抽象为可争用资源；传输一旦发射，将在估算周期内占用互联能力并对后续传输形成背压。

### 3.2 BookSim2 接入与时序耦合策略

BookSim2 在平台中以“延迟估算服务”的角色出现：给定源端点、目的端点与消息大小，返回在指定拓扑与参数下的估计传输周期。为了使拓扑差异能够反映到端到端性能，本平台采用“**互联忙碌窗口**”的耦合策略：

（1）当发生数字↔RRAM 传输时，向 BookSim2 查询该消息的估算延迟；  
（2）将估算延迟计入互联资源的占用窗口，在窗口期内延后新的传输发射；  
（3）由此实现“互联拥塞/路径差异 → 传输完成时间变化 → 任务图关键路径变化 → 总周期变化”的闭环。

该策略的优势是：在保持平台为周期级执行驱动框架的前提下，以较小的接口成本引入 NoC 拓扑、路由与交换结构对系统级性能的影响。

### 3.3 性能评估输出与指标体系

平台输出分为“汇总统计”和“过程数据”两类，用于分别支撑结论性对比与深度诊断：

（1）**汇总统计**：包含端到端总周期（如 `ChipletPlatform_cycles`）、任务数、各类传输字节、RRAM 脉冲/ADC 采样计数，以及由参数化模型给出的能耗/面积估计。  
（2）**过程数据**：按周期记录数字侧与 RRAM 侧的活动、互联吞吐与利用率等，可用于定位瓶颈与验证调度并行性。

上述数据统一输出到实验的结果目录（即运行时指定的 `bin_dirpath`），便于对不同配置/拓扑的多次运行结果进行批量归档与回溯。

为便于评估与复核，平台将关键数据产出固化为可直接检索的文件（表 3-1）。

表 3-1 评估数据产出与用途

| 输出项 | 主要内容 | 典型用途 |
|---|---|---|
| `chiplet_log.txt` | 总周期、任务统计、传输统计、RRAM 脉冲/采样与能耗/面积估计 | 结论指标汇总、跨方案对比 |
| `chiplet_cycle_log.csv` | 每周期活动、吞吐与利用率 | 瓶颈定位、并行性与背压诊断 |
| `[chiplet-debug]` 调试行（可过滤） | 任务发射/完成与传输细节 | 细粒度复核（建议重定向保存） |

配套后处理工具可将日志解析为结构化数据（如 JSON），支撑跨拓扑、跨参数的批量实验与绘图汇总。

### 3.4 指标口径与推荐分析流程

（1）**`ChipletPlatform_cycles`**：端到端总周期，覆盖计算、传输与资源等待等因素，用于方案级对比（越小越好）。  
（2）**推荐流程**：先用 `chiplet_log.txt` 获取结论指标，再结合 `chiplet_cycle_log.csv` 识别瓶颈（例如互联背压、数字侧或 RRAM 侧利用率不足），必要时再通过 `[chiplet-debug]` 调试行进行细粒度复核。

## 4. 软件算子库（AI 算子支持）

### 4.1 算子宏库（Operator Library）

平台提供可复用的算子级宏描述，用于快速拼装典型 LLM 推理流水。当前内置宏覆盖：

（1）**AttentionBlock**：包含 token 准备、跨 Chiplet 传输、CIM 线性层、回传与 elementwise 后处理等阶段。  
（2）**MoEGatingBlock**：包含 gating 计算、top-k/reduce、Host 侧 gating 事件、expert 分发（含传输 + CIM）与 merge 合并。  
（3）**SwiGluBlock**：近似 SwiGLU 的两段 elementwise/激活路径。  
（4）**TransformerBlock/Pipeline**：由 Attention + FFN（SwiGLU）组合构成可重复的层级流水。

表 4-1 内置算子宏覆盖范围（摘要）

| 宏/算子块 | 典型包含阶段 | 主要执行域 |
|---|---|---|
| AttentionBlock | token_prep、线性层（CIM）、回传、elementwise/残差 | Host/数字/RRAM/互联 |
| MoEGatingBlock | gating、top-k/归约、expert 分发（含 CIM）、merge | Host/数字/RRAM/互联 |
| SwiGluBlock | elementwise、activation、后处理 | 数字（为主） |
| TransformerBlock/Pipeline | Attention + FFN 组合与层级串联 | 跨域协同 |

### 4.2 阶段类型与执行域映射

为适配不同模型的流水差异，平台以 `stage.type` 描述常见 AI 执行阶段，典型覆盖包括：

（1）**计算类**：`attention`、`gemm`、`softmax`、`layernorm`、`activation`、`elementwise`、`residual`、`postprocess` 等；  
（2）**CIM 映射类**：`rram_linear`、`moe_linear` 等（映射到 RRAM CIM 侧执行与其后处理链路）；  
（3）**系统类**：`transfer`（Host↔数字、数字↔RRAM）、`sync`（同步与屏障）以及 `moe_gating/moe_merge` 等。

该分层方式使得“算子 → 阶段 → 资源域（Host/数字/RRAM/互联）”的映射关系清晰可控，便于进行结构化建模与对比实验。

表 4-2 阶段类型覆盖与建模要点（摘要）

| 阶段类型（示例） | 含义 | 关注指标（示例） |
|---|---|---|
| `rram_linear` / `moe_linear` | 线性层映射到 RRAM CIM | 周期、权重驻留/复用、脉冲与 ADC 采样、能耗 |
| `gemm` / `attention` | 数字侧矩阵/注意力计算 | 周期、阵列利用率、片上缓存/带宽占用 |
| `softmax` / `layernorm` / `activation` / `elementwise` | 常见归一化与逐元素算子 | 周期、向量/标量单元占用 |
| `transfer` | Host↔数字、数字↔RRAM 数据搬移 | 传输字节、互联占用、背压与排队 |
| `sync` | 同步与屏障 | 串并行边界开销、关键路径影响 |

## 5. 硬件库（微架构单元建模）

### 5.1 数字 Chiplet（Digital）

数字侧模型以吞吐与占用为核心，面向 GEMM/向量/标量等混合工作负载，主要微架构单元包括：

（1）**PE Array（矩阵计算阵列）**：用于 GEMM/Tile 级计算的并行执行与流水化建模。  
（2）**SPU/VPU（标量/向量单元）**：用于 gating、归约、elementwise 与辅助算子，支持与矩阵阵列的资源区分与并行性表达。  
（3）**片上 SRAM Buffer**：用于激活/中间结果暂存，提供容量与读写代价的参数化建模。  
（4）**同步与归约机制**：用于描述 barrier、reduce、buffer 生命周期管理等系统开销。

### 5.2 RRAM CIM Chiplet

RRAM 侧模型面向“阵列计算 + 模数混合外设 + 数字后处理”的链路，主要单元包括：

（1）**Tile/SenseArray**：阵列几何（行列）、多比特 cell、DAC/ADC 位宽等；  
（2）**控制与权重驻留**：权重目录与加载命中统计，支撑“权重驻留—复用”场景的效益评估；  
（3）**预处理/后处理链路**：反量化、累加、激活等阶段的周期与能耗模型；  
（4）**脉冲与采样统计**：以 pulse 数与 ADC sample 数作为能耗估计的关键驱动量。

### 5.3 互联与 Host

（1）**互联资源模型**：以互联占用窗口与背压表达多次传输的串并行关系，并可由 BookSim2 的估算延迟驱动。  
（2）**Host DMA/DRAM（可选）**：用于描述 Host↔数字侧搬移的服务时间与排队效应，补齐端到端路径中存储系统的影响。

### 5.4 数值一致性参考（CIM 反量化）

平台提供 CIM 反量化的参考实现，用于核对“低比特权重 + 模拟阵列计算 + 数字后处理”的数值链路一致性，为性能模型提供必要的正确性参照。

表 5-1 硬件微架构单元库（摘要）

| 域 | 关键单元 | 主要职责 | 关注指标（示例） |
|---|---|---|---|
| 数字 Chiplet | PE Array、SPU/VPU、片上 SRAM、同步/归约 | 数字侧矩阵与向量计算、数据暂存与同步 | 周期、利用率、片上带宽占用、能耗/面积估计 |
| RRAM CIM Chiplet | Tile/SenseArray、DAC/ADC、控制与权重驻留、后处理链路 | 阵列计算与模数混合链路、权重复用与后处理 | 周期、命中/复用、脉冲与采样、能耗/面积估计 |
| 互联（NoC） | BookSim2（可插拔）、互联占用窗口 | 跨 Chiplet 数据搬移与背压建模 | 估算延迟、互联占用、拥塞敏感性 |
| Host/存储 | DMA、DRAM（Ramulator2 可选） | Host↔数字侧搬移与排队效应 | 服务时间、队列/排队、端到端关键路径影响 |

## 6. 不同 NoC 拓扑结构下的性能仿真实验

### 6.1 实验目的与设置

实验目的在于验证：在保持工作负载与端点规模不变的条件下，仅改变 NoC 拓扑（及相应配置）是否会显著改变端到端执行周期，从而为互联方案选型提供量化依据。

（1）**工作负载**：内置 Attention + MoE + SwiGLU 流水（`benchmark=BS`，20 条命令）。  
（2）**对比对象**：Mesh 8×8、CMesh 4×4（c=4）、FlatFly（k=4,n=2），终端规模均为 64。  
（3）**指标**：`ChipletPlatform_cycles`（端到端总周期，越小越好）。  
（4）**数据与图表**：结果见 [`noc_topology_experiment_results.json`](noc_topology_experiment_results.json)，柱状图源文件为 [`assets/noc_topology_cycles.svg`](../assets/noc_topology_cycles.svg)。

### 6.2 结果

表 6-1 不同 NoC 拓扑下的端到端总周期（BookSim2 估算互联延迟）

| NoC 拓扑 | `ChipletPlatform_cycles` |
|---|---:|
| Mesh 8×8 | 4316 |
| CMesh 4×4,c=4 | 4766 |
| FlatFly k=4,n=2 | 6768 |

图 6-1 不同 NoC 拓扑下的端到端总周期对比

![NoC 拓扑下的 Chiplet 总周期对比](../assets/noc_topology_cycles.svg)

### 6.3 结果分析

（1）在该工作负载中，数字↔RRAM 之间存在多次 KB 级传输；互联延迟经由“互联忙碌窗口”机制被纳入任务关键路径，因此拓扑差异会直接反映到总周期差异。  
（2）以 Mesh 8×8 为基准，CMesh 4×4,c=4 总周期增加 450 cycles（约 +10.4%）；FlatFly k=4,n=2 增加 2452 cycles（约 +56.8%）。在本配置下，FlatFly 的路由/交换开销与潜在拥塞更显著，导致端到端性能劣化。  
（3）需要强调的是，NoC 的绝对数值不仅由拓扑决定，也受虚拟通道数、缓冲深度、分配器设置、flit 大小等微结构参数影响。本文实验的结论重点在于：**互联拓扑/配置变化会显著改变 Chiplet 系统的端到端周期**，可作为后续更精细参数扫描与架构权衡的基础。

## 7. 结论与后续工作

本报告总结了 Golang 联合仿真平台在“算子—微架构—互联—存储系统”四个维度的建模能力，并通过 NoC 拓扑对比实验验证了互联对端到端性能的显著影响。后续可在以下方向进一步完善：

（1）扩大工作负载覆盖（更多层数、更大 batch、不同 KV-cache 策略）；  
（2）细化 NoC/DRAM 的微结构参数扫描，形成更完整的设计空间探索（DSE）流程；  
（3）将数值正确性验证与性能模型更紧密结合，形成“功能—性能—PPA”一体化评估闭环。

## 附录 A：复现实验（建议）

为保证实验可复现，建议固定二进制、配置与输出目录后开展批量运行。以下给出参数要点（以 Mesh 为例，路径按本机环境替换）：

（1）开启 Chiplet 模式：`--platform_mode chiplet`  
（2）选择工作负载：`--benchmark BS`  
（3）启用 BookSim2：`--chiplet_noc_booksim_enabled 1`  
（4）指定配置文件：`--chiplet_noc_booksim_config tools/booksim_configs/mesh_8x8.conf`  
（5）指定服务端：`--chiplet_noc_booksim_binary golang/booksim2/src/booksim_service`

表 A-1 实验使用的 NoC 配置文件

| 拓扑 | 配置文件 |
|---|---|
| Mesh 8×8 | `tools/booksim_configs/mesh_8x8.conf` |
| CMesh 4×4,c=4 | `tools/booksim_configs/cmesh_4x4_c4.conf` |
| FlatFly k=4,n=2 | `tools/booksim_configs/flatfly_k4_n2.conf` |

## 附录 B：后处理工具（建议）

平台提供若干脚本用于将日志转换为结构化数据并汇总关键指标，建议按“先汇总、再诊断”的顺序使用：

（1）`golang/uPIMulator/tools/chiplet_profiler.py`：解析 `chiplet_log.txt`，输出关键指标（可导出 JSON）。  
（2）`tools/cycle_log_summarizer.py`：汇总 `chiplet_cycle_log.csv`，提取平均吞吐、利用率等过程统计。  
（3）`tools/transfer_debug_analyzer.py`：在开启调试输出时，按传输阶段/端点汇总搬移规模与特征。

## 附录 C：参考实现位置（便于追踪，不作为正文依赖）

- Chiplet 平台与统计输出：`golang/uPIMulator/src/simulator/chiplet_platform.go`  
- Host 编排器/任务图：`golang/uPIMulator/src/simulator/chiplet/orchestrator.go`  
- 算子宏库：`golang/uPIMulator/src/simulator/chiplet/operators/library.go`  
- BookSim 客户端：`golang/uPIMulator/src/simulator/noc/booksim/client.go`  
- CIM 反量化参考：`golang/sa_dequant_simulator.py`
