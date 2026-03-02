#!/usr/bin/env python3
"""Embedding sidecar for ALF semantic memory.

Runs as a persistent process. Loads model once, accepts requests via stdin (JSON lines).
Each request:  {"id": "...", "texts": ["text1", "text2"]}
Each response: {"id": "...", "embeddings": [[0.1, ...], [0.2, ...]]}

Also supports one-shot mode: embed.py "some text to embed"
"""

import json
import sys
import os
import time


def load_model(model_name, models_dir):
    """Load the sentence-transformers model."""
    try:
        from sentence_transformers import SentenceTransformer
    except ImportError:
        print("sentence-transformers not installed. Installing...", file=sys.stderr)
        import subprocess
        subprocess.check_call([
            sys.executable, "-m", "pip", "install",
            "--break-system-packages", "sentence-transformers"
        ], stdout=sys.stderr, stderr=sys.stderr)
        from sentence_transformers import SentenceTransformer

    os.makedirs(models_dir, exist_ok=True)

    start = time.time()
    print(f"Loading model {model_name!r} from {models_dir}...", file=sys.stderr)
    model = SentenceTransformer(model_name, cache_folder=models_dir)
    elapsed = time.time() - start
    dims = model.get_sentence_embedding_dimension()
    print(f"Model loaded in {elapsed:.1f}s (dims={dims})", file=sys.stderr)
    return model, dims


def run_server(model_name, models_dir):
    """Run as persistent server, reading JSON lines from stdin."""
    model, dims = load_model(model_name, models_dir)

    # Signal readiness.
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
                embeddings = model.encode(texts).tolist()
                result = {"id": req_id, "embeddings": embeddings}
        except json.JSONDecodeError as e:
            result = {"error": f"Invalid JSON: {e}"}
        except Exception as e:
            result = {"error": str(e)}

        sys.stdout.write(json.dumps(result) + "\n")
        sys.stdout.flush()


def run_oneshot(text, model_name, models_dir):
    """One-shot mode: load model, embed single text, exit."""
    model, dims = load_model(model_name, models_dir)
    embedding = model.encode([text])[0].tolist()
    json.dump({"dims": dims, "embedding": embedding}, sys.stdout)
    sys.stdout.write("\n")


def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("text", nargs="?", help="Text to embed (one-shot mode)")
    parser.add_argument("--model", default="all-MiniLM-L6-v2", help="Model name")
    parser.add_argument("--models-dir", default="/home/node/data/models", help="Model cache directory")
    parser.add_argument("--server", action="store_true", help="Run as persistent server (stdin/stdout JSON lines)")
    args = parser.parse_args()

    if args.server or args.text is None:
        run_server(args.model, args.models_dir)
    else:
        run_oneshot(args.text, args.model, args.models_dir)


if __name__ == "__main__":
    main()
