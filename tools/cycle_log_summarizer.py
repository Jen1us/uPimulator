#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
cycle_log_summarizer.py
-----------------------

解析 `chiplet_cycle_log.csv`（即 `ChipletPlatform` 每周期输出的 CSV），生成
简明汇总，包括：
 - 数字/RRAM/传输任务总数
 - 平均每周期传输字节、host DMA 读写字节
 - 互联 throttle 触发次数
 - Digital / RRAM 利用率均值
 - 可选对传输/host DMA 字节的波峰统计

用法示例：
    python tools/cycle_log_summarizer.py \
        --log golang/uPIMulator/bin/chiplet_cycle_log.csv

    python tools/cycle_log_summarizer.py \
        --log chiplet_cycle_log.csv --json --window 1000
"""

from __future__ import annotations

import argparse
import csv
import json
import math
import statistics
from typing import Dict, List, Optional

DEFAULT_FIELDS = [
    "cycle",
    "digital_exec",
    "digital_completed",
    "rram_exec",
    "transfer_exec",
    "transfer_bytes",
    "transfer_hops",
    "host_dma_load_bytes",
    "host_dma_store_bytes",
    "digital_load_bytes",
    "digital_store_bytes",
    "digital_pe_active",
    "digital_spu_active",
    "digital_vpu_active",
    "throttle_until",
    "throttle_events",
    "deferrals",
    "avg_wait",
    "digital_util",
    "rram_util",
    "digital_ticks",
    "rram_ticks",
    "interconnect_ticks",
    "host_tasks",
    "outstanding_digital",
    "outstanding_rram",
    "outstanding_transfer",
    "outstanding_dma",
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="汇总 chiplet_cycle_log.csv 指标")
    parser.add_argument("--log", required=True, help="chiplet_cycle_log.csv 路径")
    parser.add_argument(
        "--json", action="store_true", help="以 JSON 输出汇总，便于后续脚本处理"
    )
    parser.add_argument(
        "--window",
        type=int,
        default=0,
        help="计算移动平均时的窗口（周期数，>0 时启用），用于观察传输/host DMA 字节峰值",
    )
    return parser.parse_args()


def load_csv(path: str) -> List[Dict[str, float]]:
    rows: List[Dict[str, float]] = []
    with open(path, "r", encoding="utf-8") as fp:
        reader = csv.DictReader(fp)
        missing = set(DEFAULT_FIELDS) - set(reader.fieldnames or [])
        if missing:
            raise ValueError(f"缺少列: {', '.join(sorted(missing))}")
        for record in reader:
            rows.append({k: float(v) if v not in ("", None) else math.nan for k, v in record.items()})
    return rows


def moving_peak(values: List[float], window: int) -> float:
    if window <= 0 or not values:
        return max(values) if values else 0.0
    peak = 0.0
    acc = 0.0
    queue: List[float] = []
    for val in values:
        queue.append(val)
        acc += val
        if len(queue) > window:
            acc -= queue.pop(0)
        avg = acc / len(queue)
        peak = max(peak, avg)
    return peak


def summarize(rows: List[Dict[str, float]], window: int) -> Dict[str, float]:
    if not rows:
        return {}

    total_cycles = len(rows)
    sums = {field: 0.0 for field in DEFAULT_FIELDS if field != "cycle"}

    transfer_bytes_series: List[float] = []
    host_load_series: List[float] = []
    host_store_series: List[float] = []
    throttle_events = 0.0

    for row in rows:
        for field in sums:
            value = row.get(field, 0.0)
            if math.isnan(value):
                continue
            sums[field] += value
        transfer_bytes_series.append(row.get("transfer_bytes", 0.0))
        host_load_series.append(row.get("host_dma_load_bytes", 0.0))
        host_store_series.append(row.get("host_dma_store_bytes", 0.0))
        throttle_events += row.get("throttle_events", 0.0)

    result: Dict[str, float] = {
        "cycles": float(total_cycles),
        "digital_exec_total": sums["digital_exec"],
        "digital_completed_total": sums["digital_completed"],
        "rram_exec_total": sums["rram_exec"],
        "transfer_exec_total": sums["transfer_exec"],
        "transfer_bytes_total": sums["transfer_bytes"],
        "host_dma_load_bytes_total": sums["host_dma_load_bytes"],
        "host_dma_store_bytes_total": sums["host_dma_store_bytes"],
        "avg_digital_util": sums["digital_util"] / total_cycles,
        "avg_rram_util": sums["rram_util"] / total_cycles,
        "avg_transfer_bytes_per_cycle": sums["transfer_bytes"] / total_cycles,
        "avg_host_dma_load_bytes_per_cycle": sums["host_dma_load_bytes"] / total_cycles,
        "avg_host_dma_store_bytes_per_cycle": sums["host_dma_store_bytes"] / total_cycles,
        "throttle_events_total": throttle_events,
    }

    if transfer_bytes_series:
        result["transfer_peak_avg"] = moving_peak(transfer_bytes_series, window)
    if host_load_series:
        result["host_load_peak_avg"] = moving_peak(host_load_series, window)
    if host_store_series:
        result["host_store_peak_avg"] = moving_peak(host_store_series, window)

    if sum(transfer_bytes_series) > 0:
        result["transfer_bytes_per_exec"] = (
            sums["transfer_bytes"] / sums["transfer_exec"] if sums["transfer_exec"] else 0.0
        )
    return result


def print_summary(stats: Dict[str, float]) -> None:
    if not stats:
        print("未读取到 cycle log 数据。")
        return

    print("=== chiplet cycle log summary ===")
    print(f"  总周期数:                 {int(stats['cycles'])}")
    print(f"  数字任务执行总数:         {int(stats['digital_exec_total'])}")
    print(f"  数字任务完成总数:         {int(stats['digital_completed_total'])}")
    print(f"  RRAM 任务执行总数:        {int(stats['rram_exec_total'])}")
    print(f"  传输任务执行总数:         {int(stats['transfer_exec_total'])}")
    print(f"  总传输字节:               {int(stats['transfer_bytes_total']):,}")
    print(f"  总 Host->Digital 字节:    {int(stats['host_dma_load_bytes_total']):,}")
    print(f"  总 Digital->Host 字节:    {int(stats['host_dma_store_bytes_total']):,}")
    print(f"  平均每周期传输字节:       {stats['avg_transfer_bytes_per_cycle']:.2f}")
    print(f"  平均每周期 Host2D 字节:   {stats['avg_host_dma_load_bytes_per_cycle']:.2f}")
    print(f"  平均每周期 D2Host 字节:   {stats['avg_host_dma_store_bytes_per_cycle']:.2f}")
    print(f"  数字利用率平均值:         {stats['avg_digital_util']:.4f}")
    print(f"  RRAM 利用率平均值:        {stats['avg_rram_util']:.4f}")
    print(f"  节流事件累计:             {int(stats['throttle_events_total'])}")
    if "transfer_peak_avg" in stats:
        print(f"  传输窗口峰值(平均):       {stats['transfer_peak_avg']:.2f}")
    if "host_load_peak_avg" in stats:
        print(f"  Host2D 窗口峰值(平均):    {stats['host_load_peak_avg']:.2f}")
    if "host_store_peak_avg" in stats:
        print(f"  D2Host 窗口峰值(平均):    {stats['host_store_peak_avg']:.2f}")
    if "transfer_bytes_per_exec" in stats:
        print(f"  平均每次传输字节:         {stats['transfer_bytes_per_exec']:.2f}")


def main() -> None:
    args = parse_args()
    rows = load_csv(args.log)
    stats = summarize(rows, args.window)
    if args.json:
        print(json.dumps(stats, indent=2, ensure_ascii=False))
    else:
        print_summary(stats)


if __name__ == "__main__":
    main()

