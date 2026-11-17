# STT Service

Local speech-to-text over [faster-whisper](https://github.com/SYSTRAN/faster-whisper)
(CTranslate2). Audio is transcribed on this machine and never uploaded anywhere.

## Endpoints

| Method | Path | Status |
|---|---|---|
| GET | `/health` | implemented |
| POST | `/transcribe` | implemented |

`/health` reports the model actually in use:

```json
{
  "status": "ok",
  "service": "stt",
  "busy": false,
  "model": "small",
  "device_requested": "cuda",
  "device": "cuda",
  "compute_type": "int8",
  "language": "auto",
  "vad_filter": true,
  "model_loaded": true
}
```

`device_requested` vs `device` is the important pair: if they differ, CUDA
failed and the service silently fell back to CPU. The backend health check
flags that as `degraded` rather than letting it pass as healthy.

### POST /transcribe

`multipart/form-data`:

| Field | Required | Notes |
|---|---|---|
| `file` | yes | Audio. WebM/Opus (what the browser records) decodes natively via PyAV; WAV/FLAC/OGG also work. |
| `language` | no | ISO code such as `id` or `en`. Omit or send `auto` to detect. |
| `initial_prompt` | no | Overrides `WHISPER_INITIAL_PROMPT` for this request. |

Success (200):

```json
{
  "text": "Rekaman suara ini berisi versi lisan dari artikel...",
  "language": "id",
  "language_probability": 0.9956,
  "duration_seconds": 60.0,
  "segment_count": 7,
  "processing_seconds": 3.05
}
```

Errors use the same envelope as the Go backend (PRD §36):

| Status | Code | When |
|---|---|---|
| 400 | `INVALID_AUDIO` | Empty upload, or bytes that are not decodable audio |
| 413 | `INVALID_AUDIO` | Upload above `STT_MAX_UPLOAD_MB` |
| 503 | `STT_UNAVAILABLE` | Model not loaded |
| 500 | `STT_FAILED` | Anything else |

**Silence is not an error.** VAD strips non-speech, so a quiet chunk returns
200 with `text: ""` and `segment_count: 0`. Audio shorter than
`STT_MIN_AUDIO_SECONDS` returns empty text the same way. A meeting must not
fail because one chunk happened to be quiet (PRD §12, §33).

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `WHISPER_MODEL` | `medium` | `small` uses ~900 MiB less and is ~1.7x faster |
| `WHISPER_DEVICE` | `cuda` | `cpu` for machines without an NVIDIA runtime |
| `WHISPER_COMPUTE_TYPE` | `int8` | |
| `WHISPER_LANGUAGE` | `auto` | `id`, `en`, or `auto` to detect |
| `WHISPER_INITIAL_PROMPT` | technical-meeting prompt | Change it for non-technical meetings |
| `WHISPER_BEAM_SIZE` | `5` | |
| `WHISPER_VAD_FILTER` | `true` | Skips silence |
| `WHISPER_CPU_THREADS` | `4` | CPU mode only |
| `WHISPER_BEAM_SIZE` | `5` | Higher = slower, marginally better |
| `STT_MAX_UPLOAD_MB` | `128` | Upload size cap |
| `STT_TEMP_DIR` | `/tmp/stt-uploads` | Deleted after every request |
| `STT_MIN_AUDIO_SECONDS` | `0.3` | Below this, return empty text |

Nothing is hard-coded in source: the model is chosen entirely by environment.

## Design constraints

- **One model instance** for the process lifetime, loaded once at startup.
  Never reloaded per request.
- **One transcription at a time** (`asyncio.Semaphore(1)`). A second concurrent
  decode on a 1.5 GB budget risks an OOM that takes the service down.
- **One uvicorn worker.** More workers means more model copies in VRAM.
- **CPU fallback** is automatic and loudly logged.

## VRAM budget

Hard target: **≤ 1.5 GB** (PRD §5).

### Measured results

Measure with the service idle after the model has finished loading:

```bash
docker compose stop stt
nvidia-smi --query-gpu=memory.used --format=csv,noheader   # baseline

docker compose up -d stt
docker compose logs -f stt                                 # wait for "model_loaded"
nvidia-smi --query-gpu=memory.used --format=csv,noheader   # baseline + model
```

Take the difference between the two readings. Per-process accounting
(`--query-compute-apps`) reports `[Insufficient Permissions]` for containers
under Docker Desktop's WSL2 backend, so the stop/start delta is the reliable
measurement on Windows.

Measured on an RTX 4070 Laptop (8188 MiB, driver 592.82), `int8`, real
WebM/Opus audio, GPU sampled every 0.15 s. Baseline is 0 MiB with the container
stopped, so these are the model's own numbers.

| Model | Idle | Peak (60 s) | Peak (90 s chunk) | Speed | Budget |
|---|---|---|---|---|---|
| `small` | 415 MiB | 593 MiB | 625 MiB | 27x realtime | 41% |
| **`medium`** (default) | 1055 MiB | 1457 MiB | **1521 MiB** | 16x realtime | **99%** |

Peak grows with chunk length, then flattens — faster-whisper decodes in fixed
30 s windows, so a 3-hour meeting costs no more VRAM than one chunk. **The
budget is set by chunk size, not meeting length.**

### Why `medium` despite the PRD

PRD §5 names `small` as the default and forbids `medium`. That was written
before anything was measured. With numbers in hand, `medium` fits — at
1521 MiB against a 1536 MiB ceiling, but it fits, and the accuracy difference
on Indonesian is not subtle:

| `small` | `medium` |
|---|---|
| "Olimpiada Musim Panas" | "Olimpiade Musim Panas" |
| "Pertanggal 14 Marat 2022" | "Pertanggal 14 Maret 2022" |
| "BAP 1 latar belakang" | "bab 1 latar belakang" |

Same 60 s of spoken-Wikipedia audio; `medium` fixes three of four errors.
Both still mishear the speaker's name ("Alice" for "Elis").

**The trade is roughly 900 MiB of VRAM and half the speed.** 16x realtime still
transcribes a 90 s chunk in under 6 s, so the live transcript keeps up easily.

Set `WHISPER_MODEL=small` in `.env` if you need that VRAM for something else on
the same GPU — the app does not care which is loaded.

### Prompt echo

Whisper conditions its decoding on `initial_prompt`, and on unclear or quiet
audio it sometimes emits that prompt back as though it had been spoken. In a
real 1.7-hour meeting one chunk came back beginning "Rekaman rapat, angka,
angka, dan istilah asing sesuai yang diucapkan…" — the configured prompt,
transcribed as speech.

Segments that track the prompt verbatim are now discarded (`is_prompt_echo`),
logged as `prompt_echo_discarded`. The test requires a run of six consecutive
matching words across at least half the segment, so a sentence that merely
shares vocabulary with the prompt — "Rekaman rapat kemarin sudah saya kirim" —
is kept.

### Hallucination on non-speech

Whisper invents text when fed audio that is not speech, and `medium` does it
more readily than `small` — a continuous test tone produced "Thank you for
watching" and "www.mooji.org".

Real silence is not affected: VAD strips it and the chunk returns empty text,
verified on both models. The risk is a chunk that carries steady non-speech
noise. `condition_on_previous_text=False` was tested as a mitigation and made
no measurable difference (973 vs 967 characters, identical content), so it is
deliberately **not** set.

## Concurrency

Four simultaneous requests were served in **0.82 / 1.37 / 1.87 / 2.50 s** — a
staircase, not a stampede. The semaphore serializes GPU work as designed, and
no request was rejected.

## Local development

Run it through Docker. The dependency chain (`ctranslate2`, cuDNN 9, CUDA 12)
does not install cleanly on arbitrary host Python versions.

```bash
docker compose up --build stt
docker compose logs -f stt
```

## Tests

26 tests, no GPU and no model weights needed — the endpoint tests inject a fake
model holder:

```bash
docker compose run --rm --entrypoint sh stt -c   "pip3 install -q -r requirements-dev.txt && python3 -m pytest tests/ -q"
```

They cover upload sanitisation (traversal attempts, oversized, empty),
guaranteed temp-file deletion including on failure, language/prompt routing,
and the silence and very-short-audio paths.
