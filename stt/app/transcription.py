"""Transcription execution.

Everything that touches the GPU funnels through here so the single-job
guarantee holds no matter how many requests arrive at once.
"""

import asyncio
import logging
import re
import time
from dataclasses import dataclass

from .config import Settings
from .model import ModelHolder

logger = logging.getLogger(__name__)

# One GPU, one job at a time (PRD §39). A second concurrent decode on a
# 1.5 GB budget risks an OOM that takes the whole service down.
transcribe_lock = asyncio.Semaphore(1)


def in_flight() -> bool:
    """True while a transcription currently holds the lock."""
    return transcribe_lock.locked()


def _normalize(text: str) -> list[str]:
    """Lowercase word list, punctuation stripped — for comparing wording only."""
    return re.findall(r"[a-z0-9]+", text.lower())


# A run this long matching the prompt is regurgitation, not coincidence. Short
# overlaps ("rekaman rapat") happen in real speech and must survive.
_ECHO_RUN = 6


def is_prompt_echo(text: str, prompt: str) -> bool:
    """True when a segment is Whisper repeating its own initial_prompt.

    Whisper conditions decoding on initial_prompt, and on unclear or quiet
    audio it sometimes emits that prompt back as if it had been spoken. Left
    in, it pollutes the transcript and then the meeting notes.
    """
    if not prompt:
        return False

    words = _normalize(text)
    if not words:
        return False

    prompt_words = _normalize(prompt)
    if len(prompt_words) < _ECHO_RUN:
        return False

    prompt_runs = {
        tuple(prompt_words[i:i + _ECHO_RUN])
        for i in range(len(prompt_words) - _ECHO_RUN + 1)
    }

    if len(words) < _ECHO_RUN:
        # Too short to match a run; only drop it if the whole thing is prompt.
        return len(words) >= 3 and all(w in prompt_words for w in words)

    matched = sum(
        1
        for i in range(len(words) - _ECHO_RUN + 1)
        if tuple(words[i:i + _ECHO_RUN]) in prompt_runs
    )
    windows = len(words) - _ECHO_RUN + 1

    # Half the segment tracking the prompt verbatim means the segment is echo.
    return matched / windows >= 0.5


class AudioDecodeError(Exception):
    """The upload could not be decoded as audio."""


@dataclass
class TranscriptionResult:
    text: str
    language: str
    language_probability: float
    duration_seconds: float
    segment_count: int
    processing_seconds: float


def _run(
    holder: ModelHolder,
    settings: Settings,
    path: str,
    language: str | None,
    initial_prompt: str | None,
) -> TranscriptionResult:
    """Blocking transcription. Always called via a worker thread."""
    if holder.model is None:
        raise RuntimeError("model is not loaded")

    started = time.monotonic()

    try:
        segments, info = holder.model.transcribe(
            path,
            language=language,
            initial_prompt=initial_prompt,
            beam_size=settings.beam_size,
            vad_filter=settings.vad_filter,
        )
        # transcribe() is lazy: decoding errors surface only once the segment
        # generator is consumed, so the list() must stay inside the try.
        collected = list(segments)
    except Exception as exc:  # noqa: BLE001
        # PyAV raises a family of codec/IO errors that share no common base
        # class worth matching on, and any of them mean the same thing to the
        # caller: this payload is not usable audio.
        raise AudioDecodeError(str(exc)) from exc

    kept, echoes = [], 0
    for segment in collected:
        piece = segment.text.strip()
        if not piece:
            continue
        if is_prompt_echo(piece, initial_prompt or ""):
            echoes += 1
            continue
        kept.append(piece)

    if echoes:
        logger.warning("prompt_echo_discarded segments=%d", echoes)

    text = " ".join(kept)

    return TranscriptionResult(
        text=text.strip(),
        language=info.language or "",
        language_probability=round(float(info.language_probability or 0.0), 4),
        duration_seconds=round(float(info.duration or 0.0), 2),
        segment_count=len(kept),
        processing_seconds=round(time.monotonic() - started, 2),
    )


async def transcribe(
    holder: ModelHolder,
    settings: Settings,
    path: str,
    language: str | None = None,
    initial_prompt: str | None = None,
) -> TranscriptionResult:
    """Transcribe one audio file, serialized against every other request.

    The blocking decode runs in a worker thread so the event loop stays free to
    answer /health while a long chunk is being processed.
    """
    async with transcribe_lock:
        result = await asyncio.to_thread(
            _run, holder, settings, path, language, initial_prompt
        )

    # Never log the transcript itself (PRD §37) — only its shape.
    logger.info(
        "transcription_completed language=%s duration_sec=%.2f segments=%d "
        "chars=%d processing_sec=%.2f",
        result.language,
        result.duration_seconds,
        result.segment_count,
        len(result.text),
        result.processing_seconds,
    )
    return result
