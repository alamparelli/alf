"""Tests for transcribe endpoint with mocked whisper model."""

import io
import pytest
from unittest.mock import patch, MagicMock
from httpx import AsyncClient, ASGITransport

# Patch faster_whisper before importing server
mock_whisper_module = MagicMock()
with patch.dict("sys.modules", {"faster_whisper": mock_whisper_module}):
    from server import app, auth_manager, transcriber
    from auth import RegisterRequest


def _make_segment(text: str, no_speech_prob: float = 0.1, avg_logprob: float = -0.3):
    seg = MagicMock()
    seg.text = text
    seg.no_speech_prob = no_speech_prob
    seg.avg_logprob = avg_logprob
    return seg


def _make_info(language: str = "en", probability: float = 0.95):
    info = MagicMock()
    info.language = language
    info.language_probability = probability
    return info


@pytest.fixture(autouse=True)
def setup_transcriber():
    """Set up a mock model on the transcriber."""
    mock_model = MagicMock()
    transcriber._model = mock_model
    transcriber._semaphore = __import__("asyncio").Semaphore(1)
    yield
    transcriber._model = None
    transcriber._semaphore = None


@pytest.fixture
def token():
    resp = auth_manager.register(RegisterRequest(instance_id="test", secret="test-secret"))
    return resp.token


@pytest.mark.asyncio
async def test_transcribe_valid_audio(token):
    segments = [_make_segment("Hello world")]
    info = _make_info()
    transcriber._model.transcribe.return_value = (segments, info)

    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.post(
            "/transcribe",
            headers={"Authorization": f"Bearer {token}"},
            files={"file": ("test.ogg", b"fake-audio-data", "audio/ogg")},
        )

    assert resp.status_code == 200
    data = resp.json()
    assert data["text"] == "Hello world"
    assert data["language"] == "en"
    assert data["language_probability"] == 0.95
    assert "duration_s" in data


@pytest.mark.asyncio
async def test_hallucination_filtering(token):
    segments = [
        _make_segment("Real speech", no_speech_prob=0.1, avg_logprob=-0.3),
        _make_segment("Hallucinated", no_speech_prob=0.8, avg_logprob=-0.3),
        _make_segment("Low confidence", no_speech_prob=0.1, avg_logprob=-1.5),
        _make_segment("Also real", no_speech_prob=0.2, avg_logprob=-0.5),
    ]
    info = _make_info()
    transcriber._model.transcribe.return_value = (segments, info)

    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.post(
            "/transcribe",
            headers={"Authorization": f"Bearer {token}"},
            files={"file": ("test.ogg", b"fake-audio-data", "audio/ogg")},
        )

    assert resp.status_code == 200
    text = resp.json()["text"]
    assert "Real speech" in text
    assert "Also real" in text
    assert "Hallucinated" not in text
    assert "Low confidence" not in text


@pytest.mark.asyncio
async def test_transcribe_no_auth():
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.post(
            "/transcribe",
            files={"file": ("test.ogg", b"fake-audio-data", "audio/ogg")},
        )
    assert resp.status_code in (401, 403)


@pytest.mark.asyncio
async def test_transcribe_invalid_token():
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.post(
            "/transcribe",
            headers={"Authorization": "Bearer invalid-token"},
            files={"file": ("test.ogg", b"fake-audio-data", "audio/ogg")},
        )
    assert resp.status_code == 401


@pytest.mark.asyncio
async def test_transcribe_empty_file(token):
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.post(
            "/transcribe",
            headers={"Authorization": f"Bearer {token}"},
            files={"file": ("test.ogg", b"", "audio/ogg")},
        )
    assert resp.status_code == 400


@pytest.mark.asyncio
async def test_transcribe_too_large(token):
    large_data = b"x" * (50 * 1024 * 1024 + 1)
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.post(
            "/transcribe",
            headers={"Authorization": f"Bearer {token}"},
            files={"file": ("test.ogg", large_data, "audio/ogg")},
        )
    assert resp.status_code == 413


@pytest.mark.asyncio
async def test_model_not_ready(token):
    transcriber._model = None
    transcriber._semaphore = None

    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as client:
        resp = await client.post(
            "/transcribe",
            headers={"Authorization": f"Bearer {token}"},
            files={"file": ("test.ogg", b"fake-audio-data", "audio/ogg")},
        )
    assert resp.status_code == 503
