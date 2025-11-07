#!/usr/bin/env python3
"""
chiplet_regression.py
=====================

Lightweight regression checker for Chiplet simulator runs.

The script scans a run directory (default: golang/uPIMulator/bin) and verifies
that key artifacts exist and contain reasonable values:

* chiplet_cycle_log.csv – must include mandatory columns and report at least one
  cycle with digital execution and (optionally) transfer bytes.
* run_debug.log – should contain debug lines that prove host DMA / streaming
  paths ran (configurable via flags).
* chiplet_results.csv – validated for non-empty numeric entries.

Usage examples:

    python tools/chiplet_regression.py \
        --run-dir golang/uPIMulator/bin \
        --require-transfer \
        --require-host-dma
"""

from __future__ import annotations

import argparse
import csv
import sys
from pathlib import Path
from typing import Iterable, Sequence

# Required CSV columns for cycle log sanity checks.
MANDATORY_CYCLE_COLUMNS: tuple[str, ...] = (
    "cycle",
    "digital_exec",
    "digital_completed",
    "transfer_exec",
    "transfer_bytes",
    "digital_util",
)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Validate Chiplet simulator outputs for regression testing."
    )
    parser.add_argument(
        "--run-dir",
        type=Path,
        default=Path("golang/uPIMulator/bin"),
        help="Directory containing chiplet_{cycle,log,results} files.",
    )
    parser.add_argument(
        "--require-transfer",
        action="store_true",
        help="Fail if no cycle reports transfer bytes > 0.",
    )
    parser.add_argument(
        "--require-host-dma",
        action="store_true",
        help="Fail if run_debug.log lacks host dma markers (transfer_host2d/transfer_d2host).",
    )
    parser.add_argument(
        "--require-streaming",
        action="store_true",
        help="Fail if run_debug.log lacks stream_batch_id metadata.",
    )
    return parser.parse_args(argv)


def load_csv(path: Path) -> list[dict[str, str]]:
    with path.open("r", encoding="utf-8") as fp:
        reader = csv.DictReader(fp)
        if reader.fieldnames is None:
            raise AssertionError(f"{path} has no header")
        return list(reader)


def check_cycle_log(path: Path, require_transfer: bool) -> None:
    rows = load_csv(path)
    if not rows:
        raise AssertionError(f"{path} is empty")

    header = rows[0].keys()
    missing = [col for col in MANDATORY_CYCLE_COLUMNS if col not in header]
    if missing:
        raise AssertionError(f"{path} missing columns: {missing}")

    digital_exec_any = any(int(row["digital_exec"]) > 0 for row in rows)
    if not digital_exec_any:
        raise AssertionError(f"{path} never reports digital_exec > 0")

    if require_transfer:
        transfer_any = any(int(row["transfer_bytes"]) > 0 for row in rows)
        if not transfer_any:
            raise AssertionError(f"{path} never reports transfer bytes > 0")


def check_results_csv(path: Path) -> None:
    rows = load_csv(path)
    if not rows:
        raise AssertionError(f"{path} is empty")
    required_cols = {"cycle", "chiplet_id"}
    header = rows[0].keys()
    if not required_cols.issubset(header):
        raise AssertionError(
            f"{path} missing columns: {sorted(required_cols - set(header))}"
        )


def check_run_debug(path: Path, require_host_dma: bool, require_streaming: bool) -> None:
    contents = path.read_text(encoding="utf-8", errors="ignore")
    if not contents.strip():
        raise AssertionError(f"{path} is empty")

    if require_host_dma:
        if "transfer_host2d" not in contents and "transfer_d2host" not in contents:
            raise AssertionError(
                f"{path} missing host DMA markers (transfer_host2d/transfer_d2host)"
            )

    if require_streaming:
        if "stream_batch_id" not in contents:
            raise AssertionError(
                f"{path} missing stream_batch_id metadata (streaming not exercised?)"
            )


def validate(run_dir: Path, args: argparse.Namespace) -> None:
    artifacts = {
        "cycle_log": run_dir / "chiplet_cycle_log.csv",
        "results": run_dir / "chiplet_results.csv",
        "run_debug": run_dir.parent / "run_debug.log"
        if (run_dir / "run_debug.log").exists()
        else run_dir / "run_debug.log",
    }

    missing = [name for name, path in artifacts.items() if not path.exists()]
    if missing:
        details = ", ".join(f"{name}:{artifacts[name]}" for name in missing)
        raise FileNotFoundError(f"Missing artifacts: {details}")

    check_cycle_log(artifacts["cycle_log"], args.require_transfer)
    check_results_csv(artifacts["results"])
    check_run_debug(
        artifacts["run_debug"],
        require_host_dma=args.require_host_dma,
        require_streaming=args.require_streaming,
    )


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        validate(args.run_dir, args)
    except AssertionError as exc:
        print(f"[chiplet-regression] FAIL: {exc}", file=sys.stderr)
        return 1
    except FileNotFoundError as exc:
        print(f"[chiplet-regression] ERROR: {exc}", file=sys.stderr)
        return 2

    print("[chiplet-regression] PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
