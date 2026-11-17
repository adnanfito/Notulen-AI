# Notulen AI

Self-hosted meeting recorder, transcriber, and note generator. Audio is captured
in the browser, transcribed locally with `faster-whisper`, and turned into
structured meeting notes by an LLM.

**Privacy:** audio never leaves this machine. Only the finished transcript text
is sent to the LLM provider for summarization.

> **Status: feature complete.** Record a meeting in the browser, watch the
> transcript appear live, and get structured notes when you stop. Summarization
> needs an `LLM_API_KEY`; everything else works without one.

---

## Architecture

```text
Vue 3 (browser)  ──HTTP/SSE──>  Go API  ──HTTP──>  Python STT (faster-whisper, CUDA)
                                   │
                                   ├──> PostgreSQL   (meetings, transcripts)
                                   └──> LLM provider (transcript text only)
```

The Go backend is the only service the browser talks to. PostgreSQL and the STT
service are never exposed publicly.

---

## Prerequisites

| Requirement | Notes |
|---|---|
| Docker Desktop / Docker Engine | Compose v2+ |
| NVIDIA GPU (optional) | For CUDA transcription. CPU fallback works without it. |
| NVIDIA driver + Container Toolkit | Required only for GPU mode |

### GPU on Windows

GPU passthrough requires Docker Desktop running on the **WSL2 backend** (not
Hyper-V). Verify the container runtime can see the GPU:

```bash
docker run --rm --gpus all nvidia/cuda:12.4.1-base-ubuntu22.04 nvidia-smi
```

If that fails, set `WHISPER_DEVICE=cpu` in `.env` and remove the `deploy` block
from the `stt` service in `docker-compose.yml`. Everything still works, just
slower.

---

## Quick start

```bash
cp .env.example .env      # then set LLM_API_KEY when you reach the summary phase
docker compose up --build
```

The first start downloads the Whisper weights (a few hundred MB for `small`),
so the STT health check stays red for a minute or two. Weights are cached in a
named volume and are not re-downloaded on later starts.

| Service | URL | Exposed |
|---|---|---|
| Frontend | http://localhost:5273 | loopback |
| Backend API | http://localhost:8180/api/v1/health | loopback |
| STT | http://localhost:8200/health | dev only |
| PostgreSQL | `localhost:55432` | dev only |

Every published port binds to `127.0.0.1` only, and all four are configurable
via `FRONTEND_PORT`, `BACKEND_PORT`, `STT_PORT`, and `POSTGRES_PORT` in `.env`.
The defaults deliberately avoid the usual 5173/8080/5432 so the stack coexists
with other projects on the same machine.

`docker compose up` picks up `docker-compose.override.yml`, which adds hot
reload and exposes postgres/stt on loopback for debugging. For the production
shape:

```bash
docker compose -f docker-compose.yml up --build -d
```

### Switching between dev and production

Dev and production build different images (`:dev` runs Vite and `air`;
`:prod` runs nginx and a compiled binary) and they are tagged separately on
purpose. **Pass `--build` when you switch**, or Compose will start the images
you already have:

```bash
docker compose up -d --build                       # dev
docker compose -f docker-compose.yml up -d --build # production
```

Without separate tags both targets would land on one tag and whichever was
built last would win — producing an nginx container published on Vite's port,
which fails to connect with nothing obviously wrong in `docker compose ps`.

---

## Configuration

All configuration lives in `.env` — see `.env.example` for the full list.
Never commit `.env`.

The Whisper model must stay within a **1.5 GB VRAM** budget. `small + int8` is
the default; drop to `base` if your GPU measures over budget.

```env
WHISPER_MODEL=small
WHISPER_DEVICE=cuda
WHISPER_COMPUTE_TYPE=int8
```

Measured VRAM usage is recorded in [stt/README.md](stt/README.md).

---

## Database

Migrations live in `migrations/` and are applied by a one-shot `migrate`
service before the backend starts. To run them by hand:

```bash
docker compose run --rm migrate -path /migrations -database "$DATABASE_URL" up
docker compose run --rm migrate -path /migrations -database "$DATABASE_URL" down 1
```

On **Git Bash for Windows**, prefix those with `MSYS_NO_PATHCONV=1` — otherwise
Git Bash rewrites the container path `/migrations` into a Windows path and the
command fails with `failed to open source`. Compose's own `migrate` service is
unaffected; this only bites manual invocations.

---

## Browser limitations

These are properties of the browser, not bugs in this app:

- **System audio** — `getDisplayMedia()` support varies by browser and OS. You
  must tick the "share audio" checkbox in the browser's picker; it is off by
  default and easy to miss.
- **Tab vs. screen** — for browser-based meetings (Google Meet, etc.), sharing
  the specific **tab** with audio is more reliable than sharing the whole
  screen. On some platforms full-screen sharing carries no audio at all.
- **Microphone + system audio** — the two streams are mixed with the Web Audio
  API. Use headphones; open speakers will feed system audio back into the
  microphone and double every voice.
- **Firefox / Safari** — system-audio capture is significantly more limited
  than in Chromium. Chromium-based browsers are recommended.

---

## Development

Backend and frontend both hot-reload inside Compose. To work outside Docker:

```bash
cd backend  && go build ./... && go vet ./...
cd frontend && npm install && npm run dev
```

The STT service is **Docker-only** — `faster-whisper`/`ctranslate2` wheels do
not build on every host Python version.

---

## Roadmap

| Phase | Scope | Status |
|---|---|---|
| 1 | Repo skeleton, schema, Compose, health checks | done |
| 2 | STT `POST /transcribe`: VAD, initial prompt, temp-file cleanup | done |
| 3 | Meeting CRUD, chunk upload, STT worker queue, SSE, stop endpoint | done |
| 4 | LLM summarizer, structured notes, JSON validation | done |
| 5 | Recording UI, live transcript, meeting detail, history/search | done |
| 6 | End-to-end integration and tests | done |

See [PRD.md](PRD.md) for the full specification.

---

## How it works

```text
Browser records ──► 90 s chunk ──► POST /meetings/:id/chunks ──► 202 immediately
                                            │
                                            ▼
                                   in-memory worker queue
                                            │
                                            ▼
                                  STT service (one GPU job)
                                            │
                                            ▼
                              transcript_chunks in PostgreSQL
                                            │
                                            ▼
                                   SSE ──► live transcript
```

Stopping combines the chunks in `chunk_index` order, sends the text to the LLM,
validates the JSON, and stores the notes. **A failed summary never touches the
transcript** — the meeting still completes and the transcript is still there.

### API

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/v1/meetings` | Create a meeting |
| GET | `/api/v1/meetings?q=&page=&limit=` | List and search |
| GET | `/api/v1/meetings/:id` | Meeting with transcript chunks |
| PATCH | `/api/v1/meetings/:id` | Edit title or notes |
| DELETE | `/api/v1/meetings/:id` | Delete |
| POST | `/api/v1/meetings/:id/chunks` | Upload audio chunk (returns 202) |
| POST | `/api/v1/meetings/:id/stop` | Finish and summarize |
| POST | `/api/v1/meetings/:id/summarize` | Regenerate notes from the stored transcript |
| POST | `/api/v1/meetings/combine` | Combine several meetings into one set of notes |
| GET | `/api/v1/meetings/:id/stream` | SSE live transcript |

Errors all use one envelope: `{"error": {"code": "...", "message": "..."}}`.

### Summarization

Set `LLM_API_KEY` in `.env` to enable it. Without a key the app still records,
transcribes, and stores everything — only the notes step is skipped, and the
meeting page says so.

```env
LLM_PROVIDER=gemini
LLM_MODEL=gemini-3.5-flash
LLM_FALLBACK_MODELS=gemini-2.5-flash
LLM_MAX_RETRIES=3
LLM_API_KEY=your-key
```

Only the transcript text is sent to the provider. **Audio never leaves this
machine.** Providers sit behind a `Summarizer` interface, so adding another one
does not touch the meeting handlers.

#### When the model is busy

Flash models return **503 "model overloaded"** under load, and **429** when you
hit a quota. Either would otherwise cost the meeting its notes, so:

1. **Retry the same model** up to `LLM_MAX_RETRIES` times, with exponential
   backoff and jitter. A `Retry-After` header from the server overrides the
   computed delay.
2. **Fall through to the next model** in `LLM_FALLBACK_MODELS` once those
   retries are spent — a different model usually has capacity when one is
   swamped.
3. **Give up cleanly.** The transcript is still stored and the meeting still
   completes; only the notes are missing, and the page says so (PRD §32).

Not everything is retried. A bad API key (403) or a malformed request (400)
fails identically on every model, so it fails immediately rather than burning
attempts. An unknown model id (404) skips straight to the next model without
retrying. Unusable model output *is* retried once — generation is not
deterministic, and a second roll is cheaper than losing the notes.

Retries and fallbacks appear in the logs as `summary_retry` and
`summary_model_fallback`.

#### How detailed the notes are

The prompt asks for coverage proportional to the meeting: every substantive
topic, several discussion points each, concrete numbers and names kept as
spoken, and a multi-paragraph executive summary for a long meeting. On a
1.7-hour meeting that roughly doubled the depth — 4 topics and 14 discussion
points became 7 and 28.

Depth is capped by `LLM_MAX_OUTPUT_TOKENS` (default 32768). Hitting it
truncates the JSON mid-structure, so that case is detected and reported by
name rather than retried into the same wall.

Asking a model for "more detail" is also the instruction that makes it pad, so
the accuracy rules are stated as outranking the depth rules, and the prompt
says outright that writing less beats writing something that was not said. In
testing this made the notes *more* conservative in one useful way: a
discussion-only meeting stopped producing an invented action item.

#### When a restart interrupts summarizing

Summarization runs in a goroutine, so restarting the backend abandons whatever
was in flight while the row stays at `PROCESSING`.

Two things make that recoverable:

- **On startup**, every meeting found at `PROCESSING` is picked up and
  summarized again. Nothing can own those rows — this process was not running
  when they were marked. Logged as `summary_recovery_started`.
- **"Already running" is tracked in memory**, not read from the database
  status. A restart clears it, so a stranded meeting stays retryable by hand.

Before this, such a meeting was a dead end: the retry endpoint read
`PROCESSING` as proof that work was under way and refused with 409, while
nothing was actually running. The transcript was intact the whole time and
there was no way to reach it.

#### Regenerating notes by hand

When every automatic attempt still fails, the meeting page offers a
**Buat notulen sekarang** button (and **Buat ulang** once notes exist), backed
by `POST /api/v1/meetings/:id/summarize`.

It only ever writes the notes. The transcript is never rewritten, and a failed
re-run puts the meeting back exactly as it was and says why — so pressing it is
always safe. One exception, deliberately: if the transcript column is *empty*
while the chunks are intact, the re-run backfills it. That repairs a meeting
whose finalize failed part-way.

---

## Exporting notes as PDF

The meeting page has a **PDF** button. It opens the browser's print dialog,
where "Save as PDF" produces the document; a **transkrip** checkbox next to it
decides whether the full transcript is appended on its own page.

No PDF library is bundled. The browser already renders the page with correct
Indonesian glyphs and selectable text, and `@media print` in `src/style.css`
turns it into a document — app chrome removed, collapsed topics expanded, body
copy darkened for paper. A JS PDF builder would add ~350 KB and hand back
manual text wrapping, page breaks, and font embedding for a worse result.

The suggested filename comes from the meeting title.

## What is kept on disk

Audio is treated as a throwaway processing artifact, not data:

| | Lifetime |
|---|---|
| Browser audio blob | Released once uploaded |
| Backend chunk file | **Deleted the moment that chunk finishes transcribing** — not at the end of the meeting |
| STT upload temp file | Deleted in a `finally`, even when transcription fails |
| Per-meeting directory | Removed when the meeting completes, fails, or is deleted |
| Leftovers from a crash | Swept at backend startup |

So a three-hour meeting never holds more than one chunk of audio on disk at a
time, and the meeting folder disappears with it. Only transcripts and notes
survive, in PostgreSQL.

Set `KEEP_AUDIO=true` to retain audio for debugging — it disables every step
above, including the startup sweep.

## Backups

**Nothing else in this system holds your meeting notes.** Audio is deleted
after transcription by design, so a lost database row is lost for good — there
is no second copy to fall back on.

```bash
./scripts/backup.sh                     # -> backups/notulen-<timestamp>.sql.gz
./scripts/restore.sh backups/<file>     # replaces the current database
```

`backup.sh` writes a gzipped `pg_dump` and prints the size and meeting count,
so an empty or failed dump is obvious rather than silent. `restore.sh` asks for
confirmation before overwriting (`-y` skips it for scripts).

Run a backup before upgrades and before any manual SQL. To make it automatic,
point cron at it:

```cron
0 2 * * *  cd /path/to/notulen-ai && ./scripts/backup.sh >> backups/backup.log 2>&1
```

`docker compose down` does **not** delete the database — the `postgres_data`
volume survives. `docker compose down -v` does, and so does any `DELETE FROM
meetings`; both are unrecoverable without a dump.

## Combining several meetings

One discussion often ends up split across recordings — the recording is stopped
by accident, or the browser is closed, and the rest becomes a new meeting.
Summarizing each fragment separately gives several partial sets of notes and no
account of the discussion as a whole.

On the dashboard, **Gabungkan** turns on selection, tick two or more meetings,
and confirm. The result is a new meeting whose transcript is the sources joined
in chronological order, summarized as one.

**The sources are never modified.** The combined meeting holds *copies* of their
chunks, with timings shifted so the transcript reads as one continuous
recording, so deleting a source later cannot hollow it out. The combined
meeting behaves like any other: edit, export to PDF, regenerate, delete.

Meetings still recording or processing cannot be combined — there is no settled
end to join to. `POST /api/v1/meetings/combine` takes `{meeting_ids, title?}`;
`source_meeting_ids` on the meeting records where it came from.

In testing, five fragments of one meeting produced 8 action items together
where the fragments had yielded 0 or 1 each.

## Pausing

**Jeda** stops recording without ending the meeting; **Lanjutkan** picks it up
again. The microphone stream stays open, so resuming is instant and does not
re-prompt for permission.

A pause ends the current chunk rather than using `MediaRecorder.pause()`. Each
chunk has to be a standalone WebM file and stopping is what gives it its own
header — and the flushed chunk transcribes while you are paused, so the pause
costs nothing.

Paused time is excluded from everything that matters: the timer freezes, chunk
timings stay continuous across the gap, and the meeting's stored duration
reflects recorded audio rather than the wall clock. Without that, every
timestamp after a pause would drift by the length of the pause.

## The recording URL

Starting a recording rewrites the address bar to `/meetings/{id}` and the
heading becomes the title you typed, so the page says which meeting it is.

This uses `history.replaceState`, not a router navigation: navigating would
unmount the view and take the live `MediaRecorder` with it. Only the label on
the current history entry changes.

**A reload still cannot resume recording.** The microphone stream dies with the
page — no browser can bring it back. Reloading lands on the meeting instead,
which offers to finish it.

## Browsing while recording

A recording keeps running when you navigate away, so you can look something up
mid-meeting. There is no confirmation dialog, because there is nothing to lose
by leaving.

The recorder and the event stream live in the Pinia store rather than in the
recording view. A component-owned recorder dies the moment Vue unmounts the
view — which is exactly why leaving used to need a warning.

While a recording is running, the header shows a **timer pill** that links back
to it, and `/meetings/{id}` renders the live recorder rather than the
finished-meeting page. Stopping works from either place.

If a meeting is ever left in `RECORDING` — a browser crash, or a tab closed
mid-meeting — its detail page offers **Selesaikan & buat notulen**, which runs
the same stop the recorder would have. The chunks were transcribed as they
arrived, so nothing is lost; only the final combine and summarize step was
missing.

**Closing or reloading the tab still ends the capture.** The microphone stream
dies with the page and no browser can bring it back, so that case falls back to
finishing the meeting from its detail page.

## Pausing

**Jeda** stops recording without ending the meeting; **Lanjutkan** picks it up
again. The microphone stream stays open, so resuming is instant and does not
re-prompt for permission.

A pause ends the current chunk rather than using `MediaRecorder.pause()`. Each
chunk has to be a standalone WebM file and stopping is what gives it its own
header — and the flushed chunk transcribes while you are paused, so the pause
costs nothing.

Paused time is excluded from everything that matters: the timer freezes, chunk
timings stay continuous across the gap, and the meeting's stored duration
reflects recorded audio rather than the wall clock. Without that, every
timestamp after a pause would drift by the length of the pause.

## The recording URL

Starting a recording rewrites the address bar to `/meetings/{id}` and the
heading becomes the title you typed, so the page says which meeting it is.

This uses `history.replaceState`, not a router navigation: navigating would
unmount the view and take the live `MediaRecorder` with it. Only the label on
the current history entry changes.

**A reload still cannot resume recording.** The microphone stream dies with the
page — no browser can bring it back. Reloading lands on the meeting instead,
which offers to finish it.

## Leaving mid-recording

Navigating away from the recording page asks first. Accepting stops the
recording and finishes the meeting; cancelling returns you to it. Closing or
reloading the tab triggers the browser's own warning.

If a meeting is ever left in `RECORDING` — an older build, or a browser crash —
its detail page offers **Selesaikan & buat notulen**, which runs the same stop
the recorder would have. The chunks were transcribed as they arrived, so
nothing is lost; only the final combine and summarize step was missing.

## Known limits

- **The transcription queue is in memory.** If the backend restarts mid-meeting,
  chunks not yet transcribed are lost. Transcripts already written to PostgreSQL
  survive. The PRD accepts this for V1; a database-backed queue is the upgrade
  path if it ever bites. Summarization, which runs the same way, *is* recovered
  at startup — the transcript it needs is already in the database.
- **One chunk transcribes at a time.** With one GPU that is the right shape, but
  a long backlog takes a while to drain after a long meeting.
- **Search uses `ILIKE`.** Fine for a personal archive; move to PostgreSQL
  full-text search if it ever gets slow.
- **Chunks are cut by restarting the recorder**, not with
  `MediaRecorder`'s `timeslice`. Timeslice puts the WebM header in the first
  blob only, leaving every later chunk undecodable — a meeting would transcribe
  its opening and silently lose the rest. The restart costs 0 ms of audio
  (measured; the MediaStream stays live), but it is why the recorder looks more
  involved than it otherwise would.

---

## Tests

```bash
cd backend  && go test ./...      # 26 tests
cd frontend && npm test           # 22 tests
docker compose run --rm --entrypoint sh stt -c   "pip3 install -q -r requirements-dev.txt && python3 -m pytest tests/ -q"  # 26 tests
```

Browser tests live in [e2e/](e2e/) with their own `package.json`, so Playwright
never ships into the dev container:

```bash
cd e2e && npm run setup
npm run smoke        # record → transcribe → stop → notes page
npm run multichunk   # regression: every chunk must decode
```

`multichunk` guards a bug that unit tests cannot reach — see
[e2e/README.md](e2e/README.md). Run it after touching `useAudioRecorder.ts`.
