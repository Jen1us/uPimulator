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
from typing import Any, Dict, List, Optional, Sequence


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


def build_chunk_plan(tokens: int, hidden_size: int, dtype_bytes: int, chunk_bytes: int) -> List[int]:
    if tokens <= 0:
        return []
    per_token_bytes = max(hidden_size * dtype_bytes, dtype_bytes)
    if per_token_bytes <= 0:
        per_token_bytes = dtype_bytes
    tokens_per_chunk = max(chunk_bytes // per_token_bytes, 1)
    plan: List[int] = []
    remaining = tokens
    while remaining > 0:
        take = min(tokens_per_chunk, remaining)
        plan.append(take)
        remaining -= take
    if not plan:
        plan.append(tokens)
    return plan


def _normalize_deps(value: Optional[Any]) -> List[int]:
    if value is None:
        return []
    if isinstance(value, int):
        return [value]
    if isinstance(value, Sequence):
        return [v for v in value if isinstance(v, int)]
    return []


def append_chunked_stage(
    sequence: List[Dict[str, Any]],
    stage_builder,
    chunk_plan: List[int],
    base_dep: Optional[Any] = None,
    per_chunk_dep: Optional[List[Optional[Any]]] = None,
    enforce_sequential: bool = True,
) -> List[int]:
    if not chunk_plan:
        return []
    chunk_ids: List[int] = []
    prev_idx: Optional[int] = None
    base_list = _normalize_deps(base_dep)
    for idx, token_count in enumerate(chunk_plan):
        deps: List[int] = []
        if per_chunk_dep is not None and idx < len(per_chunk_dep):
            deps.extend(_normalize_deps(per_chunk_dep[idx]))
        if idx == 0 and not deps and base_list:
            deps.extend(base_list)
        if enforce_sequential and prev_idx is not None:
            deps.append(prev_idx)
        stage = stage_builder(idx, token_count)
        dep_arg = deps if deps else None
        stage_idx = append_stage(sequence, stage, depends=dep_arg)
        chunk_ids.append(stage_idx)
        prev_idx = stage_idx
    return chunk_ids


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
    projection_weight_bytes = hidden_size * hidden_size * dtype_bytes
    expert_weight_bytes = hidden_size * intermediate_size * dtype_bytes

    chunk_plan = build_chunk_plan(tokens, hidden_size, dtype_bytes, chunk_bytes)
    if not chunk_plan:
        chunk_plan = [tokens]
    chunk_byte_plan = [
        max(chunk_tokens * hidden_size * dtype_bytes, dtype_bytes)
        for chunk_tokens in chunk_plan
    ]
    num_chunks = len(chunk_plan)

    def chunk_digital_owner(layer_idx: int, chunk_idx: int) -> int:
        if digital_chiplets <= 0:
            return 0
        return (layer_idx + chunk_idx) % digital_chiplets

    digital_latency_base = max(int(hidden_size * digital_latency_scale), 1)

    embed_stage = append_stage(
        sequence,
        {
            "type": "token_prep",
            "name": "embed",
            "tokens": tokens,
            "features": hidden_size,
            "latency": max(digital_latency_base // 8, 2),
            "host_load_kind": "kv_cache",
        },
        depends=[],
    )

    digital_chiplets = max(digital_chiplets, 1)
    rram_chiplets = max(rram_chiplets, 1)

    num_chunks = len(chunk_plan)

    def add_projection(
        proj_label: str,
        layer_idx: int,
        depends_idx: int,
        weight_bytes: int,
        digital_id: int,
        rram_id: int,
    ) -> int:
        last_idx = depends_idx
        for chunk_idx, chunk_tokens in enumerate(chunk_plan):
            chunk_size = chunk_byte_plan[chunk_idx] if chunk_idx < len(chunk_byte_plan) else chunk_byte_plan[-1]
            approx_tok = chunk_tokens
            chunk_digital = chunk_digital_owner(layer_idx, chunk_idx)
            down = append_stage(
                sequence,
                {
                    "type": "transfer",
                    "name": f"{proj_label}_transfer_to_rram_L{layer_idx}_C{chunk_idx}",
                    "direction": "digital_to_rram",
                    "bytes": chunk_size,
                    "latency": max(int(math.ceil(chunk_size / 4096)), 4),
                    "aux": {
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "projection": proj_label,
                    },
                    "chiplet": rram_id,
                    "queue": chunk_digital,
                },
                depends=[last_idx],
            )
            linear = append_stage(
                sequence,
                {
                    "type": "rram_linear",
                    "name": f"{proj_label}_proj_rram_L{layer_idx}_C{chunk_idx}",
                    "rows": approx_tok,
                    "cols": hidden_size,
                    "k": hidden_size,
                    "activation_bytes": chunk_size,
                    "weight_bytes": weight_bytes if chunk_idx == 0 else 0,
                    "pulse_count": max(hidden_size // 64, 16),
                    "adc_samples": hidden_size,
                    "pre_cycles": 12,
                    "post_cycles": 10,
                    "aux": {
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "projection": proj_label,
                    },
                    "chiplet": rram_id,
                },
                depends=[down],
            )
            up = append_stage(
                sequence,
                {
                    "type": "transfer",
                    "name": f"{proj_label}_transfer_to_digital_L{layer_idx}_C{chunk_idx}",
                    "direction": "rram_to_digital",
                    "bytes": chunk_size,
                    "latency": max(int(math.ceil(chunk_size / 4096)), 4),
                    "aux": {
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "projection": proj_label,
                    },
                    "chiplet": chunk_digital,
                    "queue": rram_id,
                },
                depends=[linear],
            )
            last_idx = up
        return last_idx

    expert_chips = [
        chip_idx % rram_chiplets for chip_idx in range(num_experts)
    ]

    prev_stage = embed_stage

    for layer_idx in range(layers):
        digital_id = layer_idx % digital_chiplets
        rram_base = (layer_idx * 4) % rram_chiplets

        # Q, K, V projections on RRAM
        q_rram = (rram_base + 0) % rram_chiplets
        k_rram = (rram_base + 1) % rram_chiplets
        v_rram = (rram_base + 2) % rram_chiplets
        q_done = add_projection(
            "q",
            layer_idx,
            embed_stage if layer_idx == 0 else prev_stage,
            projection_weight_bytes,
            digital_id,
            q_rram,
        )
        k_done = add_projection(
            "k",
            layer_idx,
            q_done,
            projection_weight_bytes,
            digital_id,
            k_rram,
        )
        v_done = add_projection(
            "v",
            layer_idx,
            k_done,
            projection_weight_bytes,
            digital_id,
            v_rram,
        )

        attn_score_chunks = append_chunked_stage(
            sequence,
            lambda chunk_idx, chunk_tokens: {
                "type": "attention",
                "name": f"attn_score_L{layer_idx}_C{chunk_idx}",
                "rows": chunk_tokens,
                "cols": hidden_size,
                "k": hidden_size,
                "latency": digital_latency_base,
                "chiplet": chunk_digital_owner(layer_idx, chunk_idx),
                "queue": chunk_digital_owner(layer_idx, chunk_idx),
                "weight_bytes": chunk_tokens * hidden_size * dtype_bytes,
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "attention_score",
                    "chunk_index": chunk_idx,
                    "chunk_tokens": chunk_tokens,
                    "chunk_total": len(chunk_plan),
                },
            },
            chunk_plan,
            base_dep=[q_done, k_done],
            enforce_sequential=False,
        )

        post_attn_chunks = append_chunked_stage(
            sequence,
            lambda chunk_idx, chunk_tokens: {
                "type": "softmax",
                "name": f"softmax_L{layer_idx}_C{chunk_idx}",
                "rows": chunk_tokens,
                "cols": hidden_size,
                "latency": max(digital_latency_base // 4, 8),
                "chiplet": chunk_digital_owner(layer_idx, chunk_idx),
                "queue": chunk_digital_owner(layer_idx, chunk_idx),
                "nonlinear_kind": "softmax",
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "attention_norm",
                    "chunk_index": chunk_idx,
                    "chunk_tokens": chunk_tokens,
                    "chunk_total": len(chunk_plan),
                },
            },
            chunk_plan,
            per_chunk_dep=attn_score_chunks,
            enforce_sequential=False,
        )

        mix_deps: List[Optional[Any]] = []
        for idx in range(len(chunk_plan)):
            dep = []
            if idx < len(post_attn_chunks):
                dep.append(post_attn_chunks[idx])
            dep.append(v_done)
            mix_deps.append(dep)

        attn_mix_chunks = append_chunked_stage(
            sequence,
            lambda chunk_idx, chunk_tokens: {
                "type": "attention",
                "name": f"attn_mix_L{layer_idx}_C{chunk_idx}",
                "rows": chunk_tokens,
                "cols": hidden_size,
                "k": hidden_size,
                "latency": digital_latency_base,
                "chiplet": chunk_digital_owner(layer_idx, chunk_idx),
                "queue": chunk_digital_owner(layer_idx, chunk_idx),
                "weight_bytes": chunk_tokens * hidden_size * dtype_bytes,
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "attention_mix",
                    "chunk_index": chunk_idx,
                    "chunk_tokens": chunk_tokens,
                    "chunk_total": len(chunk_plan),
                },
            },
            chunk_plan,
            per_chunk_dep=mix_deps,
            enforce_sequential=False,
        )

        attn_mix_tail = attn_mix_chunks[-1] if attn_mix_chunks else (post_attn_chunks[-1] if post_attn_chunks else attn_score_chunks[-1])

        o_rram = (rram_base + 3) % rram_chiplets
        o_done = add_projection(
            "o",
            layer_idx,
            attn_mix_tail,
            projection_weight_bytes,
            digital_id,
            o_rram,
        )

        post_attention_chunks = append_chunked_stage(
            sequence,
            lambda chunk_idx, chunk_tokens: {
                "type": "postprocess",
                "name": f"post_attention_L{layer_idx}_C{chunk_idx}",
                "rows": chunk_tokens,
                "cols": hidden_size,
                "latency": max(digital_latency_base // 5, 10),
                "chiplet": chunk_digital_owner(layer_idx, chunk_idx),
                "queue": chunk_digital_owner(layer_idx, chunk_idx),
                "nonlinear_kind": "residual",
                "metadata": {
                    "layer_index": layer_idx,
                    "stage": "post_attention",
                    "chunk_index": chunk_idx,
                    "chunk_tokens": chunk_tokens,
                    "chunk_total": len(chunk_plan),
                },
            },
            chunk_plan,
            base_dep=o_done,
            enforce_sequential=False,
        )

        gating_chunks = append_chunked_stage(
            sequence,
            lambda chunk_idx, chunk_tokens: {
                "type": "moe_gating",
                "name": f"moe_gating_L{layer_idx}_C{chunk_idx}",
                "rows": chunk_tokens,
                "cols": experts_per_tok,
                "k": num_experts,
                "latency": max(digital_latency_base // 6, 6),
                "aux": {"experts_per_tok": experts_per_tok, "chunk_index": chunk_idx},
                "chiplet": chunk_digital_owner(layer_idx, chunk_idx),
                "queue": chunk_digital_owner(layer_idx, chunk_idx),
                "randomize_experts": True,
            },
            chunk_plan,
            per_chunk_dep=post_attention_chunks,
        )

        experts_template: List[Dict[str, Any]] = []
        for expert_idx in range(num_experts):
            experts_template.append(
                {
                    "name": f"expert_{layer_idx}_{expert_idx}",
                    "chiplet": expert_chips[expert_idx],
                    "activation_bytes": activation_bytes_total // max(experts_per_tok, 1),
                    "weight_bytes": expert_weight_bytes,
                    "execute_latency": max(int(intermediate_size / 16), 32),
                }
            )

        moe_prev = None
        transfer_up_chunks: List[int] = []
        for chunk_idx, chunk_tokens in enumerate(chunk_plan):
            chunk_size = chunk_byte_plan[chunk_idx] if chunk_idx < len(chunk_byte_plan) else chunk_byte_plan[-1]
            target_rram = expert_chips[(chunk_idx * experts_per_tok) % len(expert_chips)]
            down_deps: List[int] = []
            if chunk_idx < len(gating_chunks):
                down_deps.append(gating_chunks[chunk_idx])
            if moe_prev is not None:
                down_deps.append(moe_prev)
            chunk_digital = chunk_digital_owner(layer_idx, chunk_idx)
            transfer_down = append_stage(
                sequence,
                {
                    "type": "transfer",
                    "name": f"moe_transfer_to_rram_L{layer_idx}_C{chunk_idx}",
                    "direction": "digital_to_rram",
                    "bytes": chunk_size,
                    "latency": max(int(math.ceil(chunk_size / 4096)), 4),
                    "aux": {
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "experts_per_tok": experts_per_tok,
                    },
                    "chiplet": target_rram,
                    "queue": chunk_digital,
                },
                depends=down_deps if down_deps else None,
            )
            experts: List[Dict[str, Any]] = []
            for expert in experts_template:
                experts.append(
                    {
                        **expert,
                        "activation_bytes": chunk_size // max(experts_per_tok, 1),
                        "weight_bytes": expert["weight_bytes"] if chunk_idx == 0 else 0,
                        "chunk_index": chunk_idx,
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
                },
                depends=[transfer_down],
            )
            transfer_up = append_stage(
                sequence,
                {
                    "type": "transfer",
                    "name": f"moe_transfer_to_digital_L{layer_idx}_C{chunk_idx}",
                    "direction": "rram_to_digital",
                    "bytes": chunk_size,
                    "latency": max(int(math.ceil(chunk_size / 4096)), 4),
                    "aux": {
                        "chunk_index": chunk_idx,
                        "chunk_total": num_chunks,
                        "experts_per_tok": experts_per_tok,
                    },
                    "chiplet": chunk_digital,
                    "queue": expert_chips[(chunk_idx * experts_per_tok) % len(expert_chips)],
                },
                depends=[moe_stage],
            )
            moe_prev = transfer_up
            transfer_up_chunks.append(transfer_up)

        merge_chunks = append_chunked_stage(
            sequence,
            lambda chunk_idx, chunk_tokens: {
                "type": "moe_merge",
                "name": f"moe_merge_L{layer_idx}_C{chunk_idx}",
                "rows": chunk_tokens,
                "cols": hidden_size,
                "latency": max(digital_latency_base // 5, 10),
            },
            chunk_plan,
            per_chunk_dep=transfer_up_chunks,
        )

        postprocess_chunks = append_chunked_stage(
            sequence,
            lambda chunk_idx, chunk_tokens: {
                "type": "postprocess",
                "name": f"postprocess_L{layer_idx}_C{chunk_idx}",
                "rows": chunk_tokens,
                "cols": hidden_size,
                "latency": max(digital_latency_base // 3, 12),
                "nonlinear_kind": "residual",
                "host_store_kind": "kv_cache",
                "chiplet": chunk_digital_owner(layer_idx, chunk_idx),
                "queue": chunk_digital_owner(layer_idx, chunk_idx),
            },
            chunk_plan,
            per_chunk_dep=merge_chunks,
        )

        if postprocess_chunks:
            prev_stage = postprocess_chunks[-1]
        elif merge_chunks:
            prev_stage = merge_chunks[-1]
        elif transfer_up_chunks:
            prev_stage = transfer_up_chunks[-1]
        else:
            prev_stage = moe_prev if moe_prev is not None else o_done

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
