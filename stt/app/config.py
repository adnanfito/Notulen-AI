"""Environment-driven configuration for the STT service.

The Whisper model is never hard-coded (PRD §5): every knob below comes from
the environment so the model can be swapped without touching source.
"""

import os
from dataclasses import dataclass

# Deliberately domain-neutral. The prompt biases Whisper's decoding, so naming
# a field (IT, medicine, law) pulls transcripts of every other kind of meeting
# toward that vocabulary. This sets language and register only; set
# WHISPER_INITIAL_PROMPT to add domain terms when a meeting needs them.
DEFAULT_INITIAL_PROMPT = (
    "Rekaman rapat. Bahasa Indonesia, English, atau campuran Indo-English. "
    "Tulis nama orang, angka, tanggal, dan istilah asing sesuai yang diucapkan, "
    "dengan tanda baca yang wajar."
)


def _env(key: str, fallback: str) -> str:
    value = os.getenv(key, "").strip()
    return value or fallback


def _env_int(key: str, fallback: int) -> int:
    try:
        return int(os.getenv(key, "").strip())
    except (TypeError, ValueError):
        return fallback


def _env_float(key: str, fallback: float) -> float:
    try:
        return float(os.getenv(key, "").strip())
    except (TypeError, ValueError):
        return fallback


def _env_bool(key: str, fallback: bool) -> bool:
    value = os.getenv(key, "").strip().lower()
    if value in ("1", "true", "yes", "on"):
        return True
    if value in ("0", "false", "no", "off"):
        return False
    return fallback


@dataclass(frozen=True)
class Settings:
    model: str
    device: str
    compute_type: str
    language: str
    initial_prompt: str
    beam_size: int
    vad_filter: bool
    cpu_threads: int
    download_root: str
    max_upload_mb: int
    temp_dir: str
    min_audio_seconds: float

    @property
    def resolved_language(self) -> str | None:
        """faster-whisper expects None for auto-detection, not the string."""
        return None if self.language.lower() in ("auto", "") else self.language


def load_settings() -> Settings:
    return Settings(
        model=_env("WHISPER_MODEL", "small"),
        device=_env("WHISPER_DEVICE", "cuda"),
        compute_type=_env("WHISPER_COMPUTE_TYPE", "int8"),
        language=_env("WHISPER_LANGUAGE", "auto"),
        initial_prompt=_env("WHISPER_INITIAL_PROMPT", DEFAULT_INITIAL_PROMPT),
        beam_size=_env_int("WHISPER_BEAM_SIZE", 5),
        vad_filter=_env_bool("WHISPER_VAD_FILTER", True),
        cpu_threads=_env_int("WHISPER_CPU_THREADS", 4),
        download_root=_env("WHISPER_DOWNLOAD_ROOT", "/models"),
        max_upload_mb=_env_int("STT_MAX_UPLOAD_MB", 128),
        temp_dir=_env("STT_TEMP_DIR", "/tmp/stt-uploads"),
        min_audio_seconds=_env_float("STT_MIN_AUDIO_SECONDS", 0.3),
    )


settings = load_settings()
