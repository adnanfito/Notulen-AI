# PRD — Self-Hosted AI Meeting Notetaker

## 1. Project Overview

Build a **self-hosted personal web application for meeting recording, live transcription, and automatic meeting notes**.

The application is intended for a single user running it on their own machine/server. The system should prioritize:

- Privacy: meeting audio and transcripts stay on the user's infrastructure.
- Low GPU VRAM usage.
- Simple deployment and maintenance.
- Reliable long-running recordings.
- Indonesian + English + mixed Indo-English transcription.
- Useful, editable meeting notes rather than a generic chatbot UI.

### Primary workflow

1. User opens the web application.
2. User creates a new meeting.
3. User selects the audio source:
   - Microphone
   - System audio
   - Microphone + system audio
4. Browser records the meeting.
5. Audio is split into small chunks.
6. Chunks are uploaded asynchronously to the backend.
7. Local STT service transcribes each chunk using `faster-whisper`.
8. Transcripts appear progressively in the browser.
9. When recording stops, the backend combines all transcript chunks.
10. An LLM generates structured meeting notes.
11. User can review, edit, and save the notes.
12. Meeting history remains available for search and later review.

---

# 2. Product Goals

## Goals

- Provide a private personal meeting-notes system.
- Support meetings lasting at least 1–3 hours.
- Display transcript progressively while recording.
- Generate structured notes automatically after the meeting.
- Keep the architecture simple enough for one person to self-host.
- Keep STT GPU memory usage at **1.5 GB VRAM or less**.
- Make the system usable on a local network.
- Allow manual editing of generated notes.
- Avoid unnecessary infrastructure such as Redis/Kafka unless clearly required.

## Non-goals

The first version does NOT need:

- Multi-user authentication.
- Team/workspace management.
- Complex permissions.
- Calendar integrations.
- Zoom/Google Meet API integrations.
- Speaker diarization.
- Cloud audio storage.
- Mobile native applications.
- Real-time LLM summarization while the meeting is happening.
- A full enterprise collaboration platform.

---

# 3. Recommended Architecture

```text
┌─────────────────────────────────────────────┐
│                Vue 3 Web App                │
│                                             │
│  Recorder → Audio Chunking → Upload        │
│                  ↓                          │
│             SSE Transcript                 │
└────────────────────┬────────────────────────┘
                     │ HTTP / SSE
                     ▼
┌─────────────────────────────────────────────┐
│              Go API Backend                 │
│                                             │
│  Meeting API                                │
│  Chunk Upload                               │
│  Background STT Dispatch                    │
│  Transcript Storage                         │
│  SSE Event Broker                           │
│  Summary Generation                         │
└───────────────┬──────────────┬──────────────┘
                │              │
                │ HTTP         │ LLM API
                ▼              ▼
┌──────────────────────┐   ┌─────────────────┐
│ Python STT Service   │   │ LLM Provider    │
│                      │   │                 │
│ faster-whisper       │   │ OpenAI / Gemini │
│ small/int8           │   │ / compatible    │
│ CUDA                 │   │ API             │
└──────────────────────┘   └─────────────────┘
                │
                ▼
        Local temporary audio
                │
                ▼
          PostgreSQL
```

## Architecture principles

1. PostgreSQL is the source of truth for meetings and transcripts.
2. Audio chunks are temporary processing artifacts.
3. STT runs locally.
4. LLM summarization may use an external API, but only the transcript is sent by default.
5. No audio should be sent to a cloud provider.
6. Background jobs must not block HTTP requests.
7. The frontend must never depend on the STT service directly.
8. The Go backend is the single API gateway for the frontend.

---

# 4. Technology Stack

## Frontend

- Vue 3
- Vite
- Composition API
- `<script setup>`
- Pinia
- Vue Router
- Tailwind CSS
- Lucide Vue
- Native browser APIs:
  - MediaRecorder
  - MediaDevices
  - getUserMedia
  - getDisplayMedia
  - Web Audio API
  - EventSource / SSE

Use JavaScript or TypeScript consistently. **TypeScript is preferred.**

## Backend

- Go 1.22+
- Gin
- PostgreSQL
- pgx
- `google.golang.org/genai` or another provider abstraction
- SSE
- Standard Go context/timeouts
- Background worker implemented inside the Go service for V1

## STT

- Python 3.10+
- FastAPI
- faster-whisper
- CTranslate2
- CUDA when available
- CPU fallback
- Optional Silero VAD

## Database

- PostgreSQL 16+
- UUID primary keys
- JSONB for generated structured notes

## Deployment

Prefer Docker Compose:

```text
docker-compose.yml
├── frontend
├── backend
├── stt
└── postgres
```

GPU access should be enabled only for the STT container.

---

# 5. Critical GPU / STT Requirement

## Hard requirement

The STT service must be designed around a maximum target of:

**≤ 1.5 GB VRAM**

Do NOT use Whisper `medium` as the default.

The default STT model should be:

```text
faster-whisper small
compute_type=int8
device=cuda
```

If benchmarking on the target hardware shows that `small` exceeds the VRAM budget, provide:

```text
faster-whisper base
compute_type=int8
device=cuda
```

as the fallback.

The exact VRAM usage must be measured during implementation rather than assumed.

### Configuration

Make the model configurable using environment variables:

```env
WHISPER_MODEL=small
WHISPER_DEVICE=cuda
WHISPER_COMPUTE_TYPE=int8
WHISPER_LANGUAGE=auto
```

The application must not hard-code the model in source code.

## STT optimization rules

- Load exactly one Whisper model instance.
- Do not reload the model for every request.
- Limit concurrent transcription jobs.
- Prefer sequential processing on a single-GPU setup.
- Release temporary audio files after processing.
- Avoid unnecessarily large audio buffers.
- Do not load multiple Whisper models simultaneously.
- Provide CPU fallback for machines without CUDA.
- Add startup logging showing:
  - model
  - device
  - compute type

## Accuracy priority

For Indonesian meetings, prioritize:

1. `small + int8`
2. `base + int8` if VRAM/latency is constrained

Do not increase model size beyond the 1.5 GB VRAM budget.

---

# 6. Audio Recording Design

## Recording modes

The UI must provide:

### MIC_ONLY

Record microphone input.

### SYSTEM_ONLY

Record system audio using:

```javascript
navigator.mediaDevices.getDisplayMedia({
  video: true,
  audio: true
})
```

The video track should be discarded after obtaining the stream.

Important browser limitation:

System-audio capture depends on the browser and operating system. The UI must clearly explain that the user may need to select a tab/window/screen and enable audio sharing.

### FULL_MEETING

Capture:

```text
Microphone
+
System Audio
```

Use Web Audio API:

```text
MediaStream
   │
   ├── Mic MediaStreamSource
   │
   └── System Audio MediaStreamSource
            │
            ▼
      AudioContext
            │
      Gain / Mixing
            │
            ▼
   MediaStreamDestination
            │
            ▼
       MediaRecorder
```

The implementation should avoid echo and clipping as much as reasonably possible.

---

# 7. Audio Format

Prefer a browser-compatible format such as:

```text
audio/webm;codecs=opus
```

The frontend should detect supported MIME types using:

```javascript
MediaRecorder.isTypeSupported(...)
```

Fallbacks should be implemented.

The backend/STT service must accept WebM/Opus.

If conversion to WAV is required, perform it server-side or in the STT service.

Do NOT force WAV generation in the browser unless necessary.

---

# 8. Chunking Strategy

Do not rely exclusively on a single enormous recording.

Use:

```text
timeslice ≈ 60–120 seconds
```

as the preferred default.

Make it configurable:

```env
AUDIO_CHUNK_SECONDS=90
```

Why:

- Faster transcript feedback.
- Smaller upload failures.
- Lower memory usage.
- Better recovery if a request fails.
- Less waiting at the end of a meeting.

The backend must preserve:

```text
meeting_id
chunk_index
start_time_sec
end_time_sec
```

The frontend should maintain the local recording timer.

---

# 9. Chunk Processing Pipeline

```text
Browser
  │
  │ POST /api/v1/meetings/:id/chunks
  ▼
Go API
  │
  ├── Validate meeting
  ├── Validate chunk index
  ├── Save temporary audio
  ├── Create processing job
  │
  ▼
STT Worker
  │
  ├── faster-whisper
  ├── VAD
  └── transcription
  │
  ▼
Go API
  │
  ├── Save transcript_chunks
  └── Broadcast SSE event
  │
  ▼
Browser
  │
  └── Append transcript to live UI
```

STT processing must be asynchronous.

The upload endpoint must return quickly and must not wait for transcription.

---

# 10. Handling Chunk Ordering

STT processing can finish out of order.

The UI must therefore sort transcript chunks by:

```text
chunk_index
```

The backend must also return transcript chunks ordered by:

```sql
ORDER BY chunk_index ASC
```

Never rely on request completion order.

---

# 11. STT API

## Endpoint

```http
POST /transcribe
Content-Type: multipart/form-data
```

Form field:

```text
file
```

Optional:

```text
language
initial_prompt
```

## Response

```json
{
  "text": "hasil transkripsi...",
  "language": "id",
  "duration_seconds": 87.4
}
```

## STT behavior

Use:

```python
WhisperModel(
    model_size_or_path=MODEL,
    device=DEVICE,
    compute_type=COMPUTE_TYPE
)
```

Use VAD where appropriate.

Recommended initial prompt:

```text
Rapat teknis software engineering. Bahasa Indonesia, English, dan campuran Indo-English. Pertahankan istilah teknis seperti backend, frontend, API, deployment, database, pipeline, Docker, Kubernetes, GitHub, dan cloud.
```

Do not assume the meeting is always technical. Make the prompt configurable.

---

# 12. STT Failure Handling

If transcription fails:

1. Mark the chunk as failed.
2. Store an error message.
3. Retry a limited number of times.
4. Do not lose the meeting.
5. Continue processing subsequent chunks.
6. Show an error state in the UI.

Recommended:

```env
STT_MAX_RETRIES=2
```

---

# 13. Database Schema

Use migrations.

## meetings

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE meetings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    title VARCHAR(255) NOT NULL DEFAULT 'Untitled Meeting',

    status VARCHAR(50) NOT NULL DEFAULT 'RECORDING'
        CHECK (status IN (
            'RECORDING',
            'PROCESSING',
            'COMPLETED',
            'FAILED'
        )),

    language_used VARCHAR(50),

    duration_seconds INT NOT NULL DEFAULT 0,

    raw_transcript TEXT,

    summary_data JSONB,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

## transcript_chunks

```sql
CREATE TABLE transcript_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    meeting_id UUID NOT NULL
        REFERENCES meetings(id)
        ON DELETE CASCADE,

    chunk_index INT NOT NULL,

    start_time_sec INT NOT NULL,

    end_time_sec INT NOT NULL,

    transcript_text TEXT NOT NULL DEFAULT '',

    status VARCHAR(50) NOT NULL DEFAULT 'PROCESSING'
        CHECK (status IN (
            'PROCESSING',
            'COMPLETED',
            'FAILED'
        )),

    error_message TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(meeting_id, chunk_index)
);

CREATE INDEX idx_transcript_chunks_meeting
ON transcript_chunks(meeting_id, chunk_index);

CREATE INDEX idx_meetings_created_at
ON meetings(created_at DESC);
```

---

# 14. Optional Processing Jobs Table

If background processing becomes unreliable with an in-memory Go queue, introduce:

```sql
CREATE TABLE processing_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    meeting_id UUID NOT NULL
        REFERENCES meetings(id)
        ON DELETE CASCADE,

    chunk_id UUID
        REFERENCES transcript_chunks(id)
        ON DELETE CASCADE,

    job_type VARCHAR(50) NOT NULL,

    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',

    attempts INT NOT NULL DEFAULT 0,

    error_message TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

For V1, an in-memory worker queue is acceptable if the system documents that jobs are lost after a backend restart.

A database-backed queue is preferred for production reliability.

---

# 15. REST API

## Create meeting

```http
POST /api/v1/meetings
```

Response:

```json
{
  "meeting_id": "uuid",
  "title": "Untitled Meeting",
  "status": "RECORDING"
}
```

---

## Upload audio chunk

```http
POST /api/v1/meetings/:id/chunks
Content-Type: multipart/form-data
```

Fields:

```text
chunk_index
start_time_sec
end_time_sec
audio_file
```

Response:

```json
{
  "chunk_id": "uuid",
  "status": "PROCESSING"
}
```

---

## Live transcript

```http
GET /api/v1/meetings/:id/stream
Accept: text/event-stream
```

Example event:

```text
event: transcript
data: {"chunk_index":0,"text":"Halo semuanya..."}
```

Other events:

```text
event: chunk_processing
event: transcript
event: meeting_status
event: error
```

Heartbeat:

```text
: heartbeat
```

Send periodically to keep connections alive.

---

## Stop meeting

```http
POST /api/v1/meetings/:id/stop
```

Behavior:

1. Mark meeting as `PROCESSING`.
2. Wait for outstanding STT jobs to finish, with a configurable timeout.
3. Combine transcript chunks.
4. Generate meeting notes.
5. Store result.
6. Mark meeting as `COMPLETED`.

If some chunks failed, the system should still generate notes from available transcript while clearly indicating incomplete transcription.

Response:

```json
{
  "meeting_id": "uuid",
  "status": "PROCESSING"
}
```

The frontend should listen for completion via polling or SSE.

---

## List meetings

```http
GET /api/v1/meetings
```

Query parameters:

```text
q
page
limit
```

Example:

```http
GET /api/v1/meetings?q=deployment&page=1&limit=20
```

Return:

```json
{
  "items": [],
  "page": 1,
  "limit": 20,
  "total": 42
}
```

---

## Get meeting

```http
GET /api/v1/meetings/:id
```

Return:

```json
{
  "id": "uuid",
  "title": "Weekly Engineering Meeting",
  "status": "COMPLETED",
  "duration_seconds": 3600,
  "summary_data": {},
  "transcript_chunks": []
}
```

---

## Update meeting

```http
PATCH /api/v1/meetings/:id
```

Allowed fields:

```json
{
  "title": "New title",
  "summary_data": {}
}
```

---

## Delete meeting

```http
DELETE /api/v1/meetings/:id
```

Return:

```http
204 No Content
```

---

# 16. Summary / Notes Schema

Generated notes must use:

```json
{
  "title": "string",
  "language_used": "id",
  "executive_summary": "string",
  "key_decisions": [
    "string"
  ],
  "main_topics": [
    {
      "topic_name": "string",
      "discussion_points": [
        "string"
      ]
    }
  ],
  "action_items": [
    {
      "task": "string",
      "assignee": "string or null",
      "deadline": "string or null",
      "completed": false
    }
  ],
  "tags": [
    "string"
  ]
}
```

The `completed` property is local application state and may be changed manually by the user.

---

# 17. LLM Provider Abstraction

Do not hard-code the application architecture around one LLM provider.

Create an interface such as:

```go
type Summarizer interface {
    Summarize(ctx context.Context, transcript string) (*MeetingSummary, error)
}
```

V1 can implement one provider.

Configuration:

```env
LLM_PROVIDER=gemini
LLM_MODEL=<configured-model>
LLM_API_KEY=
```

The implementation should allow another provider to be added later without changing meeting handlers.

---

# 18. Summarization Rules

The LLM prompt must enforce:

### Language

- English transcript → English notes.
- Indonesian transcript → Indonesian notes.
- Mixed Indonesian-English → primarily Indonesian while retaining technical English terms.

### Accuracy

Only use information explicitly present in the transcript.

Never invent:

- attendees
- decisions
- deadlines
- names
- responsibilities
- numbers
- commitments

If an assignee or deadline is not stated:

```json
null
```

### Title

Generate a concise title.

Target:

```text
6–10 words
```

### Action items

Only create action items when the transcript contains an actual task, commitment, or follow-up.

### Decisions

Only include explicit or strongly stated decisions.

Do not convert casual discussion into a decision.

---

# 19. Master Summarization Prompt

```text
ROLE

You are an expert AI Meeting Secretary.

Your task is to transform a raw meeting transcript into accurate, concise,
structured meeting notes.

The transcript may contain:
- Bahasa Indonesia
- English
- Mixed Bahasa Indonesia and English
- Technical terminology
- Speech recognition errors

RULES

1. Use ONLY information contained in the transcript.
2. Do not hallucinate missing facts.
3. Never invent people, deadlines, decisions, or responsibilities.
4. If an assignee is not explicitly identifiable, use null.
5. If a deadline is not explicitly mentioned, use null.
6. If there are no clear action items, return an empty array.
7. If there are no clear decisions, return an empty array.
8. Preserve important technical terms.
9. For mixed Indonesian-English transcripts, write the notes primarily
   in Bahasa Indonesia while keeping technical English terms naturally.
10. Generate a concise and specific title.
11. Return ONLY valid JSON.
12. Do not wrap the JSON in Markdown code fences.

OUTPUT SCHEMA

{
  "title": "string",
  "language_used": "string",
  "executive_summary": "string",
  "key_decisions": ["string"],
  "main_topics": [
    {
      "topic_name": "string",
      "discussion_points": ["string"]
    }
  ],
  "action_items": [
    {
      "task": "string",
      "assignee": "string or null",
      "deadline": "string or null",
      "completed": false
    }
  ],
  "tags": ["string"]
}

TRANSCRIPT

[TRANSCRIPT_START]

{{TRANSCRIPT_TEXT}}

[TRANSCRIPT_END]
```

---

# 20. Frontend Pages

## 20.1 Dashboard

Route:

```text
/
```

Display:

- Recent meetings
- Search
- Create meeting button
- Meeting status
- Duration
- Date
- Generated title

Example:

```text
AI Meeting Notes

[ + New Meeting ]

Search meetings...

┌───────────────────────────────────┐
│ Weekly Engineering Sync           │
│ Sep 11, 2026 · 58 min             │
│ COMPLETED                         │
└───────────────────────────────────┘
```

---

# 21. Recording Page

Route:

```text
/meetings/new
```

UI:

```text
New Meeting

Audio Source

( ) Microphone
( ) System Audio
( ) Microphone + System Audio

[ Start Recording ]

00:00:00

Live Transcript
────────────────────────────

[ transcript appears here ]

[ Stop Recording ]
```

During recording show:

- Recording indicator.
- Timer.
- Current audio source.
- Upload/transcription status.
- Live transcript.
- Number of processed chunks.
- Number of failed chunks.

---

# 22. Meeting Detail Page

Route:

```text
/meetings/:id
```

Sections:

### Header

- Editable title
- Date
- Duration
- Status

### Executive Summary

Readable paragraph/card.

### Key Decisions

Bullet list.

### Main Topics

Expandable sections.

### Action Items

Table/list:

```text
☐ Task
  Assignee
  Deadline
```

Allow checking/unchecking completed state.

### Transcript

Show complete transcript grouped by chunk.

Allow copying transcript.

---

# 23. History / Search

Search should support:

- Meeting title
- Raw transcript
- Generated summary

For PostgreSQL V1, use simple `ILIKE`.

Example:

```sql
WHERE
    title ILIKE '%' || $1 || '%'
    OR raw_transcript ILIKE '%' || $1 || '%'
```

If the database becomes large, migrate to PostgreSQL full-text search.

---

# 24. SSE Design

Create an SSE manager in Go.

Conceptually:

```text
meeting ID
    ↓
set of connected clients
    ↓
broadcast(event)
```

Events:

```text
transcript
meeting_status
error
```

Each event should contain JSON.

Example:

```text
event: transcript
data: {
  "chunk_index": 4,
  "text": "Kita akan deploy besok..."
}
```

The client must reconnect automatically if the connection drops.

Do not duplicate transcript chunks after reconnect.

Use `chunk_index` as an idempotency key.

---

# 25. State Management

Use Pinia stores:

```text
meetingStore
recordingStore
```

Responsibilities:

### meetingStore

- meetings
- currentMeeting
- search
- fetchMeeting
- createMeeting
- updateMeeting
- deleteMeeting

### recordingStore

- recording state
- elapsed time
- selected source
- MediaRecorder
- current chunk index
- upload state
- live transcript
- SSE connection

Keep browser media APIs inside a dedicated composable:

```text
useAudioRecorder.ts
```

Do not put MediaRecorder implementation directly inside Vue components.

---

# 26. Suggested Frontend Structure

```text
frontend/
├── src/
│   ├── components/
│   │   ├── MeetingCard.vue
│   │   ├── AudioSourceSelector.vue
│   │   ├── RecordingControls.vue
│   │   ├── LiveTranscript.vue
│   │   ├── MeetingSummary.vue
│   │   ├── ActionItems.vue
│   │   └── TranscriptViewer.vue
│   │
│   ├── composables/
│   │   ├── useAudioRecorder.ts
│   │   └── useSSE.ts
│   │
│   ├── stores/
│   │   ├── meeting.ts
│   │   └── recording.ts
│   │
│   ├── views/
│   │   ├── DashboardView.vue
│   │   ├── RecordingView.vue
│   │   └── MeetingDetailView.vue
│   │
│   ├── router/
│   ├── services/
│   │   └── api.ts
│   │
│   └── App.vue
│
├── package.json
└── vite.config.ts
```

---

# 27. Suggested Backend Structure

```text
backend/
├── cmd/
│   └── server/
│       └── main.go
│
├── internal/
│   ├── config/
│   ├── database/
│   ├── meeting/
│   │   ├── handler.go
│   │   ├── service.go
│   │   ├── repository.go
│   │   └── model.go
│   │
│   ├── transcript/
│   ├── stt/
│   ├── summarizer/
│   ├── sse/
│   └── worker/
│
├── migrations/
│
├── go.mod
└── go.sum
```

---

# 28. Suggested STT Structure

```text
stt/
├── app/
│   ├── main.py
│   ├── config.py
│   ├── model.py
│   └── transcription.py
│
├── requirements.txt
├── Dockerfile
└── README.md
```

---

# 29. Configuration

Example backend `.env`:

```env
APP_ENV=development
PORT=8080

DATABASE_URL=postgres://postgres:postgres@postgres:5432/meeting_notes

STT_URL=http://stt:8000
STT_MAX_RETRIES=2
STT_TIMEOUT_SECONDS=600

AUDIO_CHUNK_SECONDS=90
AUDIO_TEMP_DIR=/tmp/meeting-chunks

LLM_PROVIDER=gemini
LLM_MODEL=
LLM_API_KEY=

SSE_HEARTBEAT_SECONDS=15
```

Never commit secrets.

Provide:

```text
.env.example
```

---

# 30. Docker GPU Configuration

The STT container must be able to access NVIDIA CUDA.

Provide documentation for:

- NVIDIA driver
- NVIDIA Container Toolkit
- Docker GPU runtime
- CUDA-compatible image

The application should still start without GPU for development.

Example conceptual configuration:

```yaml
services:
  stt:
    build: ./stt
    environment:
      WHISPER_MODEL: small
      WHISPER_DEVICE: cuda
      WHISPER_COMPUTE_TYPE: int8
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
```

Verify the actual Docker Compose syntax against the chosen Compose version during implementation.

---

# 31. Security / Privacy

Because this is self-hosted:

- Bind services appropriately.
- Do not expose PostgreSQL publicly.
- Do not expose the STT service publicly.
- Only expose the frontend and backend ports.
- Store API keys only in environment variables.
- Validate uploaded files.
- Limit maximum upload size.
- Sanitize filenames.
- Never execute uploaded files.
- Delete temporary audio after transcription.
- Do not log raw meeting transcripts by default.
- Do not log API keys.
- Add configurable retention for temporary files.

Optional future feature:

```env
KEEP_AUDIO=false
```

Default:

```text
false
```

---

# 32. Reliability Requirements

The system must survive:

- Browser tab temporarily losing network.
- SSE disconnect/reconnect.
- STT processing delays.
- One failed audio chunk.
- Backend restart.
- LLM API timeout.
- Empty transcript.
- Very short meetings.
- Long meetings.

Meeting state transitions:

```text
RECORDING
    ↓
PROCESSING
    ↓
COMPLETED
```

Failure:

```text
RECORDING
    ↓
FAILED
```

or:

```text
PROCESSING
    ↓
FAILED
```

A failed summary should not destroy already stored transcripts.

---

# 33. Empty / Low Quality Transcript Handling

If the transcript is empty or too short:

Do not send meaningless content to the LLM.

Instead show:

```text
Not enough transcript data to generate meeting notes.
```

The user should still be able to view the available transcript.

---

# 34. UI/UX Principles

The design should be:

- Clean
- Minimal
- Desktop-first
- Dark-mode friendly
- Fast
- Not overly animated
- Focused on the meeting content

Avoid:

- Excessive gradients
- Huge hero sections
- Fake AI animations
- Unnecessary dashboards
- Too many cards
- Chatbot-style UI

The main product is the **meeting transcript and notes**.

---

# 35. Accessibility

Implement:

- Keyboard navigation.
- Visible focus states.
- Proper button labels.
- Semantic headings.
- ARIA labels where necessary.
- Sufficient contrast.
- Avoid color-only status indicators.

---

# 36. API Error Format

Use a consistent format:

```json
{
  "error": {
    "code": "MEETING_NOT_FOUND",
    "message": "Meeting not found"
  }
}
```

Example error codes:

```text
MEETING_NOT_FOUND
INVALID_AUDIO
CHUNK_ALREADY_EXISTS
STT_UNAVAILABLE
STT_FAILED
SUMMARY_FAILED
INVALID_REQUEST
INTERNAL_ERROR
```

---

# 37. Logging

Use structured logs.

Example:

```text
INFO meeting_created
INFO chunk_uploaded
INFO transcription_started
INFO transcription_completed
WARN transcription_retry
INFO summary_started
INFO summary_completed
ERROR summary_failed
```

Never log full audio contents.

Avoid logging complete transcripts in production.

---

# 38. Testing Requirements

## Frontend

Test:

- Audio source selection.
- MediaRecorder support.
- Chunk creation.
- Upload retry.
- SSE reconnect.
- Transcript ordering.
- Meeting state transitions.

## Backend

Test:

- Meeting CRUD.
- Chunk upload.
- Duplicate chunk handling.
- STT dispatch.
- SSE broadcasting.
- Transcript ordering.
- Summary generation.
- Error handling.

## STT

Test:

- Valid WebM input.
- Invalid audio.
- Indonesian speech.
- English speech.
- Mixed speech.
- Empty/very short audio.

---

# 39. Performance Requirements

Target:

- Start recording within 1–2 seconds after permission is granted.
- Chunk upload should not block recording.
- Live transcript should appear as soon as the corresponding STT job finishes.
- UI must remain responsive during long meetings.
- Backend must not hold complete audio recordings in RAM.
- STT should process one chunk at a time on a single-GPU setup.

The system should support:

```text
1–3 hour meetings
```

without browser memory continuously growing due to retained audio blobs.

---

# 40. Important Browser Limitations

Document these clearly in the UI/README:

### System audio

`getDisplayMedia()` support differs between browsers and operating systems.

The user may need to explicitly enable audio sharing.

### Tab capture

For Google Meet or browser-based meetings, sharing the browser tab with audio may be more reliable than sharing the entire desktop, depending on browser/OS.

### Microphone + system audio

Some browsers may behave differently when combining streams.

The implementation must gracefully handle unsupported scenarios.

---

# 41. MVP Acceptance Criteria

The MVP is complete when all of the following work:

### Recording

- [ ] User can create a meeting.
- [ ] User can record microphone.
- [ ] User can record system audio where browser supports it.
- [ ] User can mix microphone + system audio.
- [ ] Recording uses configurable chunking.
- [ ] Browser remains responsive during long recording.

### STT

- [ ] Audio chunks reach Go backend.
- [ ] Go backend sends chunks to Python.
- [ ] Python uses faster-whisper.
- [ ] Default model is `small`.
- [ ] Default compute type is `int8`.
- [ ] GPU memory target is ≤1.5 GB.
- [ ] CPU fallback exists.
- [ ] Transcript is stored in PostgreSQL.

### Live transcript

- [ ] SSE works.
- [ ] Transcript appears progressively.
- [ ] Chunks appear in correct order.
- [ ] SSE reconnects after temporary disconnect.

### Summary

- [ ] Stop recording triggers processing.
- [ ] All completed transcript chunks are combined.
- [ ] LLM generates structured JSON.
- [ ] JSON is validated before storing.
- [ ] Notes appear on the detail page.

### History

- [ ] Meetings are listed.
- [ ] Search works.
- [ ] Meeting detail works.
- [ ] Title can be edited.
- [ ] Summary can be edited.
- [ ] Action items can be marked completed.
- [ ] Meeting can be deleted.

---

# 42. Recommended Implementation Order

The coding agent MUST implement incrementally.

## Phase 1 — Repository Setup

Create:

```text
frontend/
backend/
stt/
migrations/
docker-compose.yml
.env.example
README.md
```

Verify all services can start.

---

## Phase 2 — PostgreSQL

Implement migrations.

Verify:

- meetings table
- transcript_chunks table
- indexes
- foreign keys
- constraints

---

## Phase 3 — STT Service

Implement:

```text
POST /transcribe
```

First make it work locally.

Then add CUDA.

Then benchmark VRAM.

Document the measured result.

Do not proceed with `medium` if it violates the 1.5 GB target.

---

## Phase 4 — Go Backend

Implement:

1. Database connection.
2. Meeting CRUD.
3. Chunk upload.
4. Background STT queue.
5. Transcript persistence.
6. SSE.
7. Stop meeting.
8. Summary provider.

---

## Phase 5 — Frontend

Implement:

1. Dashboard.
2. New meeting.
3. Audio source selector.
4. Recording.
5. Chunk upload.
6. SSE transcript.
7. Meeting detail.
8. History/search.
9. Editing.

---

## Phase 6 — Integration

Test the complete flow:

```text
Create meeting
↓
Record
↓
Upload chunk
↓
STT
↓
SSE
↓
Stop
↓
Summarize
↓
Save
↓
Display notes
```

---

# 43. Definition of Done

The implementation is considered complete only when:

1. `docker compose up` starts the application.
2. PostgreSQL initializes correctly.
3. Backend health check works.
4. STT health check works.
5. Frontend can communicate with backend.
6. Microphone recording works.
7. System audio recording works where supported.
8. Combined recording works where supported.
9. Audio chunks are uploaded asynchronously.
10. STT uses `small + int8` by default.
11. STT VRAM usage is measured and documented.
12. VRAM target is ≤1.5 GB on the target GPU.
13. Live transcript works through SSE.
14. Meeting stop triggers summary generation.
15. Structured notes are stored as JSONB.
16. Notes can be edited.
17. Meeting history works.
18. Search works.
19. Delete works.
20. Temporary audio files are cleaned up.
21. Errors are handled gracefully.
22. README contains complete setup instructions.

---

# 44. AI Coding Agent Instructions

You are a Principal Full-Stack Engineer implementing this PRD.

Follow these rules:

1. Read the entire PRD before coding.
2. Do not implement everything in one giant step.
3. Start with repository structure and migrations.
4. Keep commits/changes modular.
5. After each major phase, run tests/build checks.
6. Do not invent APIs that are not required.
7. Prefer simple architecture over unnecessary infrastructure.
8. Do not add Redis, Kafka, RabbitMQ, Kubernetes, or microservices beyond the defined STT service unless there is a demonstrated requirement.
9. Keep STT local.
10. Do not use Whisper `medium`.
11. Enforce the 1.5 GB VRAM target.
12. Make STT model configuration environment-driven.
13. Never store API keys in source code.
14. Validate all LLM JSON before persisting it.
15. Never allow an LLM failure to delete or corrupt transcript data.
16. Write tests for important business logic.
17. Handle browser audio limitations gracefully.
18. Keep temporary audio files outside the permanent database.
19. Use clear TypeScript types on the frontend.
20. Use clear DTOs/models on the backend.
21. Do not over-engineer the MVP.

Before changing architecture, explain why the existing PRD architecture is insufficient.

---

# 45. First Task for the Coding Agent

Do NOT start by implementing the entire application.

First:

1. Inspect the repository.
2. Create the project structure.
3. Create the PostgreSQL migration files.
4. Create `.env.example`.
5. Create Docker Compose.
6. Create minimal health endpoints for backend and STT.
7. Create a minimal Vue application.
8. Verify all containers/builds.
9. Report the current project structure.
10. Stop and wait for the next implementation instruction.

The first milestone is:

```text
Repository skeleton
+
Database schema
+
Docker Compose
+
Backend health check
+
STT health check
+
Frontend booting
```

Only after that should implementation continue to audio recording and STT.
