#!/usr/bin/env python3
"""Embedding sidecar for ALF semantic memory.

Uses ONNX Runtime + tokenizers for lightweight CPU inference (~150 MB vs ~1 GB PyTorch).

Runs as a persistent process. Loads model once, accepts requests via stdin (JSON lines).
Each request:  {"id": "...", "texts": ["text1", "text2"]}
Each response: {"id": "...", "embeddings": [[0.1, ...], [0.2, ...]]}

Also supports one-shot mode: embed.py "some text to embed"
"""

import json
import sys
import os
import time

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer


def load_model(model_dir):
    """Load ONNX model and tokenizer from directory."""
    model_path = os.path.join(model_dir, "model.onnx")
    tokenizer_path = os.path.join(model_dir, "tokenizer.json")

    for path in (model_path, tokenizer_path):
        if not os.path.exists(path):
            print(f"Missing: {path}", file=sys.stderr)
            sys.exit(1)

    start = time.time()
    print(f"Loading ONNX model from {model_dir}...", file=sys.stderr)

    tokenizer = Tokenizer.from_file(tokenizer_path)
    tokenizer.enable_padding()
    tokenizer.enable_truncation(max_length=256)

    sess_opts = ort.SessionOptions()
    sess_opts.inter_op_num_threads = 2
    sess_opts.intra_op_num_threads = 4
    session = ort.InferenceSession(model_path, sess_opts, providers=["CPUExecutionProvider"])

    # Infer dims from a dummy forward pass.
    dummy = tokenizer.encode("hello")
    ids = np.array([dummy.ids], dtype=np.int64)
    mask = np.array([dummy.attention_mask], dtype=np.int64)
    token_types = np.zeros_like(ids)
    out = session.run(None, {"input_ids": ids, "attention_mask": mask, "token_type_ids": token_types})
    dims = out[0].shape[-1]

    elapsed = time.time() - start
    print(f"Model loaded in {elapsed:.1f}s (dims={dims})", file=sys.stderr)
    return session, tokenizer, dims


def encode(session, tokenizer, texts):
    """Encode texts to normalized embeddings (matching sentence-transformers output)."""
    encoded = tokenizer.encode_batch(texts)

    ids = np.array([e.ids for e in encoded], dtype=np.int64)
    mask = np.array([e.attention_mask for e in encoded], dtype=np.int64)
    token_types = np.zeros_like(ids)

    out = session.run(None, {
        "input_ids": ids,
        "attention_mask": mask,
        "token_type_ids": token_types,
    })

    # Mean pooling over token embeddings, masked.
    token_embeddings = out[0]  # (batch, seq_len, dims)
    mask_expanded = mask[:, :, np.newaxis].astype(np.float32)
    summed = np.sum(token_embeddings * mask_expanded, axis=1)
    counts = np.clip(mask_expanded.sum(axis=1), a_min=1e-9, a_max=None)
    pooled = summed / counts

    # L2 normalize.
    norms = np.linalg.norm(pooled, axis=1, keepdims=True)
    norms = np.clip(norms, a_min=1e-12, a_max=None)
    normalized = pooled / norms

    return normalized.tolist()


def run_server(model_dir):
    """Run as persistent server, reading JSON lines from stdin."""
    session, tokenizer, dims = load_model(model_dir)

    ready_msg = json.dumps({"status": "ready", "dims": dims})
    sys.stdout.write(ready_msg + "\n")
    sys.stdout.flush()

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue

        try:
            req = json.loads(line)
            texts = req.get("texts", [])
            req_id = req.get("id", "")

            if not texts:
                result = {"id": req_id, "error": "no texts provided"}
            else:
                embeddings = encode(session, tokenizer, texts)
                result = {"id": req_id, "embeddings": embeddings}
        except json.JSONDecodeError as e:
            result = {"error": f"Invalid JSON: {e}"}
        except Exception as e:
            result = {"error": str(e)}

        sys.stdout.write(json.dumps(result) + "\n")
        sys.stdout.flush()


def run_oneshot(text, model_dir):
    """One-shot mode: load model, embed single text, exit."""
    session, tokenizer, dims = load_model(model_dir)
    embedding = encode(session, tokenizer, [text])[0]
    json.dump({"dims": dims, "embedding": embedding}, sys.stdout)
    sys.stdout.write("\n")


def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("text", nargs="?", help="Text to embed (one-shot mode)")
    parser.add_argument("--model-dir", default="/opt/alf/models/all-MiniLM-L6-v2",
                        help="Directory containing model.onnx and tokenizer.json")
    parser.add_argument("--server", action="store_true",
                        help="Run as persistent server (stdin/stdout JSON lines)")
    args = parser.parse_args()

    if args.server or args.text is None:
        run_server(args.model_dir)
    else:
        run_oneshot(args.text, args.model_dir)


if __name__ == "__main__":
    main()
