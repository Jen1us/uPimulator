# Chiplet 性能/能耗分析指引

本文件演示如何基于现有日志快速生成 Mesh 负载、Host DMA 与 RRAM 能耗的摘要结果，并可在 CI/回归中自动校验。

## 1. 生成日志

1. 按 `docs/chiplet_architecture.md` 中步骤运行 Chiplet 模拟（确保启用 `--chiplet_host_stream_*` 以测试 streaming 场景）。
2. 运行结束后，确认下列输出存在：
   - `golang/uPIMulator/bin/chiplet_cycle_log.csv`
   - `golang/uPIMulator/bin/chiplet_log.txt`
   - `golang/uPIMulator/log_dump.json`
   - `golang/uPIMulator/run_debug.log`

## 2. 快速回归检查

```bash
python tools/chiplet_regression.py \
  --run-dir golang/uPIMulator/bin \
  --require-transfer --require-host-dma --require-streaming
```

该脚本会验证 cycle log / results / run_debug 的存在性以及：
- 是否至少有一次 transfer 字节；
- run_debug 中是否出现 `transfer_host2d`/`transfer_d2host` 与 `stream_batch_id` 字段；
- `chiplet_results.csv` 格式正确。

## 3. 性能/能耗摘要

```bash
python tools/chiplet_perf_analyzer.py \
  --log-dump golang/uPIMulator/log_dump.json \
  --cycle-log golang/uPIMulator/bin/chiplet_cycle_log.csv \
  --json perf_summary.json
```

输出示例：
```json
{
  "cycles": 5000.0,
  "digital_tasks_total": 3.0,
  "mesh_bandwidth_bytes_per_cycle": 13.1,
  "rram_energy_nj": 0.0246,
  "host_dma_energy_nj": 0.0,
  "peak_transfer_cycle": 2,
  "peak_transfer_bytes": 32768
}
```

该摘要包含：
- `digital_tasks_per_cycle`：用以衡量 pipeline 吞吐；
- `mesh_bandwidth_bytes_per_cycle`：基于总 transfer 字节估算平均带宽；
- `rram_energy_nj`：依据脉冲/ADC 采样数与默认能耗模型（2.5pJ/脉冲、0.8pJ/采样）推算；
- `host_dma_energy_nj`：按照 0.2pJ/Byte 估算 Host DMA 消耗；
- `peak_transfer_cycle`：来自 cycle log 的峰值传输周期，可用于 mesh/拥塞分析；
- streaming/host DMA/KV cache 计数由 `log_dump.json` 提供，可用于 MoE 延迟、KV 命中率的后续分析。

## 4. 可视化 / 后处理建议

- 将 `cycle_log_summarizer.py` 与 `chiplet_perf_analyzer.py` 的输出导入 Notebook，绘制数字/传输利用率趋势或 RRAM 能耗分布。
- 对比多次运行的 `perf_summary.json` 可快速定位 Host DMA 与 mesh 带宽瓶颈。
- 如需更精细的能耗模型，可在 `chiplet_perf_analyzer.py` 内调整 `RRAM_PULSE_ENERGY_PJ`、`HOST_DMA_ENERGY_PJ_PER_BYTE` 等常数。
