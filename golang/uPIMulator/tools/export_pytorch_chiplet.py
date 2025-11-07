#!/usr/bin/env python3
"""
export_pytorch_chiplet.py
=========================

构建一个最小的 PyTorch Transformer+MoE 模型，运行一次前向推理，并导出
uPIMulator Chiplet 指令规格（JSON）。适合用于快速验证 PyTorch → Chiplet 的映射流程。

示例：
    python tools/export_pytorch_chiplet.py \
        --layers 6 --hidden-size 4096 --intermediate-size 11008 \
        --num-experts 16 --experts-per-tok 2 --seq-length 2048 \
        --output bin/pytorch_demo_chiplet.json
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any, Dict

import torch
import torch.nn as nn

from generate_chiplet_model import build_moe_sequence


class SimpleMoEBlock(nn.Module):
    def __init__(self, hidden_size: int, intermediate_size: int, num_experts: int, experts_per_tok: int):
        super().__init__()
        self.hidden_size = hidden_size
        self.intermediate_size = intermediate_size
        self.num_experts = num_experts
        self.experts_per_tok = max(experts_per_tok, 1)

        self.gate = nn.Linear(hidden_size, num_experts, bias=False)
        experts = []
        for _ in range(num_experts):
            experts.append(
                nn.Sequential(
                    nn.Linear(hidden_size, intermediate_size),
                    nn.GELU(),
                    nn.Linear(intermediate_size, hidden_size),
                )
            )
        self.experts = nn.ModuleList(experts)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        gate_logits = self.gate(x)
        gate_scores = torch.softmax(gate_logits, dim=-1)

        outputs = []
        for expert, score in zip(self.experts, gate_scores.transpose(-1, -2)):
            expert_out = expert(x)
            weight = score.unsqueeze(-1)
            outputs.append(expert_out * weight)
        stacked = torch.stack(outputs, dim=-2)
        return stacked.sum(dim=-2)


class SimpleTransformerMoE(nn.Module):
    def __init__(
        self,
        hidden_size: int,
        intermediate_size: int,
        num_heads: int,
        num_experts: int,
        experts_per_tok: int,
        layers: int,
    ):
        super().__init__()
        self.layers = nn.ModuleList()
        for _ in range(layers):
            self.layers.append(
                nn.ModuleDict(
                    dict(
                        attn=nn.MultiheadAttention(embed_dim=hidden_size, num_heads=num_heads, batch_first=True),
                        attn_norm=nn.LayerNorm(hidden_size),
                        moe=SimpleMoEBlock(hidden_size, intermediate_size, num_experts, experts_per_tok),
                        moe_norm=nn.LayerNorm(hidden_size),
                    )
                )
            )

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        for layer in self.layers:
            attn_out, _ = layer["attn"](x, x, x)
            x = layer["attn_norm"](x + attn_out)
            moe_out = layer["moe"](x)
            x = layer["moe_norm"](x + moe_out)
        return x


def run_demo(model: nn.Module, batch: int, seq_length: int, hidden_size: int) -> None:
    model.eval()
    with torch.no_grad():
        dummy = torch.randn(batch, seq_length, hidden_size)
        _ = model(dummy)


def build_spec(args: argparse.Namespace) -> Dict[str, Any]:
    sequence = build_moe_sequence(
        layers=args.layers,
        hidden_size=args.hidden_size,
        intermediate_size=args.intermediate_size,
        num_experts=args.num_experts,
        experts_per_tok=args.experts_per_tok,
        seq_length=args.seq_length,
        batch=args.batch,
        dtype_bytes=args.dtype_bytes,
        digital_chiplets=args.digital_chiplets,
        rram_chiplets=args.rram_chiplets,
        digital_latency_scale=args.digital_latency_scale,
        chunk_bytes=args.chunk_bytes,
    )
    return {
        "name": args.name or "pytorch_transformer_moe",
        "sequence": sequence,
        "metadata": {
            "hidden_size": args.hidden_size,
            "intermediate_size": args.intermediate_size,
            "layers": args.layers,
            "num_experts": args.num_experts,
            "experts_per_tok": args.experts_per_tok,
            "seq_length": args.seq_length,
            "batch": args.batch,
            "dtype_bytes": args.dtype_bytes,
            "digital_chiplets": args.digital_chiplets,
            "rram_chiplets": args.rram_chiplets,
            "chunk_bytes": args.chunk_bytes,
        },
    }


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="导出简单 PyTorch Transformer+MoE 模型的 Chiplet 指令规格。")
    parser.add_argument("--output", type=Path, required=True, help="输出 JSON 文件路径。")
    parser.add_argument("--name", type=str, default=None, help="规格名称。")
    parser.add_argument("--layers", type=int, default=6, help="Transformer 层数。")
    parser.add_argument("--hidden-size", type=int, default=4096, help="隐藏维度大小。")
    parser.add_argument("--intermediate-size", type=int, default=11008, help="FFN/专家中间维度。")
    parser.add_argument("--num-heads", type=int, default=32, help="Attention 多头数。")
    parser.add_argument("--num-experts", type=int, default=16, help="专家数量。")
    parser.add_argument("--experts-per-tok", type=int, default=2, help="每个 token 激活的专家数量。")
    parser.add_argument("--seq-length", type=int, default=2048, help="序列长度。")
    parser.add_argument("--batch", type=int, default=1, help="batch size。")
    parser.add_argument("--dtype-bytes", type=int, default=2, help="激活精度（字节）。")
    parser.add_argument("--digital-chiplets", type=int, default=2, help="数字 Chiplet 数量。")
    parser.add_argument("--rram-chiplets", type=int, default=8, help="RRAM Chiplet 数量。")
    parser.add_argument("--digital-latency-scale", type=float, default=1.0, help="数字算子延迟缩放系数。")
    parser.add_argument("--chunk-bytes", type=int, default=32 * 1024 * 1024, help="流水分块大小（字节）。")
    parser.add_argument("--skip-forward", action="store_true", help="仅生成规格，不执行 PyTorch 前向。")
    return parser


def main() -> None:
    parser = build_arg_parser()
    args = parser.parse_args()

    model = SimpleTransformerMoE(
        hidden_size=args.hidden_size,
        intermediate_size=args.intermediate_size,
        num_heads=args.num_heads,
        num_experts=args.num_experts,
        experts_per_tok=args.experts_per_tok,
        layers=args.layers,
    )

    if not args.skip_forward:
        run_demo(model, args.batch, args.seq_length, args.hidden_size)

    spec = build_spec(args)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", encoding="utf-8") as fp:
        json.dump(spec, fp, indent=2)
        fp.write("\n")
    print(f"[export_pytorch_chiplet] wrote {args.output}")


if __name__ == "__main__":
    main()
