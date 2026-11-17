import io
import os
from pathlib import Path

import pytest

from app.uploads import EmptyUpload, UploadTooLarge, safe_suffix, stored_upload


@pytest.mark.parametrize(
    "filename,expected",
    [
        ("chunk.webm", ".webm"),
        ("CHUNK.WEBM", ".webm"),
        ("recording.ogg", ".ogg"),
        (None, ".bin"),
        ("noextension", ".bin"),
        # A traversal attempt must never reach the filesystem path.
        ("../../../../etc/passwd", ".bin"),
        ("evil.sh; rm -rf /", ".bin"),
        ("x." + "a" * 40, ".bin"),
    ],
)
def test_safe_suffix(filename, expected):
    assert safe_suffix(filename) == expected


def test_stored_upload_writes_then_deletes(tmp_path):
    payload = b"fake audio bytes"
    seen = {}

    with stored_upload(str(tmp_path), "chunk.webm", 1024, io.BytesIO(payload)) as (path, size):
        seen["path"] = path
        assert size == len(payload)
        assert Path(path).read_bytes() == payload
        assert Path(path).suffix == ".webm"

    # Temporary audio must not outlive the request (PRD §31).
    assert not Path(seen["path"]).exists()


def test_stored_upload_deletes_even_when_body_raises(tmp_path):
    seen = {}

    with pytest.raises(RuntimeError):
        with stored_upload(str(tmp_path), "chunk.webm", 1024, io.BytesIO(b"data")) as (path, _):
            seen["path"] = path
            raise RuntimeError("transcription blew up")

    assert not Path(seen["path"]).exists()
    assert os.listdir(tmp_path) == []


def test_stored_upload_rejects_oversized_and_leaves_nothing(tmp_path):
    with pytest.raises(UploadTooLarge):
        with stored_upload(str(tmp_path), "big.webm", 10, io.BytesIO(b"x" * 5000)):
            pass

    assert os.listdir(tmp_path) == []


def test_stored_upload_rejects_empty(tmp_path):
    with pytest.raises(EmptyUpload):
        with stored_upload(str(tmp_path), "empty.webm", 1024, io.BytesIO(b"")):
            pass

    assert os.listdir(tmp_path) == []


def test_filenames_do_not_collide(tmp_path):
    paths = []
    for _ in range(5):
        with stored_upload(str(tmp_path), "same.webm", 1024, io.BytesIO(b"a")) as (path, _):
            paths.append(path)
    assert len(set(paths)) == 5
