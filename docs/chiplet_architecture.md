# Chiplet Architecture Overview

本文件简要总结 Phase6/Phase7 中实现的 Chiplet 平台改造，便于后续团队成员快速了解模拟器与分析工具的使用方式。

## 平台结构
- **ChipletPlatform**：在 `simulator/chiplet_platform.go` 中实现，统一调度数字 Chiplet、RRAM Chiplet 与互联系统，多时钟域推进。
- **HostOrchestrator**：支持基于 `deps` 拓扑批量下发任务，`Advance()` 每周期可一次发射多条命令，并可通过 `--chiplet_host_stream_{total_batches,low_watermark,high_watermark}` 开启双缓冲/多缓冲流式下发，维持 MoE 批次流水。
- **数字 Chiplet**：位于 `simulator/chiplet/digital`，建模 PE/ SPU/ Buffer；`SubmitDescriptor` 接收算子任务描述。
- **RRAM Chiplet**：位于 `simulator/chiplet/rram`，模拟 tile/SA 行为、脉冲统计与误差聚合。
- **命令 ISA**：`linker/kernel/instruction` 增加 `PE_CMD_*`、`RRAM_CMD_*`、`XFER_CMD_SCHEDULE` 等 opcode；`assembler/chiplet_commands.go` 与 `simulator/chiplet/operators` 负责生成高层命令序列。

## Benchmark
- `benchmark=TRANSFORMER`（Chiplet 模式）会触发 `prim.Transformers` 数据准备以及 `assembler.AssembleChipletCommands()` 输出的 Transformer 命令序列。
- 默认序列现在会生成多层 Attention / MoE / SwiGLU Block（默认 6 层），便于在不提供外部规格时快速验证更大的负载。
- 若使用自定义 JSON 模型，可在每个 stage 中通过 `deps: [stage_idx, ...]` 指定依赖的前序 stage（以 0 为起始索引），生成的命令会自动串联这些依赖。
- MoE 线性层支持 `parallel: true` 与 `experts` 数组，每个 expert 块可覆盖 `chiplet`、`activation_bytes`、`weight_bytes`、`execute_latency` 等字段，实现多专家并行映射。
- `tools/generate_chiplet_model.py` 现支持在省略 `--config` 的情况下，通过命令行参数（如 `--layers/--hidden-size`）直接生成规格。
- 提供 `tools/export_pytorch_chiplet.py`，可构造最小 PyTorch Transformer+MoE 模型并输出匹配的 Chiplet 指令规格，便于验证 PyTorch→Chiplet 的映射流程。

## Host Streaming 参数
- `--chiplet_host_stream_total_batches`：Host 侧要发射的批次数；传入 `0`/负数表示按需无限流式（依赖水位线触发）。默认 `1`（关闭流式）。
- `--chiplet_host_stream_low_watermark`：低水位线，活动批次数小于等于该值时补充新批次。
- `--chiplet_host_stream_high_watermark`：高水位线，补批次时的目标上限；典型配置为 `2` 以实现双缓冲。
- `--chiplet_host_limit_resources`：可选开关，打开后 Orchestrator 会按照命令估算激活/权重/互联缓冲占用，超出阈值则等待释放。

## NoC 延迟建模
- **带宽模型（默认）**：根据 `--chiplet_transfer_bw_{dr,rd}` 将互联建模为定带宽通道。
- **BookSim 集成**：若传入 `--chiplet_noc_booksim_enabled 1`，平台会在初始化时启动 `booksim_service` 子进程（可通过 `--chiplet_noc_booksim_binary` 覆盖默认路径），并在 MoE 传输/Host DMA → RRAM 等阶段调用延迟估算器。
  - `--chiplet_noc_booksim_config` 指向 BookSim 拓扑配置，必须保证节点编号与 Chiplet 拓扑一致：数字 Chiplet 从 0 开始，RRAM Chiplet 顺序排在其后。
  - `--chiplet_noc_booksim_timeout_ms` 控制 Go 端的单次 RPC 超时，超时或错误会自动回退到带宽模型，并在日志中提示。
  - 关闭模拟器时 `booksim_service` 会被自动回收，无需手动管理。

## 分析工具
- `tools/chiplet_profiler.py`：解析 `chiplet_log.txt`，输出总结或 JSON 供脚本/可视化使用。
- 运行示例：
  ```bash
  python tools/chiplet_profiler.py /path/to/chiplet_log.txt --json
  ```

## 推荐流程
1. 编译：`python script/build.py`
2. 使用生成脚本创建 Chiplet 指令规格（可选择 HuggingFace 配置，或直接指定参数）：
   ```bash
   # 方式 A：读取 HuggingFace 配置
   python3 tools/generate_chiplet_model.py \
      --config /root/uPIMulator/config/Qwen1.5-MoE-A2.7B/config.json \
      --output /root/uPIMulator/bin/qwen_moe_chiplet.json \
      --seq-length 4096 --batch 1 --digital-chiplets 2 --rram-chiplets 8

   # 方式 B：直接指定参数生成示例规格
   python tools/generate_chiplet_model.py \
      --output <repo>/golang/uPIMulator/bin/demo_chiplet.json \
      --layers 6 --hidden-size 4096 --intermediate-size 11008 \
      --num-experts 16 --experts-per-tok 2 --seq-length 2048

   # 方式 C：构建并运行一个最小 PyTorch Transformer+MoE，并导出规格
   python tools/export_pytorch_chiplet.py \
      --layers 6 --hidden-size 4096 --intermediate-size 11008 \
      --num-experts 16 --experts-per-tok 2 --seq-length 2048 \
      --output <repo>/golang/uPIMulator/bin/pytorch_demo_chiplet.json
   ```
   - `--seq-length`、`--batch` 会影响生成命令的激活/带宽估算，可根据实际测试场景调整。
3. 运行 Chiplet 平台模拟：
   ```bash
   ./build/uPIMulator --platform_mode chiplet --benchmark TRANSFORMER \
      --root_dirpath /root/uPIMulator/ \
      --bin_dirpath /root/uPIMulator/bin \
      --chiplet_model_path /root/uPIMulator/bin/qwen_moe_chiplet.json \
      --chiplet_host_stream_total_batches 0 \
      --chiplet_host_stream_low_watermark 1 \
      --chiplet_host_stream_high_watermark 2 \
      --chiplet_digital_activation_buffer 1073741824 \
      --chiplet_digital_scratch_buffer    1073741824 \
      --chiplet_rram_input_buffer         1073741824 \
      --chiplet_rram_output_buffer        1073741824 \
      --num_tasklets 4 --num_channels 1 --num_ranks_per_channel 1 --num_dpus_per_rank 1 --chiplet_host_dma_ramulator_enabled 1 --chiplet_host_dma_ramulator_config ./golang/ramulator2/resources/hbm2_default.yaml
   ```
   - `--chiplet_host_stream_total_batches 0` 配合水位线参数意味着只要活动批次数降到 1，Host 会自动补充新的批次，实现 MoE 双缓冲。
   - 若未提供 `--chiplet_model_path`，模拟器会回退到内置的 Attention/MoE/SwiGLU 序列。
4. 分析：
   ```bash
   python tools/chiplet_profiler.py \
     <repo>/golang/uPIMulator/bin/chiplet_log.txt \
     --cycle-log <repo>/golang/uPIMulator/bin/chiplet_cycle_log.csv
   ```
   - `chiplet_log.txt` 提供聚合统计（任务数、MAC、RRAM 脉冲、数字 Chiplet 实际 load/store 字节与完成任务总数等），适合做全局指标比对。
   - `chiplet_cycle_log.csv` 记录每周期 `digital_exec/digital_completed/digital_load_bytes/digital_pe_active/...` 等序列，可配合 Notebook 绘制吞吐率与带宽利用率曲线。
