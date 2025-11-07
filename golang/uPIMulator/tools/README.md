# Chiplet Tools

本目录聚合了与 Chiplet 模拟器相关的辅助脚本，便于相同入口下查看和调用：

| 脚本 | 作用 |
| --- | --- |
| `generate_chiplet_model.py` | 根据 HuggingFace/PyTorch 配置生成 chiplet_model 规格 JSON。 |
| `export_pytorch_chiplet.py` | 构建最小 PyTorch Transformer+MoE 并导出 Chiplet 规格。 |
| `chiplet_profiler.py` | 解析 `chiplet_log.txt` / `chiplet_cycle_log.csv` 输出汇总统计。 |
| `cycle_log_summarizer.py` | 对 `chiplet_cycle_log.csv` 生成 JSON 概览（平均利用率、Top cycles 等）。 |
| `chiplet_regression.py` | 运行目录回归检查：存在性、transfer/host DMA/streaming、results CSV 等。 |

使用示例：

```bash
python tools/chiplet_regression.py \
  --run-dir golang/uPIMulator/bin \
  --require-transfer --require-host-dma --require-streaming

python tools/cycle_log_summarizer.py \
  --log golang/uPIMulator/bin/chiplet_cycle_log.csv \
  --json golang/uPIMulator/bin/cycle_summary.json
```
