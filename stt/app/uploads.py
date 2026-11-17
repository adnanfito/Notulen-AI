"""Safe handling of uploaded audio.

Uploads are untrusted input (PRD §31): they are size-capped, written under a
generated name, never executed, and always deleted once transcription ends.
"""

import logging
import os
import uuid
from contextlib import contextmanager
from pathlib import Path

logger = logging.getLogger(__name__)

CHUNK_BYTES = 1 << 20  # 1 MiB


class UploadTooLarge(Exception):
    """Upload exceeded the configured size cap."""


class EmptyUpload(Exception):
    """Upload contained no bytes."""


def safe_suffix(filename: str | None) -> str:
    """Return a conservative extension derived from the client's filename.

    The client's name is never used as a path component — only its extension
    is consulted, and only when it is short and alphanumeric. Anything else
    (traversal attempts, absurd extensions, no name at all) falls back to a
    neutral default. PyAV sniffs the container from content anyway, so the
    extension is a hint, not a contract.
    """
    if not filename:
        return ".bin"
    suffix = Path(filename).suffix.lower()
    if 1 < len(suffix) <= 6 and suffix[1:].isalnum():
        return suffix
    return ".bin"


@contextmanager
def stored_upload(temp_dir: str, filename: str | None, max_bytes: int, reader):
    """Stream an upload to a temp file, yield its path, then always delete it.

    `reader` is any object with a `.read(n)` returning bytes (sync). Streaming
    in fixed chunks keeps a long meeting chunk from being held in RAM twice
    (PRD §39), and lets the size cap trip before the whole body is buffered.
    """
    directory = Path(temp_dir)
    directory.mkdir(parents=True, exist_ok=True)

    path = directory / f"{uuid.uuid4().hex}{safe_suffix(filename)}"
    total = 0

    try:
        with open(path, "wb") as handle:
            while True:
                block = reader.read(CHUNK_BYTES)
                if not block:
                    break
                total += len(block)
                if total > max_bytes:
                    raise UploadTooLarge(f"upload exceeds {max_bytes} bytes")
                handle.write(block)

        if total == 0:
            raise EmptyUpload("upload contained no bytes")

        yield str(path), total
    finally:
        # Temporary audio must not outlive the request (PRD §31).
        try:
            os.unlink(path)
        except FileNotFoundError:
            pass
        except OSError as exc:
            logger.warning("temp_cleanup_failed path=%s error=%s", path.name, exc)
