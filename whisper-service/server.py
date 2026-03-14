"""FastAPI whisper transcription service."""

import os
import sys
from contextlib import asynccontextmanager

from fastapi import Depends, FastAPI, HTTPException, UploadFile, File
from fastapi.security import HTTPAuthorizationCredentials

from auth import AuthManager, RegisterRequest, RegisterResponse, security
from transcriber import Transcriber, TranscribeResult

MAX_FILE_SIZE = 50 * 1024 * 1024  # 50MB

def read_secret(name: str) -> str:
    """Read a secret from a Docker secret file (NAME_FILE env) or plain env var."""
    file_path = os.environ.get(f"{name}_FILE")
    if file_path:
        try:
            with open(file_path) as f:
                return f.read().strip()
        except OSError:
            pass
    return os.environ.get(name, "")


shared_secret = read_secret("WHISPER_SHARED_SECRET")
if not shared_secret:
    print("FATAL: WHISPER_SHARED_SECRET environment variable is required", file=sys.stderr)
    sys.exit(1)

model_name = os.environ.get("WHISPER_MODEL", "small")
cache_dir = os.environ.get("WHISPER_CACHE_DIR", "/models")
num_workers = int(os.environ.get("WHISPER_WORKERS", "1"))

auth_manager = AuthManager(shared_secret)
transcriber = Transcriber(model_name=model_name, cache_dir=cache_dir, num_workers=num_workers)


@asynccontextmanager
async def lifespan(app: FastAPI):
    transcriber.load_model()
    yield


app = FastAPI(title="Whisper Service", lifespan=lifespan)


@app.get("/health")
async def health():
    return {
        "status": "ready" if transcriber.ready else "loading",
        "model": transcriber.model_name,
        "queue_size": transcriber.queue_size,
        "registered_instances": auth_manager.registered_count,
        "workers": transcriber.num_workers,
    }


@app.post("/register", response_model=RegisterResponse)
async def register(req: RegisterRequest):
    return auth_manager.register(req)


@app.post("/transcribe", response_model=TranscribeResult)
async def transcribe(
    file: UploadFile = File(...),
    credentials: HTTPAuthorizationCredentials = Depends(security),
):
    auth_manager.validate_token(credentials)

    content = await file.read()
    if len(content) == 0:
        raise HTTPException(status_code=400, detail="Empty file")
    if len(content) > MAX_FILE_SIZE:
        raise HTTPException(status_code=413, detail="File exceeds 50MB limit")

    return await transcriber.transcribe(content, file.filename or "audio.ogg")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run("server:app", host="0.0.0.0", port=8000)
