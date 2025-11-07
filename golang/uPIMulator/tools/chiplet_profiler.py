#!/usr/bin/env python3
"""Chiplet profiler: summarize chiplet_log.txt metrics and cycle traces."""

import argparse
import csv
import json
import re
from collections import defaultdict

ENTRY_RE = re.compile(r"^(?P<key>[^:]+):\s+(?P<value>.+)$")
DIGITAL_RE = re.compile(r"^DigitalChiplet\[(?P<id>\d+)\]_(?P<field>.+)$")
RRAM_RE = re.compile(r"^RramChiplet\[(?P<id>\d+)\]_(?P<field>.+)$")


def parse_value(raw: str):
    raw = raw.strip()
    try:
        if raw.startswith("0x"):
            return int(raw, 16)
        if "." in raw:
            return float(raw)
        return int(raw)
    except ValueError:
        return raw


def parse_log(path: str):
    summary = {}
    digital = defaultdict(dict)
    rram = defaultdict(dict)

    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            match = ENTRY_RE.match(line)
            if not match:
                continue
            key = match.group("key")
            value = parse_value(match.group("value"))
            d_match = DIGITAL_RE.match(key)
            if d_match:
                idx = int(d_match.group("id"))
                field = d_match.group("field")
                digital[idx][field] = value
                continue
            r_match = RRAM_RE.match(key)
            if r_match:
                idx = int(r_match.group("id"))
                field = r_match.group("field")
                rram[idx][field] = value
                continue
            summary[key] = value

    # convert defaultdict to regular dicts for downstream processing
    digital_s = {idx: dict(fields) for idx, fields in digital.items()}
    rram_s = {idx: dict(fields) for idx, fields in rram.items()}
    return summary, digital_s, rram_s


def aggregate(summary, digital, rram):
    digital_list = [{"id": idx, **fields} for idx, fields in sorted(digital.items())]
    rram_list = [{"id": idx, **fields} for idx, fields in sorted(rram.items())]

    result = {
        "summary": summary,
        "digital": digital_list,
        "rram": rram_list,
    }

    if digital_list:
        total_macs = sum(chip.get("macs_total", 0) for chip in digital_list)
        total_pe_busy = 0
        for chip in digital_list:
            for key, value in chip.items():
                if isinstance(key, str) and key.startswith("pe[") and key.endswith("busy_cycles"):
                    total_pe_busy += value
        result["summary"].setdefault("aggregated", {})
        result["summary"]["aggregated"].update({
            "digital_total_macs": total_macs,
            "digital_total_pe_busy_cycles": total_pe_busy,
        })
    if rram_list:
        pulses = sum(chip.get("pulse_count", 0) for chip in rram_list)
        adc = sum(chip.get("adc_samples", 0) for chip in rram_list)
        result["summary"].setdefault("aggregated", {})
        result["summary"]["aggregated"].update({
            "rram_total_pulse_count": pulses,
            "rram_total_adc_samples": adc,
        })

    if "ChipletPlatform_digital_load_bytes_runtime_total" in summary:
        result["summary"].setdefault("aggregated", {})
        result["summary"]["aggregated"]["digital_load_bytes_runtime_total"] = summary.get(
            "ChipletPlatform_digital_load_bytes_runtime_total", 0
        )
        result["summary"]["aggregated"]["digital_store_bytes_runtime_total"] = summary.get(
            "ChipletPlatform_digital_store_bytes_runtime_total", 0
        )
        result["summary"]["aggregated"]["digital_tasks_completed_total"] = summary.get(
            "ChipletPlatform_digital_tasks_completed_total", 0
        )

    return result


def parse_cycle_log(path: str):
    if not path:
        return []
    rows = []
    with open(path, "r", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            parsed = {}
            for key, value in row.items():
                value = value.strip()
                if value == "":
                    parsed[key] = 0
                    continue
                try:
                    if "." in value:
                        parsed[key] = float(value)
                    else:
                        parsed[key] = int(value)
                except ValueError:
                    parsed[key] = value
            rows.append(parsed)
    return rows


def summarize_cycle_log(rows):
    if not rows:
        return {}
    summary = {}
    total_cycles = len(rows)
    fields = [
        "digital_exec",
        "digital_completed",
        "rram_exec",
        "transfer_exec",
        "transfer_bytes",
        "digital_load_bytes",
        "digital_store_bytes",
        "digital_pe_active",
        "digital_spu_active",
        "throttle_events",
        "deferrals",
    ]
    for field in fields:
        summary[f"{field}_sum"] = sum(row.get(field, 0) for row in rows)
        summary[f"{field}_avg"] = summary[f"{field}_sum"] / total_cycles
    summary["cycles"] = total_cycles
    return summary


def main():
    parser = argparse.ArgumentParser(description="Summarize chiplet logs and cycle traces")
    parser.add_argument("log", help="Path to chiplet_log.txt")
    parser.add_argument("--cycle-log", help="Path to chiplet_cycle_log.csv", default=None)
    parser.add_argument("--json", action="store_true", help="Print JSON summary")
    args = parser.parse_args()

    summary, digital, rram = parse_log(args.log)
    data = aggregate(summary, digital, rram)
    cycle_rows = parse_cycle_log(args.cycle_log)
    cycle_summary = summarize_cycle_log(cycle_rows)
    if cycle_summary:
        data["cycle_summary"] = cycle_summary

    if args.json:
        print(json.dumps(data, indent=2, sort_keys=True))
        return

    summary_map = data["summary"]
    digital_list = data["digital"]
    rram_list = data["rram"]

    print("== Chiplet Summary ==")
    for key, value in sorted(summary_map.items()):
        if key == "aggregated":
            continue
        print(f"{key}: {value}")
    if "aggregated" in summary_map:
        print("\nAggregated:")
        for key, value in sorted(summary_map["aggregated"].items()):
            print(f"  {key}: {value}")

    if digital_list:
        print("\n== Digital Chiplets ==")
        for chip in digital_list:
            idx = chip.get("id")
            print(f"Chiplet {idx}:")
            for key, value in sorted(chip.items()):
                if key == "id":
                    continue
                print(f"  {key}: {value}")

    if rram_list:
        print("\n== RRAM Chiplets ==")
        for chip in rram_list:
            idx = chip.get("id")
            print(f"Chiplet {idx}:")
            for key, value in sorted(chip.items()):
                if key == "id":
                    continue
                print(f"  {key}: {value}")

    if cycle_summary:
        print("\n== Cycle Summary ==")
        for key, value in sorted(cycle_summary.items()):
            print(f"{key}: {value}")


if __name__ == "__main__":
    main()
