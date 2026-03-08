#!/usr/bin/env python3
"""Whisper transcription server for ALF daemon.

Runs as a persistent process. Loads model once, accepts requests via stdin (JSON lines).
Each line: {"audio_file": "/path/to/file.ogg"}
Each response: {"text": "...", "duration_s": 1.5, "language": "en", "language_probability": 0.95}

Also supports one-shot mode: transcribe.py <audio_file> [--model small] [--models-dir /path]
"""

import json
import sys
import os
import time


def load_model(model_name, models_dir):
    """Load the faster-whisper model."""
    try:
        from faster_whisper import WhisperModel
    except ImportError:
        print("faster-whisper not installed. Installing...", file=sys.stderr)
        import subprocess
        subprocess.check_call([
            sys.executable, "-m", "pip", "install",
            "--break-system-packages", "faster-whisper"
        ], stdout=sys.stderr, stderr=sys.stderr)
        from faster_whisper import WhisperModel

    os.makedirs(models_dir, exist_ok=True)

    start = time.time()
    print(f"Loading model {model_name!r} from {models_dir}...", file=sys.stderr)
    model = WhisperModel(
        model_name,
        device="cpu",
        compute_type="int8",
        download_root=models_dir,
    )
    elapsed = time.time() - start
    print(f"Model loaded in {elapsed:.1f}s", file=sys.stderr)
    return model


def transcribe(model, audio_file):
    """Transcribe an audio file and return result dict."""
    start = time.time()
    segments, info = model.transcribe(audio_file)

    # Filter out hallucinated segments (silence/music misdetected as speech).
    good_segments = []
    for seg in segments:
        if hasattr(seg, 'no_speech_prob') and seg.no_speech_prob > 0.6:
            continue
        if hasattr(seg, 'avg_logprob') and seg.avg_logprob < -1.0:
            continue
        good_segments.append(seg.text)

    text = " ".join(good_segments).strip()
    elapsed = time.time() - start

    return {
        "text": text,
        "duration_s": round(elapsed, 2),
        "language": info.language if info else "",
        "language_probability": round(info.language_probability, 2) if info else 0,
    }


def run_server(model_name, models_dir):
    """Run as persistent server, reading JSON lines from stdin."""
    model = load_model(model_name, models_dir)

    # Signal readiness
    ready_msg = json.dumps({"status": "ready", "model": model_name})
    sys.stdout.write(ready_msg + "\n")
    sys.stdout.flush()

    # Process requests from stdin
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue

        try:
            req = json.loads(line)
            audio_file = req.get("audio_file", "")

            if not audio_file or not os.path.exists(audio_file):
                result = {"error": f"File not found: {audio_file}"}
            else:
                result = transcribe(model, audio_file)
        except json.JSONDecodeError as e:
            result = {"error": f"Invalid JSON: {e}"}
        except Exception as e:
            result = {"error": str(e)}

        sys.stdout.write(json.dumps(result) + "\n")
        sys.stdout.flush()


def run_oneshot(audio_file, model_name, models_dir):
    """One-shot mode: load model, transcribe, exit."""
    model = load_model(model_name, models_dir)

    if not os.path.exists(audio_file):
        print(f"File not found: {audio_file}", file=sys.stderr)
        sys.exit(1)

    result = transcribe(model, audio_file)
    json.dump(result, sys.stdout)
    sys.stdout.write("\n")


def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("audio_file", nargs="?", help="Path to audio file (one-shot mode)")
    parser.add_argument("--model", default="small", help="Whisper model name")
    parser.add_argument("--models-dir", default="/home/alf/data/models", help="Directory to store models")
    parser.add_argument("--server", action="store_true", help="Run as persistent server (stdin/stdout JSON lines)")
    args = parser.parse_args()

    if args.server or args.audio_file is None:
        run_server(args.model, args.models_dir)
    else:
        run_oneshot(args.audio_file, args.model, args.models_dir)


if __name__ == "__main__":
    main()
