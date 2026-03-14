"""Integration tests — require WHISPER_INTEGRATION=1 and download tiny model."""

import asyncio
import os
import pytest
from unittest.mock import patch
from httpx import AsyncClient, ASGITransport

pytestmark = pytest.mark.skipif(
    os.environ.get("WHISPER_INTEGRATION") != "1",
    reason="Set WHISPER_INTEGRATION=1 to run integration tests",
)

# Generate a minimal valid WAV file (silence, 1 second, 16kHz mono)
def _silent_wav(duration_s: float = 1.0) -> bytes:
    import struct
    sample_rate = 16000
    num_samples = int(sample_rate * duration_s)
    data_size = num_samples * 2  # 16-bit samples
    header = struct.pack(
        "<4sI4s4sIHHIIHH4sI",
        b"RIFF", 36 + data_size, b"WAVE",
        b"fmt ", 16, 1, 1, sample_rate, sample_rate * 2, 2, 16,
        b"data", data_size,
    )
    return header + b"\x00" * data_size


@pytest.fixture(scope="module")
def _setup_real_model():
    """Load real tiny model for integration tests."""
    os.environ["WHISPER_SHARED_SECRET"] = "integration-secret"
    os.environ["WHISPER_MODEL"] = "tiny"
    os.environ["WHISPER_CACHE_DIR"] = os.path.join(os.path.dirname(__file__), ".models")

    # Re-import with real model
    import importlib
    import server as srv
    srv.shared_secret = "integration-secret"
    srv.auth_manager = __import__("auth").AuthManager("integration-secret")
    srv.transcriber = __import__("transcriber").Transcriber(
        model_name="tiny",
        cache_dir=os.environ["WHISPER_CACHE_DIR"],
        num_workers=1,
    )
    srv.transcriber.load_model()
    return srv


@pytest.mark.asyncio
async def test_full_flow(_setup_real_model):
    srv = _setup_real_model
    transport = ASGITransport(app=srv.app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        # Register
        resp = await client.post("/register", json={"instance_id": "int-test", "secret": "integration-secret"})
        assert resp.status_code == 200
        token = resp.json()["token"]

        # Transcribe silent audio
        wav = _silent_wav(1.0)
        resp = await client.post(
            "/transcribe",
            headers={"Authorization": f"Bearer {token}"},
            files={"file": ("test.wav", wav, "audio/wav")},
        )
        assert resp.status_code == 200
        data = resp.json()
        assert "text" in data
        assert "duration_s" in data
        assert "language" in data


@pytest.mark.asyncio
async def test_concurrent_requests(_setup_real_model):
    srv = _setup_real_model
    transport = ASGITransport(app=srv.app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.post("/register", json={"instance_id": "conc-test", "secret": "integration-secret"})
        token = resp.json()["token"]

        wav = _silent_wav(0.5)

        async def do_transcribe():
            return await client.post(
                "/transcribe",
                headers={"Authorization": f"Bearer {token}"},
                files={"file": ("test.wav", wav, "audio/wav")},
            )

        results = await asyncio.gather(do_transcribe(), do_transcribe(), do_transcribe())
        for r in results:
            assert r.status_code == 200


@pytest.mark.asyncio
async def test_health_during_transcription(_setup_real_model):
    srv = _setup_real_model
    transport = ASGITransport(app=srv.app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.get("/health")
        assert resp.status_code == 200
        data = resp.json()
        assert data["status"] == "ready"
        assert data["model"] == "tiny"
