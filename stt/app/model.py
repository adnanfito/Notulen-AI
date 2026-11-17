"""Single shared Whisper model instance.

Exactly one model is loaded for the lifetime of the process (PRD §5). Loading
per request would both blow the VRAM budget and make every chunk pay the load
cost.
"""

import logging

from faster_whisper import WhisperModel

from .config import Settings

logger = logging.getLogger(__name__)


class ModelHolder:
    """Owns the one WhisperModel instance and how it was actually loaded."""

    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.model: WhisperModel | None = None
        self.device_requested: str = settings.device
        self.device: str = settings.device
        self.compute_type: str = settings.compute_type
        self.load_error: str | None = None

    @property
    def loaded(self) -> bool:
        return self.model is not None

    def load(self) -> None:
        try:
            self.model = self._build(self.settings.device, self.settings.compute_type)
            self.device = self.settings.device
            self.compute_type = self.settings.compute_type
        except Exception as exc:  # noqa: BLE001 - any CUDA/driver failure counts
            if self.settings.device == "cpu":
                self.load_error = str(exc)
                logger.error("model_load_failed model=%s error=%s", self.settings.model, exc)
                raise

            # CPU fallback keeps a GPU-less machine usable for development
            # (PRD §5). It is logged loudly and reported by /health, because
            # silently running 10x slower is worse than failing.
            logger.warning(
                "cuda_unavailable_falling_back_to_cpu model=%s error=%s",
                self.settings.model,
                exc,
            )
            self.load_error = str(exc)
            self.model = self._build("cpu", "int8")
            self.device = "cpu"
            self.compute_type = "int8"

        logger.info(
            "model_loaded model=%s device=%s compute_type=%s language=%s vad=%s",
            self.settings.model,
            self.device,
            self.compute_type,
            self.settings.language,
            self.settings.vad_filter,
        )

    def _build(self, device: str, compute_type: str) -> WhisperModel:
        logger.info(
            "model_loading model=%s device=%s compute_type=%s",
            self.settings.model,
            device,
            compute_type,
        )
        return WhisperModel(
            model_size_or_path=self.settings.model,
            device=device,
            compute_type=compute_type,
            cpu_threads=self.settings.cpu_threads,
            download_root=self.settings.download_root,
        )

    def unload(self) -> None:
        self.model = None

    def info(self) -> dict:
        return {
            "model": self.settings.model,
            "device_requested": self.device_requested,
            "device": self.device,
            "compute_type": self.compute_type,
            "language": self.settings.language,
            "vad_filter": self.settings.vad_filter,
            "model_loaded": self.loaded,
        }
