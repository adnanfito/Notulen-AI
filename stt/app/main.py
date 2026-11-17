"""FastAPI entrypoint for the local STT service.

Exposes /health and POST /transcribe. Audio arrives, is transcribed on this
machine, and the temporary file is deleted before the response is returned.
"""

import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, File, Form, Request, UploadFile
from fastapi.responses import JSONResponse

from .config import settings
from .model import ModelHolder
from .transcription import AudioDecodeError, in_flight, transcribe
from .uploads import EmptyUpload, UploadTooLarge, stored_upload

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)
# huggingface_hub/httpx log one INFO line per HTTP request, which buries the
# handful of lines that actually matter during model download.
for noisy in ("httpx", "huggingface_hub.utils._http", "hf_xet"):
    logging.getLogger(noisy).setLevel(logging.WARNING)

logger = logging.getLogger("stt")

holder = ModelHolder(settings)


@asynccontextmanager
async def lifespan(app: FastAPI):
    logger.info(
        "stt_starting model=%s device=%s compute_type=%s",
        settings.model,
        settings.device,
        settings.compute_type,
    )
    holder.load()
    app.state.holder = holder
    app.state.settings = settings
    yield
    holder.unload()
    logger.info("stt_stopped")


app = FastAPI(
    title="notulen-ai STT",
    version="0.2.0",
    lifespan=lifespan,
)


def error(status: int, code: str, message: str) -> JSONResponse:
    """Mirror the backend's error envelope (PRD §36) so callers parse one shape."""
    return JSONResponse(
        status_code=status,
        content={"error": {"code": code, "message": message}},
    )


@app.get("/health")
async def health() -> dict:
    info = holder.info()
    return {
        "status": "ok" if holder.loaded else "degraded",
        "service": "stt",
        "busy": in_flight(),
        **info,
    }


@app.post("/transcribe")
async def transcribe_endpoint(
    request: Request,
    file: UploadFile = File(...),
    language: str | None = Form(default=None),
    initial_prompt: str | None = Form(default=None),
):
    active = getattr(request.app.state, "holder", holder)
    config = getattr(request.app.state, "settings", settings)

    if not active.loaded:
        return error(503, "STT_UNAVAILABLE", "Speech model is not loaded.")

    # An explicit "auto" from the caller means detect, same as an unset value.
    resolved_language = language.strip() if language else ""
    if resolved_language.lower() in ("", "auto"):
        resolved_language = config.resolved_language or None

    prompt = initial_prompt if initial_prompt is not None else config.initial_prompt
    prompt = prompt.strip() or None if prompt else None

    max_bytes = config.max_upload_mb << 20

    try:
        with stored_upload(
            config.temp_dir, file.filename, max_bytes, file.file
        ) as (path, size):
            logger.info("transcription_started bytes=%d language=%s", size, resolved_language or "auto")
            result = await transcribe(
                active, config, path, resolved_language, prompt
            )
    except UploadTooLarge:
        logger.warning("upload_rejected reason=too_large limit_mb=%d", config.max_upload_mb)
        return error(
            413,
            "INVALID_AUDIO",
            f"Audio exceeds the {config.max_upload_mb} MB limit.",
        )
    except EmptyUpload:
        logger.warning("upload_rejected reason=empty")
        return error(400, "INVALID_AUDIO", "Audio file is empty.")
    except AudioDecodeError as exc:
        logger.warning("upload_rejected reason=undecodable error=%s", exc)
        return error(400, "INVALID_AUDIO", "Audio could not be decoded.")
    except Exception:
        logger.exception("transcription_failed")
        return error(500, "STT_FAILED", "Transcription failed.")

    # Audio shorter than the floor carries no usable speech. It is a normal
    # outcome, not an error: the meeting keeps going and the caller decides
    # what an empty chunk means.
    too_short = result.duration_seconds < config.min_audio_seconds
    if too_short:
        logger.info("transcription_empty reason=too_short duration_sec=%.2f", result.duration_seconds)

    return {
        "text": "" if too_short else result.text,
        "language": result.language,
        "language_probability": result.language_probability,
        "duration_seconds": result.duration_seconds,
        "segment_count": 0 if too_short else result.segment_count,
        "processing_seconds": result.processing_seconds,
    }
