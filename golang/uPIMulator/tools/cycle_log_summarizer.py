#!/usr/bin/env python3
"""
cycle_log_summarizer.py
=======================

Summarize chiplet_cycle_log.csv with optional filters and emit CSV/JSON.
"""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path
from typing import Iterable, Sequence

MANDATORY_COLUMNS = (
    "cycle",
    "digital_exec",
    "digital_completed",
    "transfer_exec",
    "transfer_bytes",
    "digital_util",
)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Summarize chiplet cycle logs.")
    parser.add_argument(
        "--log",
        type=Path,
        default=Path("golang/uPIMulator/bin/chiplet_cycle_log.csv"),
        help="Path to chiplet_cycle_log.csv",
    )
    parser.add_argument(
        "--json",
        type=Path,
        default=None,
        help="Optional JSON output path",
    )
    parser.add_argument(
        "--top",
        type=int,
        default=20,
        help="Show this many highest-utilization cycles",
    )
    parser.add_argument(
        "--filter-transfer",
        action="store_true",
        help="Only consider cycles where transfer_bytes > 0",
    )
    return parser.parse_args(argv)


def load_cycles(path: Path) -> list[dict[str, str]]:
    with path.open("r", encoding="utf-8") as fp:
        reader = csv.DictReader(fp)
        if reader.fieldnames is None:
            raise AssertionError(f"{path} missing header")
        header = reader.fieldnames
        missing = [col for col in MANDATORY_COLUMNS if col not in header]
        if missing:
            raise AssertionError(f"{path} missing columns: {missing}")
        return list(reader)


def summarize(rows: list[dict[str, str]], filter_transfer: bool) -> dict[str, float | int]:
    total_cycles = len(rows)
    digital_exec = sum(int(row["digital_exec"]) for row in rows)
    transfer_bytes = sum(int(row["transfer_bytes"]) for row in rows)
    active_cycles = sum(1 for row in rows if int(row["digital_exec"]) > 0)
    transfer_cycles = sum(1 for row in rows if int(row["transfer_bytes"]) > 0)
    util_avg = (
        sum(float(row["digital_util"]) for row in rows) / total_cycles if total_cycles else 0.0
    )

    filtered = [row for row in rows if not filter_transfer or int(row["transfer_bytes"]) > 0]
    filtered.sort(key=lambda r: float(r["digital_util"]), reverse=True)

    def top_entries(n: int) -> list[dict[str, float | int]]:
        result = []
        for row in filtered[:n]:
            result.append(
                {
                    "cycle": int(row["cycle"]),
                    "digital_util": float(row["digital_util"]),
                    "transfer_bytes": int(row["transfer_bytes"]),
                    "digital_exec": int(row["digital_exec"]),
                }
            )
        return result

    return {
        "total_cycles": total_cycles,
        "digital_exec_samples": digital_exec,
        "active_cycles": active_cycles,
        "transfer_cycles": transfer_cycles,
        "transfer_bytes_total": transfer_bytes,
        "digital_util_avg": util_avg,
        "top_cycles": top_entries(10),
    }


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    rows = load_cycles(args.log)
    summary = summarize(rows, args.filter_transfer)

    if args.json:
        args.json.write_text(json.dumps(summary, indent=2), encoding="utf-8")
        print(f"[cycle_log_summarizer] wrote {args.json}")
    else:
        print(json.dumps(summary, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
