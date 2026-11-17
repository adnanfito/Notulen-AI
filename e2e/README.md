# Browser tests

Scripts that drive the real app in a real browser. Playwright lives here rather
than in `frontend/` so it never ships into the dev container.

```bash
npm run setup          # once
npm run smoke          # record → transcribe → stop → notes page
npm run multichunk     # regression: every chunk must decode
npm run probe-chunks   # are chunks standalone WebM files?
npm run probe-gap      # audio lost between chunk boundaries
npm run pdf -- <id>    # render a meeting's PDF export and eyeball it
npm run browse         # recording survives navigation; no confirmation dialog
npm run stuck -- <id>  # a meeting left at PROCESSING must still be retryable
npm run pause          # pause/resume, and that paused time is not counted
npm run combine -- "<title>"  # combine matching meetings; sources must survive
npm run url            # the address bar carries the meeting id while recording
npm run finish -- <id> # finish a meeting left in RECORDING
```

Note `waitUntil: 'domcontentloaded'`, not `'networkidle'`: a meeting that is
RECORDING or PROCESSING holds an SSE stream open, so the network never goes
idle and `networkidle` times out.

The stack must already be running. Chromium's fake media device supplies the
audio, so recording is exercised for real — no microphone needed.

## Why `multichunk` exists

`MediaRecorder.start(timeslice)` looks like the natural way to cut a meeting
into chunks, but it writes the WebM/EBML header into the **first blob only**.
Every later blob is a bare cluster fragment that no decoder can open:

```text
chunk 0:  1a 45 df a3  <- EBML header, decodes
chunk 1:  43 c3 81 03  <- fragment, "Audio could not be decoded"
chunk 2:  43 c3 81 03  <- fragment
```

So a meeting transcribed its first 90 seconds and silently lost the rest. The
recorder now stops and restarts per chunk, giving each one its own header.
`probe-gap` measures what that costs: **0 ms**, because the replacement
recorder starts synchronously inside `onstop` while the MediaStream stays live.

Run `multichunk` after touching `useAudioRecorder.ts`. A unit test cannot catch
this — it is real browser behaviour.
