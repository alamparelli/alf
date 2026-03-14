"""Whisper model wrapper with semaphore-controlled queue."""

import asyncio
import os
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor
from pydantic import BaseModel


class TranscribeResult(BaseModel):
    text: str
    duration_s: float
    language: str
    language_probability: float


class Transcriber:
    def __init__(self, model_name: str = "small", cache_dir: str = "/models", num_workers: int = 1):
        self.model_name = model_name
        self.cache_dir = cache_dir
        self.num_workers = num_workers
        self._model = None
        self._semaphore: asyncio.Semaphore | None = None
        self._executor = ThreadPoolExecutor(max_workers=num_workers)
        self._queue_waiters = 0

    @property
    def ready(self) -> bool:
        return self._model is not None

    @property
    def queue_size(self) -> int:
        return self._queue_waiters

    def load_model(self):
        from faster_whisper import WhisperModel

        os.makedirs(self.cache_dir, exist_ok=True)
        self._model = WhisperModel(
            self.model_name,
            device="cpu",
            compute_type="int8",
            download_root=self.cache_dir,
        )
        self._semaphore = asyncio.Semaphore(self.num_workers)

    async def transcribe(self, audio_bytes: bytes, filename: str) -> TranscribeResult:
        if not self.ready:
            from fastapi import HTTPException
            raise HTTPException(status_code=503, detail="Model not loaded")

        self._queue_waiters += 1
        try:
            async with self._semaphore:
                self._queue_waiters -= 1
                loop = asyncio.get_event_loop()
                return await loop.run_in_executor(
                    self._executor, self._transcribe_sync, audio_bytes, filename
                )
        except Exception:
            # Decrement if we never acquired the semaphore
            if self._queue_waiters > 0:
                self._queue_waiters -= 1
            raise

    def _transcribe_sync(self, audio_bytes: bytes, filename: str) -> TranscribeResult:
        suffix = os.path.splitext(filename)[1] or ".ogg"
        with tempfile.NamedTemporaryFile(suffix=suffix, delete=True) as tmp:
            tmp.write(audio_bytes)
            tmp.flush()

            start = time.time()
            segments, info = self._model.transcribe(tmp.name)

            good_segments = []
            for seg in segments:
                if hasattr(seg, "no_speech_prob") and seg.no_speech_prob > 0.6:
                    continue
                if hasattr(seg, "avg_logprob") and seg.avg_logprob < -1.0:
                    continue
                good_segments.append(seg.text)

            text = " ".join(good_segments).strip()
            elapsed = time.time() - start

            return TranscribeResult(
                text=text,
                duration_s=round(elapsed, 2),
                language=info.language if info else "",
                language_probability=round(info.language_probability, 2) if info else 0,
            )
