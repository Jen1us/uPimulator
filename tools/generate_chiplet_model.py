#!/usr/bin/env python3
"""
generate_chiplet_model.py
=========================

Convert a HuggingFace/PyTorch MoE model configuration (e.g., Qwen1.5-MoE-A2.7B)
into a chiplet command specification understood by uPIMulator.

The script also works without a config file—when `--config` is omitted you can
fully describe the model via command-line flags.
"""

from __future__ import annotations

import argparse
import json
import math
from pathlib import Path
from typing import Any, Dict, List, Optional


def load_model_config(path: Optional[Path]) -> Dict[str, Any]:
    if path is None:
        return {}
    if not path.exists():
        raise FileNotFoundError(f"config file not found: {path}")
    with path.open("r", encoding="utf-8") as fp:
        return json.load(fp)


def int_from_config(data: Dict[str, Any], *keys: str, default: Optional[int] = None) -> Optional[int]:
    for key in keys:
        if key in data and isinstance(data[key], (int, float)):
            return int(data[key])
    return default


def bool_from_config(data: Dict[str, Any], *keys: str, default: bool = False) -> bool:
    for key in keys:
        if key in data:
            value = data[key]
            if isinstance(value, bool):
                return value
            if isinstance(value, str):
                return value.lower() in {"1", "true", "yes", "y", "on"}
    return default


def append_stage(sequence: List[Dict[str, Any]], stage: Dict[str, Any], depends: Optional[List[int]] = None) -> int:
    if depends is None:
        if sequence:
            stage["deps"] = [len(sequence) - 1]
    else:
        stage["deps"] = depends
    sequence.append(stage)
    return len(sequence) - 1


def build_moe_sequence(
    layers: int,
    hidden_size: int,
    intermediate_size: int,
    num_experts: int,
    experts_per_tok: int,
    seq_length: int,
    batch: int,
    dtype_bytes: int,
    digital_chiplets: int,
    rram_chiplets: int,
    digital_latency_scale: float,
    chunk_bytes: int,
) -> List[Dict[str, Any]]:
    sequence: List[Dict[str, Any]] = []

    dtype_bytes = max(dtype_bytes, 1)
    chunk_bytes = max(chunk_bytes, dtype_bytes)

    tokens = seq_length * batch
    activation_bytes_total = tokens * hidden_size * dtype_bytes
    expert_weight_bytes = hidden_size * intermediate_size * dtype_bytes

    digital_latency_base = max(int(hidden_size * digital_latency_scale), 1)

    host_dma_unit = max(chunk_bytes // 4, 4096)
    host_load_latency = max(int(math.ceil(max(activation_bytes_total, 1) / host_dma_unit)), 8)

    host_prefetch_idx = append_stage(
        sequence,
        {
            "type": "transfer",
            "name": "host_load_input",
            "direction": "host_to_digital",
            "chiplet": 0,
            "queue": 0,
            "bytes": activation_bytes_total,
            "latency": host_load_latency,
            "buffer_id": hidden_size,
            "metadata": {
                "tensor": "input_tokens",
                "phase": "prefetch",
                "batch": batch,
                "seq_length": seq_length,
                "dtype_bytes": dtype_bytes,
            },
        },
        depends=[],
    )

    append_stage(
        sequence,
        {
            "type": "token_prep",
            "name": "embed",
            "tokens": tokens,
            "features": hidden_size,
            "latency": max(digital_latency_base // 8, 2),
        },
        depends=[host_prefetch_idx],
    )

    digital_chiplets = max(digital_chiplets, 1)
    rram_chiplets = max(rram_chiplets, 1)

    for layer_idx in range(layers):
        digital_owner = layer_idx % digital_chiplets

        att = append_stage(
            sequence,
            {
                "type": "attention",
                "name": f"attn_L{layer_idx}",
                "rows": hidden_size,
                "cols": hidden_size,
                "k": hidden_size,
                "latency": digital_latency_base,
                "chiplet": digital_owner,
                "queue": digital_owner,
                "buffer_id": hidden_size,
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "attention",
                    "digital_chiplet": digital_owner,
                },
            },
        )

        post_attn = append_stage(
            sequence,
            {
                "type": "softmax",
                "name": f"softmax_L{layer_idx}",
                "rows": tokens,
                "cols": hidden_size,
                "latency": max(digital_latency_base // 4, 8),
                "chiplet": digital_owner,
                "queue": digital_owner,
                "buffer_id": hidden_size,
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "attention_norm",
                    "digital_chiplet": digital_owner,
                },
            },
            depends=[att],
        )

        experts_template: List[Dict[str, Any]] = []
        for expert_idx in range(num_experts):
            chiplet_id = expert_idx % rram_chiplets
            experts_template.append(
                {
                    "name": f"expert_{layer_idx}_{expert_idx}",
                    "chiplet": chiplet_id,
                    "activation_bytes": activation_bytes_total // max(experts_per_tok, 1),
                    "weight_bytes": expert_weight_bytes,
                    "execute_latency": max(int(intermediate_size / 16), 32),
                    "metadata": {
                        "layer_index": layer_idx,
                        "expert_index": expert_idx,
                        "rram_chiplet": chiplet_id,
                    },
                }
            )

        candidate_experts = list(range(num_experts))
        candidate_chiplets = [entry["chiplet"] for entry in experts_template]

        gating = append_stage(
            sequence,
            {
                "type": "moe_gating",
                "name": f"moe_gating_L{layer_idx}",
                "rows": tokens,
                "cols": num_experts,
                "latency": max(digital_latency_base // 6, 6),
                "chiplet": digital_owner,
                "queue": digital_owner,
                "buffer_id": hidden_size,
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "moe_gating",
                    "digital_chiplet": digital_owner,
                    "top_k": experts_per_tok,
                    "candidate_experts": candidate_experts,
                    "candidate_chiplets": candidate_chiplets,
                    "tokens": tokens,
                    "features": hidden_size,
                },
                "aux": {
                    "experts_per_tok": experts_per_tok,
                    "layer_index": layer_idx,
                },
            },
            depends=[post_attn],
        )

        num_chunks = max(int(math.ceil(activation_bytes_total / chunk_bytes)), 1)
        last_transfer_up = None

        for chunk_idx in range(num_chunks):
            chunk_size = (
                chunk_bytes
                if chunk_idx < num_chunks - 1
                else activation_bytes_total - chunk_bytes * (num_chunks - 1)
            )
            chunk_dep = [gating] if chunk_idx == 0 else [last_transfer_up]

            rram_target = chunk_idx % rram_chiplets

            transfer_down = append_stage(
                sequence,
                {
                    "type": "transfer",
                    "name": f"transfer_to_rram_L{layer_idx}_C{chunk_idx}",
                    "direction": "digital_to_rram",
                    "bytes": chunk_size,
                    "latency": max(int(math.ceil(chunk_size / 4096)), 4),
                    "chiplet": rram_target,
                    "queue": digital_owner,
                    "buffer_id": hidden_size,
                    "metadata": {
                        "layer_index": layer_idx,
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "source_digital": digital_owner,
                        "target_rram": rram_target,
                        "stage": "digital_to_rram",
                        "chunk_bytes": chunk_size,
                    },
                    "aux": {
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "layer_index": layer_idx,
                    },
                },
                depends=chunk_dep,
            )

            experts: List[Dict[str, Any]] = []
            for expert in experts_template:
                experts.append(
                    {
                        **expert,
                        "activation_bytes": chunk_size // max(experts_per_tok, 1),
                        "weight_bytes": expert["weight_bytes"] if chunk_idx == 0 else 0,
                        "chunk_index": chunk_idx,
                        "metadata": {
                            **expert.get("metadata", {}),
                            "chunk_index": chunk_idx,
                            "chunk_bytes": chunk_size // max(experts_per_tok, 1),
                            "chunk_total": num_chunks,
                        },
                    }
                )

            moe_stage = append_stage(
                sequence,
                {
                    "type": "moe_linear",
                    "name": f"moe_L{layer_idx}_C{chunk_idx}",
                    "parallel": True,
                    "pulse_count": max(intermediate_size // 64, 16),
                    "adc_samples": hidden_size,
                    "pre_cycles": 12,
                    "post_cycles": 10,
                    "activation_bytes": chunk_size,
                    "weight_bytes": expert_weight_bytes if chunk_idx == 0 else 0,
                    "experts": experts,
                    "metadata": {
                        "layer_index": layer_idx,
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "stage": "moe_experts",
                    },
                },
                depends=[transfer_down],
            )

            last_transfer_up = append_stage(
                sequence,
                {
                    "type": "transfer",
                    "name": f"transfer_to_digital_L{layer_idx}_C{chunk_idx}",
                    "direction": "rram_to_digital",
                    "bytes": chunk_size,
                    "latency": max(int(math.ceil(chunk_size / 4096)), 4),
                    "chiplet": digital_owner,
                    "queue": rram_target,
                    "buffer_id": hidden_size,
                    "metadata": {
                        "layer_index": layer_idx,
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "source_rram": rram_target,
                        "target_digital": digital_owner,
                        "stage": "rram_to_digital",
                        "chunk_bytes": chunk_size,
                    },
                    "aux": {
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "layer_index": layer_idx,
                    },
                },
                depends=[moe_stage],
            )

        merge = append_stage(
            sequence,
            {
                "type": "moe_merge",
                "name": f"moe_merge_L{layer_idx}",
                "rows": tokens,
                "cols": hidden_size,
                "latency": max(digital_latency_base // 5, 10),
                "chiplet": digital_owner,
                "queue": digital_owner,
                "buffer_id": hidden_size,
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "moe_merge",
                    "digital_chiplet": digital_owner,
                },
            },
            depends=[last_transfer_up] if last_transfer_up is not None else [gating],
        )

        append_stage(
            sequence,
            {
                "type": "postprocess",
                "name": f"postprocess_L{layer_idx}",
                "rows": tokens,
                "cols": hidden_size,
                "latency": max(digital_latency_base // 3, 12),
                "chiplet": digital_owner,
                "queue": digital_owner,
                "buffer_id": hidden_size,
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "postprocess",
                    "digital_chiplet": digital_owner,
                },
            },
            depends=[merge],
        )

    if sequence:
        last_stage_idx = len(sequence) - 1
    else:
        last_stage_idx = None

    if last_stage_idx is not None:
        append_stage(
            sequence,
            {
                "type": "transfer",
                "name": "store_logits",
                "direction": "digital_to_host",
                "chiplet": 0,
                "queue": 0,
                "bytes": activation_bytes_total,
                "latency": max(host_load_latency, 8),
                "buffer_id": hidden_size,
                "metadata": {
                    "tensor": "decoder_output",
                    "phase": "write_back",
                    "batch": batch,
                    "seq_length": seq_length,
                    "dtype_bytes": dtype_bytes,
                },
            },
            depends=[last_stage_idx],
        )

    return sequence


def generate_chiplet_model(args: argparse.Namespace) -> Dict[str, Any]:
    config_path = Path(args.config) if args.config else None
    config_data = load_model_config(config_path)

    hidden_size = args.hidden_size or int_from_config(
        config_data, "hidden_size", "n_embd", "dim", default=4096
    )
    intermediate_size = args.intermediate_size or int_from_config(
        config_data,
        "moe_intermediate_size",
        "intermediate_size",
        "ffn_hidden_size",
        default=hidden_size * 4,
    )
    num_layers = args.layers or int_from_config(
        config_data, "num_hidden_layers", "n_layer", "num_layers", default=32
    )
    config_local_experts = int_from_config(
        config_data, "num_local_experts", default=None
    )
    config_moe_experts = int_from_config(
        config_data, "moe_num_experts", "num_experts", default=None
    )
    num_experts = (
        args.num_experts
        or config_local_experts
        or config_moe_experts
        or 16
    )
    experts_per_tok = args.experts_per_tok or int_from_config(
        config_data, "moe_topk", "moe_top_k", default=2
    )

    use_moe = bool_from_config(config_data, "use_moe", default=num_experts > 0)
    if not use_moe and num_experts <= 0:
        raise ValueError("模型未启用 MoE，请指定 --num-experts > 0 以生成专家阶段。")
    if num_experts <= 0:
        num_experts = 1

    sequence = build_moe_sequence(
        layers=num_layers,
        hidden_size=hidden_size,
        intermediate_size=intermediate_size,
        num_experts=num_experts,
        experts_per_tok=experts_per_tok,
        seq_length=args.seq_length,
        batch=args.batch,
        dtype_bytes=args.dtype_bytes,
        digital_chiplets=args.digital_chiplets,
        rram_chiplets=args.rram_chiplets,
        digital_latency_scale=args.digital_latency_scale,
        chunk_bytes=args.chunk_bytes,
    )

    return {
        "name": args.name
        or config_data.get("model_type")
        or config_data.get("architectures", ["moe_model"])[0],
        "sequence": sequence,
        "metadata": {
            "hidden_size": hidden_size,
            "intermediate_size": intermediate_size,
            "layers": num_layers,
            "num_experts": num_experts,
            "experts_per_tok": experts_per_tok,
            "seq_length": args.seq_length,
            "batch": args.batch,
            "dtype_bytes": args.dtype_bytes,
            "digital_chiplets": args.digital_chiplets,
            "rram_chiplets": args.rram_chiplets,
            "chunk_bytes": args.chunk_bytes,
        },
    }


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Convert HuggingFace/PyTorch MoE models into chiplet command specifications."
    )
    parser.add_argument(
        "--config",
        type=Path,
        default=None,
        help="模型配置（HuggingFace config.json）路径，可选。",
    )
    parser.add_argument(
        "--output",
        type=Path,
        required=True,
        help="输出 JSON 文件路径。",
    )
    parser.add_argument("--name", type=str, default=None, help="规格名称。")
    parser.add_argument("--seq-length", type=int, default=2048, help="序列长度。")
    parser.add_argument("--batch", type=int, default=1, help="batch size。")
    parser.add_argument("--dtype-bytes", type=int, default=2, help="张量精度（字节）。")
    parser.add_argument("--layers", type=int, default=None, help="覆盖配置中的层数。")
    parser.add_argument("--hidden-size", type=int, default=None, help="覆盖 hidden_size。")
    parser.add_argument("--intermediate-size", type=int, default=None, help="覆盖 intermediate_size。")
    parser.add_argument("--num-experts", type=int, default=None, help="专家数量。")
    parser.add_argument("--experts-per-tok", type=int, default=None, help="每个 token 激活的专家数量。")
    parser.add_argument("--digital-chiplets", type=int, default=2, help="数字 Chiplet 数量。")
    parser.add_argument("--rram-chiplets", type=int, default=8, help="RRAM Chiplet 数量。")
    parser.add_argument(
        "--digital-latency-scale",
        type=float,
        default=1.0,
        help="数字算子延迟缩放系数（用于估计 latency 字段）。",
    )
    parser.add_argument(
        "--chunk-bytes",
        type=int,
        default=32 * 1024 * 1024,
        help="流水分块大小（字节），用于拆分激活传输与 RRAM 任务。",
    )
    return parser


def main() -> None:
    parser = build_arg_parser()
    args = parser.parse_args()
    spec = generate_chiplet_model(args)

    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", encoding="utf-8") as fp:
        json.dump(spec, fp, indent=2)
        fp.write("\n")

    print(f"[generate_chiplet_model] wrote {args.output}")


if __name__ == "__main__":
    main()
