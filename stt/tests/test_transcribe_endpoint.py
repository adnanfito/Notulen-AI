"""Endpoint tests driven by a fake model, so they need no GPU and no weights."""

import io
from dataclasses import dataclass

import pytest
from fastapi.testclient import TestClient

from app import main
from app.config import load_settings


@dataclass
class FakeSegment:
    text: str


@dataclass
class FakeInfo:
    language: str = "id"
    language_probability: float = 0.97
    duration: float = 12.5


class FakeModel:
    """Stands in for WhisperModel, recording how it was called."""

    def __init__(self, segments=None, info=None, raises=None):
        self.segments = segments if segments is not None else [FakeSegment(" Halo semuanya. ")]
        self.info = info or FakeInfo()
        self.raises = raises
        self.calls = []

    def transcribe(self, path, **kwargs):
        self.calls.append({"path": path, **kwargs})
        if self.raises:
            raise self.raises

        def generate():
            yield from self.segments

        return generate(), self.info


class FakeHolder:
    def __init__(self, model=None, loaded=True):
        self.model = model if model is not None else FakeModel()
        self._loaded = loaded

    @property
    def loaded(self):
        return self._loaded

    def info(self):
        return {
            "model": "small",
            "device_requested": "cuda",
            "device": "cuda",
            "compute_type": "int8",
            "language": "auto",
            "vad_filter": True,
            "model_loaded": self._loaded,
        }


@pytest.fixture
def settings(tmp_path, monkeypatch):
    monkeypatch.setenv("STT_TEMP_DIR", str(tmp_path))
    monkeypatch.setenv("STT_MAX_UPLOAD_MB", "1")
    monkeypatch.setenv("WHISPER_INITIAL_PROMPT", "prompt dari config")
    monkeypatch.setenv("WHISPER_LANGUAGE", "auto")
    return load_settings()


def make_client(holder, settings):
    client = TestClient(main.app)
    # Bypass lifespan (which would load a real model) and inject the fake.
    client.app.state.holder = holder
    client.app.state.settings = settings
    return client


def upload(data=b"pretend-webm-bytes", name="chunk.webm"):
    return {"file": (name, io.BytesIO(data), "audio/webm")}


def test_transcribes_and_returns_prd_shape(settings):
    holder = FakeHolder()
    client = make_client(holder, settings)

    response = client.post("/transcribe", files=upload())

    assert response.status_code == 200
    body = response.json()
    assert body["text"] == "Halo semuanya."
    assert body["language"] == "id"
    assert body["duration_seconds"] == 12.5
    assert body["segment_count"] == 1


def test_temp_file_is_deleted_after_success(settings, tmp_path):
    holder = FakeHolder()
    client = make_client(holder, settings)

    client.post("/transcribe", files=upload())

    assert list(tmp_path.iterdir()) == []


def test_config_prompt_is_used_when_caller_sends_none(settings):
    holder = FakeHolder()
    client = make_client(holder, settings)

    client.post("/transcribe", files=upload())

    assert holder.model.calls[0]["initial_prompt"] == "prompt dari config"


def test_caller_prompt_overrides_config(settings):
    holder = FakeHolder()
    client = make_client(holder, settings)

    client.post("/transcribe", files=upload(), data={"initial_prompt": "rapat marketing"})

    assert holder.model.calls[0]["initial_prompt"] == "rapat marketing"


def test_language_auto_means_detect(settings):
    holder = FakeHolder()
    client = make_client(holder, settings)

    client.post("/transcribe", files=upload(), data={"language": "auto"})

    assert holder.model.calls[0]["language"] is None


def test_explicit_language_is_forwarded(settings):
    holder = FakeHolder()
    client = make_client(holder, settings)

    client.post("/transcribe", files=upload(), data={"language": "id"})

    assert holder.model.calls[0]["language"] == "id"


def test_vad_is_enabled_by_default(settings):
    holder = FakeHolder()
    client = make_client(holder, settings)

    client.post("/transcribe", files=upload())

    assert holder.model.calls[0]["vad_filter"] is True


def test_invalid_audio_returns_400(settings, tmp_path):
    holder = FakeHolder(FakeModel(raises=ValueError("Invalid data found when processing input")))
    client = make_client(holder, settings)

    response = client.post("/transcribe", files=upload(b"this is not audio"))

    assert response.status_code == 400
    assert response.json()["error"]["code"] == "INVALID_AUDIO"
    # A rejected upload must still not leave audio behind.
    assert list(tmp_path.iterdir()) == []


def test_empty_upload_returns_400(settings):
    holder = FakeHolder()
    client = make_client(holder, settings)

    response = client.post("/transcribe", files=upload(b""))

    assert response.status_code == 400
    assert response.json()["error"]["code"] == "INVALID_AUDIO"


def test_oversized_upload_returns_413(settings):
    holder = FakeHolder()
    client = make_client(holder, settings)

    response = client.post("/transcribe", files=upload(b"x" * (2 << 20)))

    assert response.status_code == 413
    assert response.json()["error"]["code"] == "INVALID_AUDIO"


def test_silence_yields_empty_text_not_an_error(settings):
    """VAD strips silence, so a quiet chunk transcribes to nothing. That is a
    normal outcome — the meeting continues."""
    holder = FakeHolder(FakeModel(segments=[], info=FakeInfo(duration=90.0)))
    client = make_client(holder, settings)

    response = client.post("/transcribe", files=upload())

    assert response.status_code == 200
    assert response.json()["text"] == ""
    assert response.json()["segment_count"] == 0


def test_very_short_audio_yields_empty_text(settings):
    holder = FakeHolder(FakeModel(info=FakeInfo(duration=0.05)))
    client = make_client(holder, settings)

    response = client.post("/transcribe", files=upload())

    assert response.status_code == 200
    assert response.json()["text"] == ""


def test_returns_503_when_model_not_loaded(settings):
    holder = FakeHolder(loaded=False)
    client = make_client(holder, settings)

    response = client.post("/transcribe", files=upload())

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "STT_UNAVAILABLE"
