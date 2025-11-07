#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
transfer_debug_analyzer.py
--------------------------

读取 Chiplet 模式运行产生的 `run_debug.log`，解析形如
`[chiplet-debug] transfer stage=...` 的调试行，输出各类传输的统计信息。

用法示例：
    python tools/transfer_debug_analyzer.py \
        --log golang/uPIMulator/run_debug.log

    python tools/transfer_debug_analyzer.py \
        --log run_debug.log --stage transfer_to_rram --json

默认输出汇总表；若使用 `--json`，则打印 JSON，便于后续脚本处理。
"""

from __future__ import annotations

import argparse
import collections
import json
import os
import re
import statistics
from typing import Dict, Iterable, List, Optional, Tuple

TRANSFER_RE = re.compile(
    r"\[chiplet-debug\]\s+transfer\s+stage=(?P<stage>\w+)\s+bytes=(?P<bytes>\d+)\s+"
    r"hops=(?P<hops>\d+)\s+srcDigital=(?P<src_dig>-?\d+)\s+dstDigital=(?P<dst_dig>-?\d+)"
    r"\s+srcRram=(?P<src_rram>-?\d+)\s+dstRram=(?P<dst_rram>-?\d+)"
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="解析 run_debug.log 中的 transfer 调试信息")
    parser.add_argument(
        "--log",
        required=True,
        help="run_debug.log 路径",
    )
    parser.add_argument(
        "--stage",
        default=None,
        help="仅统计指定 stage（transfer_to_rram/transfer_to_digital/transfer_host2d/transfer_d2host 等）",
    )
    parser.add_argument(
        "--src-digital",
        type=int,
        nargs="*",
        help="筛选源数字 chiplet ID，可传多个",
    )
    parser.add_argument(
        "--dst-digital",
        type=int,
        nargs="*",
        help="筛选目标数字 chiplet ID，可传多个",
    )
    parser.add_argument(
        "--src-rram",
        type=int,
        nargs="*",
        help="筛选源 RRAM chiplet ID，可传多个",
    )
    parser.add_argument(
        "--dst-rram",
        type=int,
        nargs="*",
        help="筛选目标 RRAM chiplet ID，可传多个",
    )
    parser.add_argument(
        "--min-bytes",
        type=int,
        default=0,
        help="仅统计 payload 字节 ≥ 此阈值的传输",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="以 JSON 输出结果，便于后续脚本处理",
    )
    parser.add_argument(
        "--top",
        type=int,
        default=5,
        help="按字节量列出 stage 内最多的源/目的对数，默认 5",
    )
    return parser.parse_args()


def read_transfers(path: str) -> Iterable[Dict[str, int]]:
    if not os.path.isfile(path):
        raise FileNotFoundError(f"log file not found: {path}")

    with open(path, "r", encoding="utf-8", errors="ignore") as fp:
        for line in fp:
            match = TRANSFER_RE.search(line)
            if not match:
                continue
            info = {k: int(v) if k != "stage" else v for k, v in match.groupdict().items()}
            yield info


def matches_filters(record: Dict[str, int], args: argparse.Namespace) -> bool:
    if args.stage and record["stage"] != args.stage:
        return False
    if args.min_bytes and record["bytes"] < args.min_bytes:
        return False

    def match_list(values: Optional[List[int]], field: str) -> bool:
        if not values:
            return True
        return record[field] in values

    if not match_list(args.src_digital, "src_dig"):
        return False
    if not match_list(args.dst_digital, "dst_dig"):
        return False
    if not match_list(args.src_rram, "src_rram"):
        return False
    if not match_list(args.dst_rram, "dst_rram"):
        return False
    return True


def summarize(transfers: Iterable[Dict[str, int]], args: argparse.Namespace) -> Dict[str, dict]:
    stage_stats: Dict[str, dict] = {}
    for record in transfers:
        stage = record["stage"]
        if not matches_filters(record, args):
            continue

        stats = stage_stats.setdefault(
            stage,
            {
                "count": 0,
                "total_bytes": 0,
                "hops": [],
                "by_endpoint": collections.Counter(),
            },
        )
        stats["count"] += 1
        stats["total_bytes"] += record["bytes"]
        stats["hops"].append(record["hops"])
        endpoint = (record["src_dig"], record["dst_dig"], record["src_rram"], record["dst_rram"])
        stats["by_endpoint"][endpoint] += record["bytes"]

    # 计算平均 hop 以及端点排序
    for stage, stats in stage_stats.items():
        hops = stats["hops"]
        stats["avg_hops"] = statistics.mean(hops) if hops else 0.0
        stats["max_hops"] = max(hops) if hops else 0
        stats["min_hops"] = min(hops) if hops else 0
        top_pairs: List[Tuple[Tuple[int, int, int, int], int]] = stats["by_endpoint"].most_common()
        stats["top_endpoints"] = [
            {
                "src_digital": ep[0],
                "dst_digital": ep[1],
                "src_rram": ep[2],
                "dst_rram": ep[3],
                "bytes": byte_cnt,
            }
            for ep, byte_cnt in top_pairs
        ]
        # 删除临时字段
        del stats["hops"]
        del stats["by_endpoint"]
    return stage_stats


def print_table(stats: Dict[str, dict], top_k: int) -> None:
    if not stats:
        print("未找到符合条件的 transfer 行。")
        return

    header = (
        f"{'Stage':<22} {'Count':>8} {'Bytes':>14} {'AvgHop':>8} "
        f"{'MinHop':>8} {'MaxHop':>8}"
    )
    print(header)
    print("-" * len(header))
    for stage, info in sorted(stats.items()):
        print(
            f"{stage:<22} {info['count']:>8d} {info['total_bytes']:>14,d} "
            f"{info['avg_hops']:>8.2f} {info['min_hops']:>8d} {info['max_hops']:>8d}"
        )
        top_list = info.get("top_endpoints", [])[:top_k]
        for idx, endpoint in enumerate(top_list, 1):
            print(
                f"    #{idx:<2d} bytes={endpoint['bytes']:,} | "
                f"src_dig={endpoint['src_digital']}, dst_dig={endpoint['dst_digital']}, "
                f"src_rram={endpoint['src_rram']}, dst_rram={endpoint['dst_rram']}"
            )
        if not top_list:
            print("    (no endpoint data)")


def main() -> None:
    args = parse_args()
    transfers = list(read_transfers(args.log))
    stats = summarize(transfers, args)
    if args.json:
        top_k = max(args.top, 0)
        trimmed = {}
        for stage, info in stats.items():
            trimmed[stage] = dict(info)
            if top_k >= 0:
                trimmed[stage]["top_endpoints"] = info.get("top_endpoints", [])[:top_k]
        print(json.dumps(trimmed, indent=2, ensure_ascii=False))
    else:
        print_table(stats, args.top)


if __name__ == "__main__":
    main()
