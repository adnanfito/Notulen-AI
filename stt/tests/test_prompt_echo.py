"""Whisper sometimes emits its own initial_prompt as if it had been spoken.

Left in, it pollutes the transcript and then the meeting notes. These cases are
built from a real leak observed in a 1.7-hour meeting.
"""

import pytest

from app.config import DEFAULT_INITIAL_PROMPT
from app.transcription import is_prompt_echo

PROMPT = DEFAULT_INITIAL_PROMPT


@pytest.mark.parametrize(
    "text",
    [
        # Verbatim, as it appeared in the real transcript.
        "Rekaman rapat, angka, angka, dan istilah asing sesuai yang diucapkan, "
        "dengan tanda baca yang wajar.",
        DEFAULT_INITIAL_PROMPT,
        "Tulis nama orang, angka, tanggal, dan istilah asing sesuai yang diucapkan.",
        "Bahasa Indonesia, English, atau campuran Indo-English.",
    ],
)
def test_detects_prompt_echo(text):
    assert is_prompt_echo(text, PROMPT), f"missed an echo: {text[:60]}"


@pytest.mark.parametrize(
    "text",
    [
        # Real speech from the same meeting. None of this may be discarded.
        "Jadi untuk kelompok sasaran kita fokus ke kader dan pasien dulu ya.",
        "Keluhan naik dari rata-rata lima belas per hari jadi empat puluh.",
        # Incidental overlap: the words exist in the prompt, the sentence does not.
        "Rekaman rapat kemarin sudah saya kirim ke grup.",
        "Tolong tulis nama orang yang hadir di daftar absensi.",
        "Bahasa Indonesia memang lebih enak dipakai di forum ini.",
        "Kita bahas angka dan tanggal peluncurannya minggu depan.",
    ],
)
def test_keeps_real_speech(text):
    assert not is_prompt_echo(text, PROMPT), f"discarded real speech: {text[:60]}"


def test_no_prompt_means_nothing_is_discarded():
    assert not is_prompt_echo("apa pun isinya", "")


def test_empty_text_is_not_an_echo():
    assert not is_prompt_echo("", PROMPT)
    assert not is_prompt_echo("   ", PROMPT)


def test_short_fragments_are_judged_conservatively():
    # Two prompt words alone are far too weak a signal to drop a segment.
    assert not is_prompt_echo("Rekaman rapat", PROMPT)
    # A custom prompt shorter than the run length can never match.
    assert not is_prompt_echo("halo semuanya", "rapat")
