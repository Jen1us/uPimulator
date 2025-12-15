#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import math
from pathlib import Path


def _escape(text: str) -> str:
    return (
        text.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
        .replace("'", "&#39;")
    )


def _nice_max(value: int, step: int) -> int:
    if value <= 0:
        return step
    return int(math.ceil(value / step) * step)


def render_svg(
    results_path: Path,
    output_path: Path,
    width: int = 960,
    height: int = 520,
) -> None:
    data = json.loads(results_path.read_text(encoding="utf-8"))
    results = data.get("results", [])
    if not results:
        raise SystemExit("no results found in JSON")

    labels = [r.get("label") or r.get("topology") or "" for r in results]
    values = [int(r.get("cycles", 0) or 0) for r in results]
    max_value = max(values) if values else 0

    margin_left = 90
    margin_right = 30
    margin_top = 80
    margin_bottom = 140
    chart_left = margin_left
    chart_right = width - margin_right
    chart_top = margin_top
    chart_bottom = height - margin_bottom
    chart_w = chart_right - chart_left
    chart_h = chart_bottom - chart_top

    tick_step = 1000
    y_max = _nice_max(max_value, tick_step)
    if y_max < tick_step:
        y_max = tick_step

    # Excel-ish grayscale palette (best -> darkest, worst -> lightest).
    fills = ["#404040", "#595959", "#737373", "#8C8C8C", "#A6A6A6", "#BFBFBF", "#D9D9D9"]
    if len(values) > len(fills):
        fills = (fills * (len(values) // len(fills) + 1))[: len(values)]

    font_stack = "-apple-system,BlinkMacSystemFont,Segoe UI,Arial"
    title = "不同 NoC 拓扑下的 Chiplet 总周期对比（黑白图）"
    subtitle = "指标：ChipletPlatform_cycles（越小越好）；工作负载：内置 Attention + MoE + SwiGLU；BookSim2 估算互联延迟"

    svg = []
    svg.append(f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">')
    svg.append("<style>")
    svg.append(f'.title{{font:700 20px {font_stack};fill:#111}}')
    svg.append(f'.subtitle{{font:400 13px {font_stack};fill:#333}}')
    svg.append(f'.axis{{stroke:#000;stroke-width:1}}')
    svg.append(f'.grid{{stroke:#d0d0d0;stroke-width:1}}')
    svg.append(f'.tick{{font:12px {font_stack};fill:#111}}')
    svg.append(f'.label{{font:12px {font_stack};fill:#111}}')
    svg.append(f'.value{{font:12px {font_stack};fill:#111}}')
    svg.append("</style>")

    svg.append(f'<rect x="0" y="0" width="{width}" height="{height}" fill="#fff"/>')
    svg.append(f'<text class="title" x="{margin_left}" y="34">{_escape(title)}</text>')
    svg.append(f'<text class="subtitle" x="{margin_left}" y="56">{_escape(subtitle)}</text>')

    # Axes
    svg.append(f'<line class="axis" x1="{chart_left}" y1="{chart_top}" x2="{chart_left}" y2="{chart_bottom}"/>')
    svg.append(f'<line class="axis" x1="{chart_left}" y1="{chart_bottom}" x2="{chart_right}" y2="{chart_bottom}"/>')

    # Y grid + ticks
    for t in range(0, y_max + 1, tick_step):
        y = chart_bottom - (t / y_max) * chart_h
        svg.append(f'<line class="grid" x1="{chart_left}" y1="{y:.2f}" x2="{chart_right}" y2="{y:.2f}"/>')
        svg.append(
            f'<text class="tick" x="{chart_left - 10}" y="{y + 4:.2f}" text-anchor="end">{t}</text>'
        )

    # Bars
    n = len(values)
    slot_w = chart_w / n
    bar_w = slot_w * 0.7
    for i, (label, val) in enumerate(zip(labels, values, strict=True)):
        x = chart_left + i * slot_w + (slot_w - bar_w) / 2
        bar_h = 0 if y_max == 0 else (val / y_max) * chart_h
        y = chart_bottom - bar_h
        fill = fills[i]
        svg.append(
            f'<rect x="{x:.2f}" y="{y:.2f}" width="{bar_w:.2f}" height="{bar_h:.2f}" fill="{fill}" stroke="#000" stroke-width="1"/>'
        )
        svg.append(
            f'<text class="value" x="{x + bar_w / 2:.2f}" y="{y - 6:.2f}" text-anchor="middle">{val}</text>'
        )
        label_x = x + bar_w / 2
        label_y = chart_bottom + 36
        svg.append(
            f'<text class="label" x="{label_x:.2f}" y="{label_y:.2f}" text-anchor="end" transform="rotate(-45 {label_x:.2f} {label_y:.2f})">{_escape(label)}</text>'
        )

    svg.append("</svg>")
    output_path.write_text("\n".join(svg) + "\n", encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description="Render NoC topology cycle results as an SVG bar chart.")
    parser.add_argument(
        "--results",
        default="docs/noc_topology_experiment_results.json",
        help="Path to the JSON results file",
    )
    parser.add_argument(
        "--out",
        default="assets/noc_topology_cycles.svg",
        help="Path to the output SVG file",
    )
    parser.add_argument("--width", type=int, default=960)
    parser.add_argument("--height", type=int, default=520)
    args = parser.parse_args()

    render_svg(Path(args.results), Path(args.out), width=args.width, height=args.height)


if __name__ == "__main__":
    main()

