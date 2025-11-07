#!/usr/bin/env python3
"""
chiplet_perf_analyzer.py
=========================

Compute a compact performance / energy summary from Chiplet simulator outputs.
The script consumes `log_dump.json` (aggregated counters) and optionally
`chiplet_cycle_log.csv` to extract peak transfer statistics.

Example:

    python tools/chiplet_perf_analyzer.py \
        --log-dump golang/uPIMulator/log_dump.json \
        --cycle-log golang/uPIMulator/bin/chiplet_cycle_log.csv
"""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path
from typing import Sequence

MANDATORY_DUMP_KEYS = (
    "ChipletPlatform_cycles",
    "ChipletPlatform_transfer_bytes_total",
    "ChipletPlatform_digital_tasks_total",
    "ChipletPlatform_digital_domain_cycles",
)

RRAM_PULSE_ENERGY_PJ = 2.5
RRAM_ADC_ENERGY_PJ = 0.8
HOST_DMA_ENERGY_PJ_PER_BYTE = 0.2


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Chiplet performance/energy summary")
    parser.add_argument(
        "--log-dump",
        type=Path,
        default=Path("golang/uPIMulator/log_dump.json"),
        help="Path to log_dump.json",
    )
    parser.add_argument(
        "--cycle-log",
        type=Path,
        default=None,
        help="Optional chiplet_cycle_log.csv for peak transfer stats",
    )
    parser.add_argument(
        "--json",
        type=Path,
        default=None,
        help="Optional output path (JSON). Default: print to stdout",
    )
    return parser.parse_args(argv)


def load_log_dump(path: Path) -> dict[str, float]:
    data = json.loads(path.read_text(encoding="utf-8"))
    entries = data.get("entries", [])
    stats: dict[str, float] = {}
    for entry in entries:
        for key, value in entry.items():
            stats[key] = parse_float(value)
    for key in MANDATORY_DUMP_KEYS:
        if key not in stats:
            raise AssertionError(f"log_dump.json missing key: {key}")
    return stats


def parse_float(value) -> float:
    if value is None:
        return 0.0
    if isinstance(value, (int, float)):
        return float(value)
    try:
        return float(str(value))
    except ValueError:
        return 0.0


def summarize_cycles(path: Path) -> dict[str, float]:
    if not path:
        return {}
    if not path.exists():
        return {}
    with path.open("r", encoding="utf-8") as fp:
        reader = csv.DictReader(fp)
        max_transfer = 0
        peak_cycle = 0
        for row in reader:
            try:
                transfer_bytes = int(row.get("transfer_bytes", 0))
            except ValueError:
                transfer_bytes = 0
            if transfer_bytes > max_transfer:
                max_transfer = transfer_bytes
                peak_cycle = int(row.get("cycle", 0))
    return {"peak_transfer_cycle": peak_cycle, "peak_transfer_bytes": max_transfer}


def compute_summary(stats: dict[str, float], cycle_stats: dict[str, float]) -> dict[str, float]:
    cycles = stats.get("ChipletPlatform_cycles", 0.0)
    digital_tasks = stats.get("ChipletPlatform_digital_tasks_total", 0.0)
    digital_cycles = stats.get("ChipletPlatform_digital_domain_cycles", 0.0)
    throughput = digital_tasks / digital_cycles if digital_cycles else 0.0

    transfer_bytes = stats.get("ChipletPlatform_transfer_bytes_total", 0.0)
    mesh_bandwidth_bytes_per_cycle = transfer_bytes / cycles if cycles else 0.0

    pulses = stats.get("ChipletPlatform_rram_pulse_count_total", 0.0)
    adc_samples = stats.get("ChipletPlatform_rram_adc_samples_total", 0.0)
    rram_energy_nj = pulses * RRAM_PULSE_ENERGY_PJ * 1e-3 + adc_samples * RRAM_ADC_ENERGY_PJ * 1e-3

    host_dma_load = stats.get("ChipletPlatform_host_dma_load_bytes_total", 0.0)
    host_dma_store = stats.get("ChipletPlatform_host_dma_store_bytes_total", 0.0)
    host_dma_energy_nj = (host_dma_load + host_dma_store) * HOST_DMA_ENERGY_PJ_PER_BYTE * 1e-3

    summary = {
        "cycles": cycles,
        "digital_tasks_total": digital_tasks,
        "digital_tasks_per_cycle": throughput,
        "transfer_bytes_total": transfer_bytes,
        "mesh_bandwidth_bytes_per_cycle": mesh_bandwidth_bytes_per_cycle,
        "rram_energy_nj": rram_energy_nj,
        "host_dma_energy_nj": host_dma_energy_nj,
        "host_dma_load_bytes": host_dma_load,
        "host_dma_store_bytes": host_dma_store,
        "kv_cache_hits": stats.get("ChipletPlatform_kv_cache_hits_total", 0.0),
        "kv_cache_misses": stats.get("ChipletPlatform_kv_cache_misses_total", 0.0),
        "stream_batches_issued": stats.get("ChipletPlatform_stream_batches_issued_total", 0.0),
        "stream_active_peak": stats.get("ChipletPlatform_stream_active_peak", 0.0),
        "moe_latency_total_cycles": stats.get("ChipletPlatform_moe_latency_total_cycles", 0.0),
    }
    summary.update(cycle_stats)
    return summary


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    stats = load_log_dump(args.log_dump)
    cycle_stats = summarize_cycles(args.cycle_log) if args.cycle_log else {}
    summary = compute_summary(stats, cycle_stats)

    output = json.dumps(summary, indent=2)
    if args.json:
        args.json.write_text(output, encoding="utf-8")
        print(f"[chiplet-perf] wrote {args.json}")
    else:
        print(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
