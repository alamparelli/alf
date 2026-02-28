#!/usr/bin/env python3
"""Whisper transcription subprocess for ALF daemon.

Usage: transcribe.py <audio_file> [--model small] [--models-dir /path/to/models]

Outputs JSON to stdout: {"text": "transcribed text", "duration_s": 3.5}
On error, exits with non-zero code and prints error to stderr.
"""

import json
import sys
import os
import time

def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument("audio_file", help="Path to audio file (OGG, WAV, etc)")
    parser.add_argument("--model", default="small", help="Whisper model name")
    parser.add_argument("--models-dir", default="/home/node/data/models", help="Directory to store models")
    args = parser.parse_args()

    if not os.path.exists(args.audio_file):
        print(f"File not found: {args.audio_file}", file=sys.stderr)
        sys.exit(1)

    # Ensure models directory exists
    os.makedirs(args.models_dir, exist_ok=True)

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

    # Load model (downloads on first use)
    model = WhisperModel(
        args.model,
        device="cpu",
        compute_type="int8",
        download_root=args.models_dir,
    )

    # Transcribe
    start = time.time()
    segments, info = model.transcribe(args.audio_file)
    text = " ".join(seg.text for seg in segments).strip()
    elapsed = time.time() - start

    # Output result as JSON
    result = {
        "text": text,
        "duration_s": round(elapsed, 2),
        "language": info.language if info else "",
        "language_probability": round(info.language_probability, 2) if info else 0,
    }
    json.dump(result, sys.stdout)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
