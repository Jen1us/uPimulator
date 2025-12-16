# Golang 联合仿真模拟器技术报告（uPIMulator × BookSim2 × Ramulator2）

## 摘要

本文围绕 `golang/` 目录下的联合仿真模拟器，按照“成果报告”体例给出平台搭建思路、软硬件算子库能力边界与片上网络拓扑探索结果。平台以 `uPIMulator` 的执行驱动周期级仿真为核心，将“数字 Chiplet + RRAM CIM Chiplet + 片上互联 + Host 存储系统”统一到一条端到端关键路径上：一方面以可复用的**软件算子库**与**硬件微架构库**描述工作负载与资源占用；另一方面以 **BookSim2**（NoC）与 **Ramulator2**（Host DMA/DRAM，可选）作为外部时序模型，为互联与存储子系统提供可插拔的延迟估算，并通过资源占用窗口将其影响显式注入总周期。

在此基础上，本文对 7 种片上网络拓扑开展对比实验（BookSim2 终端规模统一为 64），结果表明 NoC 拓扑与路由配置会显著改变端到端总周期：在本工作负载下，`Fly k=8,n=2` 最优（4208 cycles），`FlatFly k=4,n=2` 最差（6768 cycles），二者相差约 60.8%。

## 第一章 搭建片上网络仿真与性能评估平台

### 1.1 平台目标与适用边界

芯粒异构集成将系统瓶颈从“单核算力”扩展为“计算—存储—互联”的协同问题。与签核级逐周期实现不同，本平台面向**体系结构探索与方案对比**：以周期级粒度刻画关键资源（数字阵列、CIM 阵列、互联、DMA/DRAM）的占用与排队，把互联与存储系统的长尾影响纳入端到端关键路径，从而为拓扑选型、微结构参数敏感性分析与 PPA（能耗/面积）评估提供可复现的量化依据。

### 1.2 三大组件及其分工

平台由三部分构成，并通过清晰的职责边界实现“松耦合联合仿真”：

- **uPIMulator（Golang）**：主控仿真引擎与平台骨架。负责解析工作负载命令/阶段，推进周期，建模数字侧与 RRAM 侧的资源占用与并行性，并汇总端到端指标（周期、带宽、背压、能耗/面积估计等）。
- **BookSim2（片上网络）**：NoC 仿真器。负责在给定拓扑、路由与 VC/缓冲参数下，对一次“端点间传输”给出延迟估算，使互联具备拓扑敏感性与拥塞敏感性。
- **Ramulator2（Host 存储，可选）**：DRAM 仿真器。用于替代固定带宽近似，对 Host DMA/DRAM 访问给出服务时间与排队效应估算，引入访问类型与队列拥塞差异。

### 1.3 三者如何连接与交互

平台采用“主引擎 + 外部时序服务”的联合方式：uPIMulator 在运行时启动 BookSim2/Ramulator2 的服务进程，并通过标准输入输出进行 JSON 行协议交互。外部仿真器不参与数据搬运本身，仅返回**延迟（cycles）**；主引擎将该延迟写入对应资源的占用窗口并推进全局关键路径。

这一连接方式强调两点：其一，NoC/DRAM 模型可独立替换与批量扫参；其二，主仿真维持单一的“执行驱动周期推进”语义，外部模型以“延迟约束”的形式嵌入，不破坏整体可控性与可复现性。

### 1.4 片上网络仿真（重点）

平台对 NoC 的建模重点在于“估算—占用—背压”闭环：

1. 当执行到 `Digital → RRAM` 或 `RRAM → Digital` 的跨域传输阶段时，提取源/目的端点与传输字节数；
2. 通过 BookSim2（配置文件描述 topology/routing/VC 等）返回该传输的估算延迟；
3. 将估算延迟累加到互联占用窗口，使互联成为可竞争资源；在窗口结束前，新的传输会被推迟，从而形成背压；
4. 背压在多次传输与多阶段流水中累计，最终体现在端到端总周期差异上（第三章给出拓扑对比实验）。

### 1.5 性能评估平台（重点）

平台的性能评估输出强调“可复现、可对比、可后处理”。仿真结束后，关键统计写入 `chiplet_log.txt`，其中包含：

- **端到端指标**：`ChipletPlatform_cycles`、各域周期（Digital/RRAM/Interconnect）、任务完成数等；
- **互联指标**：传输字节数、hop 统计、互联背压事件/背压周期等；
- **CIM 特征指标**：脉冲数、ADC 采样数、预处理/后处理周期、权重驻留/命中等；
- **PPA 估计**：基于参数化模型输出动态能耗拆分与面积估算，用于方案级比较。

本报告第三章实验数据见 [`docs/noc_topology_experiment_results.json`](noc_topology_experiment_results.json)，图表见 [`assets/noc_topology_cycles.svg`](../assets/noc_topology_cycles.svg)。

### 1.6 抽象软件架构图（LaTeX/TikZ）

以下 LaTeX 代码提供一张“抽象的软件架构图”，用于在报告/论文中直接引用（建议使用 `tikz` 编译）。

```latex
% Requires:
%   \usepackage{tikz}
%   \usetikzlibrary{arrows.meta,positioning,fit}
\begin{tikzpicture}[
  font=\small,
  >=Latex,
  main/.style={draw,rounded corners,align=center,inner sep=6pt,text width=8.2cm},
  svc/.style={draw,rounded corners,align=center,inner sep=6pt,text width=6.4cm},
  group/.style={draw,rounded corners,inner sep=8pt},
  arrow/.style={-Latex,thick},
  lbl/.style={font=\scriptsize,fill=white,inner sep=1pt}
]
  \node[main] (workload) {Workload Specification\\(Operator Macros / Stage Sequence)};
  \node[main, below=12mm of workload] (assembler) {Assembler\\(Emit Command Sequence)};

  \node[main, below=12mm of assembler] (engine) {uPIMulator (Go)\\Execution-Driven Cycle-Level Engine\\
  Host Orchestration / Scheduling\\Digital / RRAM / Interconnect Modeling\\Stats + PPA Estimation};

  \node[svc, right=38mm of engine, yshift=26mm] (booksim) {BookSim2\\NoC Timing Service\\(Topology / Routing / VC)};
  \node[svc, right=38mm of engine, yshift=-26mm] (ramulator) {Ramulator2\\Host DRAM Timing Service\\(Optional)};

  \node[main, below=12mm of engine] (outputs) {Outputs \& Evaluation\\chiplet\_log.txt\\Stats / Cycles / PPA\\Post-processing \& Plots};

  \node[group, fit=(assembler) (engine), label={[font=\small]left:uPIMulator Workflow}] {};

  \draw[arrow] (workload) -- node[lbl, right, xshift=2mm]{Specs / Params} (assembler);
  \draw[arrow] (assembler) -- node[lbl, right, xshift=2mm]{chiplet\_commands.json} (engine);
  \draw[arrow] (engine) -- node[lbl, right, xshift=2mm]{Stats / Logs} (outputs);

  \draw[arrow, bend left=18] (engine.north east) to node[lbl, pos=0.55, above, sloped]{(src,dst,bytes)\\NoC Latency Query} (booksim.west);
  \draw[arrow, bend left=18] (booksim.west) to node[lbl, pos=0.45, below, sloped]{cycles} (engine.north east);

  \draw[arrow, bend left=18] (engine.south east) to node[lbl, pos=0.55, above, sloped]{bytes / access\\DMA Latency Query} (ramulator.west);
  \draw[arrow, bend left=18] (ramulator.west) to node[lbl, pos=0.45, below, sloped]{cycles} (engine.south east);
\end{tikzpicture}
```

## 第二章 建立软硬件算子库

### 2.1 AI 软件算子库（支持哪些算子）

平台在工作负载描述上采用“两级抽象”：其一是**算子宏**（用于快速搭建 Attention/MoE 等高层模块）；其二是**阶段类型（Stage Types）**（用于表达更贴近真实模型的流水与依赖）。两者可以单独使用，也可以组合：当没有外部规格时，默认使用算子宏生成命令序列；当提供模型 JSON 时，则按阶段序列生成任务图并保持更高的可控性。

#### 2.1.1 算子宏（Operator Macros）

平台内置的算子宏覆盖以下典型模块（摘要）：

| 算子宏 | 含义（报告级描述） | 典型阶段（示意） | 涉及域 |
|---|---|---|---|
| AttentionBlock | 最小注意力子流水，用于刻画“数字预处理 + CIM 线性 + 回传后处理”的端到端路径 | token\_prep → D→R → RRAM(stage/exec/post) → R→D → elementwise | Digital/RRAM/NoC |
| MoEGatingBlock | MoE gating + expert 计算的简化版本，用于刻画“gating/归约 + expert 传输 + CIM + merge”的关键交互 | gating(spu) → top-k(reduce) → host fetch → D→R → RRAM(...) → R→D → merge | Host/Digital/RRAM/NoC |
| SwiGluBlock | SwiGLU 激活的简化版本，用于刻画逐元素与非线性阶段的占用 | elementwise/activation（两段） | Digital |
| TransformerBlock / Pipeline | Attention 与 FFN（SwiGLU）串联的层级构件，用于快速拉长流水 | Attention + SwiGLU（可多层） | 跨域协同 |

#### 2.1.2 阶段类型（Stage Types）

平台支持的阶段类型覆盖常见 AI 模型流水（摘要）：

- **计算类**：`token_prep`、`attention`、`softmax`、`layernorm`、`residual`、`activation`、`elementwise`、`postprocess`；
- **CIM 映射类**：`gemm`、`rram_linear`、`moe_linear`（映射到 RRAM CIM 侧的 stage/execute/post 链路）；
- **系统类**：`transfer`（Host↔Digital、Digital↔RRAM）、`moe_gating`、`moe_merge`、`sync`。

该分层使“算子 → 阶段 → 资源域（Host/数字/RRAM/互联）”的映射关系清晰可控，便于在架构探索阶段开展结构化建模与对比实验。

#### 2.1.3 数值链路参考（CIM 反量化）

为保证“低比特权重 + 模拟阵列计算 + 数字后处理”的数值链路具备可核对的参考，平台提供了 FP16×INT4 的 CIM 反量化流程参考实现（见 `golang/sa_dequant_simulator.py`），用于对关键步骤（补码切片、权重映射、偏置/scale 还原等）进行抽样校验。

### 2.2 硬件库（微架构单元）

#### 2.2.1 数字 Chiplet（Digital）

数字侧模型以“吞吐 + 占用 + 并行性”为核心，主要单元与细节如下：

- **Compute Cluster**：数字 Chiplet 的基本调度单元，内部维护等待队列与轮转策略，刻画不同执行单元间的并行与竞争关系；
- **PE Array（矩阵阵列）**：用于 tile/GEMM 类计算；支持并发度与阵列波次（wave）概念，输出 MAC 计数与忙碌周期；
- **SPU Cluster（标量/轻量向量）**：用于 gating、归约、elementwise 等非 GEMM 阶段，统计 scalar/vector/special ops；
- **VPU Unit（向量执行）**：用于向量化阶段与 VPU 型操作，按 issue width / lane / latency 建模；
- **片上 Buffer（activation/weights/scratch）**：容量与带宽参数化，记录峰值占用，用于评估片上存储压力与潜在瓶颈；
- **能耗与统计**：按周期累计动态能耗与静态能耗估计，并输出可用于拆分分析的指标（PE/SPU/VPU 等）。

#### 2.2.2 RRAM CIM Chiplet

RRAM 侧模型面向“阵列计算 + 模数混合外设 + 数字后处理”的链路，主要单元与细节如下：

- **Tile / SenseArray**：以阵列几何（rows/cols）与器件精度（cell bits、DAC/ADC bits）为核心参数，刻画 CIM 计算能力与外设开销；
- **Controller（资源预留/调度）**：对 CIM 任务进行占用与排队建模，输出执行周期并驱动统计累积；
- **Preprocess / Postprocess**：分别对应激活预处理与输出后处理，提供可拆分的周期与能耗项；
- **WeightDirectory（权重驻留）**：以 tag 记录权重驻留与命中，统计 weight load / hit / peak bytes，支撑复用收益评估；
- **CIM 特征统计**：脉冲数、ADC 采样数、预/后处理周期等作为能耗估计的关键驱动量。

#### 2.2.3 互联与 Host 存储

- **互联（Interconnect）**：以“互联占用窗口”表达传输的串并行关系；在启用 BookSim2 时，窗口由外部 NoC 延迟估算驱动，从而具备拓扑敏感性；
- **Host DMA/DRAM（可选）**：Host 侧以 DMA 控制器建模带宽占用；在启用 Ramulator2 时，DMA 周期由 DRAM 服务模型替代带宽近似，从而引入排队与访问类型差异；
- **KV Cache（可选）**：用于刻画注意力类工作负载的缓存命中与带宽影响，输出 hit/miss 与字节统计，便于开展系统级瓶颈归因。

## 第三章 探索片上网络拓扑结构

### 3.1 实验目标

在保持工作负载与端点规模不变的条件下，仅改变 NoC 拓扑与路由配置，观测端到端总周期的变化，回答两个问题：

1. NoC 拓扑差异能否在端到端总周期层面形成可量化的区分？
2. 哪类拓扑在本平台工作负载下更具优势，差异主要体现在哪个量级？

### 3.2 实验设置

- **工作负载**：内置 `benchmark=BS`（Attention + MoE + SwiGLU，共 20 条命令）。
- **测量指标**：`ChipletPlatform_cycles`（越小越好）。
- **NoC 规模**：BookSim2 配置统一为 64 terminals；平台实际端点（Digital+RRAM）映射到其中的子集端口，以保证不同拓扑在统一规模下可比。
- **对比拓扑（7 种）**：Mesh、Torus、CMesh、FlatFly、Fly、FatTree、Tree4。

实验数据记录于 [`docs/noc_topology_experiment_results.json`](noc_topology_experiment_results.json)，图表由 [`tools/render_noc_topology_cycles_svg.py`](../tools/render_noc_topology_cycles_svg.py) 生成。

### 3.3 实验结果（黑白柱状图）

![不同 NoC 拓扑下的 Chiplet 总周期对比（黑白）](../assets/noc_topology_cycles.svg)

表 3-1 NoC 拓扑对比结果（按 cycles 从小到大）

| 拓扑 | BookSim 配置 | cycles | 相对 Mesh |
|---|---|---:|---:|
| Fly k=8,n=2 | tools/booksim_configs/fly_k8_n2.conf | 4208 | -2.5% |
| FatTree k=4,n=3 | tools/booksim_configs/fattree_k4_n3.conf | 4242 | -1.7% |
| Tree4 k=4,n=3 | tools/booksim_configs/tree4_k4_n3.conf | 4242 | -1.7% |
| Mesh 8×8 | tools/booksim_configs/mesh_8x8.conf | 4316 | 0% |
| CMesh 4×4,c=4 | tools/booksim_configs/cmesh_4x4_c4.conf | 4766 | +10.4% |
| Torus 8×8 | tools/booksim_configs/torus_8x8.conf | 4843 | +12.2% |
| FlatFly k=4,n=2 | tools/booksim_configs/flatfly_k4_n2.conf | 6768 | +56.8% |

### 3.4 分析与讨论

（1）**拓扑差异会被“互联占用窗口”放大为端到端差异**。平台把 NoC 延迟作为互联资源的占用时间累积到关键路径上，因此即便单次传输延迟差异有限，在多次传输与多阶段流水中也会形成可观的总周期差异。  

（2）**在本工作负载下，Fly 与树型拓扑表现更优**。`Fly k=8,n=2` 与 `FatTree/Tree4` 获得最低总周期，说明在当前端点映射与传输规模下，更高的路径选择自由度与较小的有效直径更有利于降低互联引入的长尾开销。  

（3）**二维规则拓扑呈现“稳定但非最优”**。Mesh 8×8 的结果接近最优，体现了规则拓扑在路由与负载均衡上的稳定性；而 Torus 8×8 在本配置下略差，提示“拓扑类型”并不必然决定优势，路由策略与微结构参数（VC、缓冲、路由延迟等）同样关键。  

（4）**FlatFly 在当前配置下最差，提示拓扑调参与端点映射的重要性**。Flattened butterfly 的表现对路由/缓冲/端点映射更敏感；在未针对性调参的情况下，端口竞争与路由特性可能导致显著劣化。  

综上，本章以 7 种 NoC 拓扑对比验证了平台的“拓扑—时序—端到端”闭环能力：BookSim2 的拓扑差异能够稳定体现在 `ChipletPlatform_cycles` 上，为后续更大规模的设计空间探索（拓扑 + 带宽 + VC/缓冲 + 端点映射）提供了可复现的评估基础。
