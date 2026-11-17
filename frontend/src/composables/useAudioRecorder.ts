/**
 * Browser recording: source selection, mixing, and timesliced chunking.
 *
 * All MediaRecorder/getUserMedia handling lives here rather than in components
 * (PRD §25), so the views only ever see "start", "stop", and a stream of
 * finished chunks.
 */

import { ref, shallowRef } from 'vue'

export type AudioSource = 'MIC_ONLY' | 'SYSTEM_ONLY' | 'FULL_MEETING'

export interface RecordedChunk {
  index: number
  blob: Blob
  startSec: number
  endSec: number
}

/** Ordered by preference; the first one the browser supports wins (PRD §7). */
const MIME_CANDIDATES = [
  'audio/webm;codecs=opus',
  'audio/webm',
  'audio/ogg;codecs=opus',
  'audio/mp4',
]

export function pickMimeType(): string | null {
  if (typeof MediaRecorder === 'undefined') return null
  return MIME_CANDIDATES.find((type) => MediaRecorder.isTypeSupported(type)) ?? null
}

export function isRecordingSupported(): boolean {
  return (
    typeof MediaRecorder !== 'undefined' &&
    typeof navigator !== 'undefined' &&
    !!navigator.mediaDevices?.getUserMedia &&
    pickMimeType() !== null
  )
}

export function supportsSystemAudio(): boolean {
  return typeof navigator !== 'undefined' && !!navigator.mediaDevices?.getDisplayMedia
}

/** Raised for problems worth explaining to the user rather than logging. */
export class RecorderError extends Error {
  constructor(
    message: string,
    public readonly hint?: string,
  ) {
    super(message)
    this.name = 'RecorderError'
  }
}

interface Options {
  chunkSeconds: number
  onChunk: (chunk: RecordedChunk) => void
  onError?: (error: Error) => void
}

export function useAudioRecorder(options: Options) {
  const recording = ref(false)
  const paused = ref(false)
  const elapsedSeconds = ref(0)
  const mimeType = ref<string | null>(null)

  const recorder = shallowRef<MediaRecorder | null>(null)
  const tracks: MediaStreamTrack[] = []
  let audioContext: AudioContext | null = null
  let timer: number | null = null
  let cycleTimer: number | null = null
  let activeStream: MediaStream | null = null
  let startedAt = 0

  // Wall-clock time spent paused. Chunk timings are transcript timestamps, so
  // they must measure recorded audio, not the clock — otherwise every chunk
  // after a pause claims a span longer than the audio it contains.
  let pausedTotalMs = 0
  let pausedAt = 0

  /** Seconds of audio actually captured so far, excluding paused time. */
  function recordedSeconds(): number {
    if (!startedAt) return 0
    const inPause = pausedAt ? Date.now() - pausedAt : 0
    return Math.max(0, (Date.now() - startedAt - pausedTotalMs - inPause) / 1000)
  }

  // Chunk bookkeeping is ours, not MediaRecorder's: ondataavailable gives no
  // index and no timing, and the backend needs both.
  let chunkIndex = 0
  let chunkStart = 0

  async function getMic(): Promise<MediaStream> {
    try {
      return await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
      })
    } catch (err) {
      throw new RecorderError(
        'Tidak bisa mengakses mikrofon.',
        (err as Error)?.name === 'NotAllowedError'
          ? 'Izin mikrofon ditolak. Aktifkan lewat ikon gembok di address bar.'
          : 'Pastikan ada perangkat mikrofon yang aktif.',
      )
    }
  }

  async function getSystem(): Promise<MediaStream> {
    if (!supportsSystemAudio()) {
      throw new RecorderError(
        'Browser ini tidak mendukung perekaman audio sistem.',
        'Gunakan browser berbasis Chromium.',
      )
    }

    let display: MediaStream
    try {
      // video: true is required — most browsers refuse a display capture that
      // asks for audio alone. The video track is discarded below (PRD §6).
      display = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: true })
    } catch (err) {
      throw new RecorderError(
        'Perekaman audio sistem dibatalkan.',
        (err as Error)?.name === 'NotAllowedError'
          ? 'Kamu membatalkan dialog berbagi layar.'
          : undefined,
      )
    }

    display.getVideoTracks().forEach((track) => {
      track.stop()
      display.removeTrack(track)
    })

    if (display.getAudioTracks().length === 0) {
      display.getTracks().forEach((t) => t.stop())
      throw new RecorderError(
        'Layar dibagikan tanpa audio.',
        'Centang "Bagikan audio tab" di dialog. Membagikan tab biasanya lebih andal daripada seluruh layar.',
      )
    }

    return display
  }

  /**
   * Mix two streams into one recordable track.
   *
   * MediaRecorder takes a single stream, so mic and system audio have to be
   * summed in an AudioContext first. Each side is attenuated because two
   * full-scale sources added together clip (PRD §6).
   */
  function mix(streams: MediaStream[]): MediaStream {
    audioContext = new AudioContext()
    const destination = audioContext.createMediaStreamDestination()

    for (const stream of streams) {
      const source = audioContext.createMediaStreamSource(stream)
      const gain = audioContext.createGain()
      gain.gain.value = 0.8
      source.connect(gain).connect(destination)
    }

    return destination.stream
  }

  async function buildStream(source: AudioSource): Promise<MediaStream> {
    if (source === 'MIC_ONLY') {
      const mic = await getMic()
      mic.getTracks().forEach((t) => tracks.push(t))
      return mic
    }

    if (source === 'SYSTEM_ONLY') {
      const system = await getSystem()
      system.getTracks().forEach((t) => tracks.push(t))
      return system
    }

    const mic = await getMic()
    mic.getTracks().forEach((t) => tracks.push(t))

    let system: MediaStream
    try {
      system = await getSystem()
    } catch (err) {
      // The mic is already open; release it before surfacing the failure.
      stopTracks()
      throw err
    }
    system.getTracks().forEach((t) => tracks.push(t))

    return mix([mic, system])
  }

  async function start(source: AudioSource) {
    if (recording.value) return

    const type = pickMimeType()
    if (!type) {
      throw new RecorderError(
        'Browser ini tidak mendukung perekaman audio.',
        'Coba Chrome, Edge, atau browser Chromium lainnya.',
      )
    }
    mimeType.value = type

    const stream = await buildStream(source)

    chunkIndex = 0
    chunkStart = 0
    elapsedSeconds.value = 0
    pausedTotalMs = 0
    pausedAt = 0
    paused.value = false
    startedAt = Date.now()
    activeStream = stream
    recording.value = true

    // A user who stops screen sharing from the browser's own bar ends the
    // meeting; without this the recorder would keep producing silence.
    tracks.forEach((track) => {
      track.addEventListener('ended', () => {
        if (recording.value) stop()
      })
    })

    beginCycle()

    timer = window.setInterval(() => {
      elapsedSeconds.value = Math.floor(recordedSeconds())
    }, 1000)
  }

  /**
   * Record one chunk, then start a fresh recorder for the next.
   *
   * MediaRecorder's `timeslice` looks like the obvious fit, but it writes the
   * WebM/EBML header into the FIRST blob only — every later blob is a bare
   * cluster fragment that no decoder can open on its own. Chunk 0 would
   * transcribe and every chunk after it would fail as undecodable.
   *
   * Stopping and restarting gives each chunk its own header, so every chunk is
   * a complete file. The price is the few milliseconds between stop and the
   * next start.
   */
  function beginCycle() {
    if (!recording.value || !activeStream || !mimeType.value) return

    const rec = new MediaRecorder(activeStream, { mimeType: mimeType.value })

    rec.ondataavailable = (event) => {
      if (!event.data || event.data.size === 0) return

      const endSec = recordedSeconds()
      options.onChunk({
        index: chunkIndex++,
        blob: event.data,
        startSec: chunkStart,
        endSec,
      })
      chunkStart = endSec
    }

    rec.onerror = (event) => {
      const err = (event as unknown as { error?: Error }).error
      options.onError?.(err ?? new Error('Perekaman gagal.'))
    }

    // Chain from onstop rather than a second timer: the next chunk must not
    // begin until this one has been handed over.
    rec.onstop = () => {
      // Pause stops the recorder to flush the chunk; it must not immediately
      // start the next one.
      if (recording.value && !paused.value) beginCycle()
    }

    // No timeslice — one blob per recorder, emitted on stop.
    rec.start()
    recorder.value = rec

    cycleTimer = window.setTimeout(() => {
      if (rec.state === 'recording') rec.stop()
    }, options.chunkSeconds * 1000)
  }

  /**
   * Pause recording.
   *
   * Implemented by ending the current chunk rather than MediaRecorder.pause():
   * every chunk has to be a standalone WebM file, and stopping is what gives
   * it its own header. The flushed chunk uploads and transcribes while paused,
   * so the pause costs nothing. The microphone stream stays open, so resuming
   * is instant and does not re-prompt for permission.
   */
  function pause() {
    if (!recording.value || paused.value) return

    if (cycleTimer !== null) {
      clearTimeout(cycleTimer)
      cycleTimer = null
    }

    // Set before stopping, so onstop does not start the next chunk.
    paused.value = true
    pausedAt = Date.now()

    const rec = recorder.value
    if (rec && rec.state === 'recording') rec.stop()
  }

  function resume() {
    if (!recording.value || !paused.value) return

    // Fold this pause into the total before clearing it, so recordedSeconds
    // stays continuous across any number of pauses.
    if (pausedAt) {
      pausedTotalMs += Date.now() - pausedAt
      pausedAt = 0
    }
    paused.value = false

    beginCycle()
  }

  function stopTracks() {
    tracks.splice(0).forEach((track) => track.stop())
    if (audioContext) {
      void audioContext.close().catch(() => {})
      audioContext = null
    }
  }

  /**
   * Stop recording and resolve once the final chunk has been handed over.
   *
   * This MUST be awaited. MediaRecorder emits the last partial chunk
   * asynchronously via `ondataavailable`, so returning synchronously lets the
   * caller finalize the meeting while the closing chunk is still in flight —
   * the backend then combines the transcript without it, and the end of the
   * meeting, where decisions land, is silently missing.
   *
   * Per spec `ondataavailable` fires before `onstop`, so onstop is the point
   * at which the chunk is guaranteed to have reached `options.onChunk`.
   */
  function stop(): Promise<number> {
    // Close out an in-flight pause so the reported duration matches the audio.
    if (pausedAt) {
      pausedTotalMs += Date.now() - pausedAt
      pausedAt = 0
    }
    const total = Math.floor(recordedSeconds())

    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
    if (cycleTimer !== null) {
      clearTimeout(cycleTimer)
      cycleTimer = null
    }

    // Cleared before rec.stop() so onstop does not start another cycle.
    recording.value = false
    paused.value = false

    const rec = recorder.value
    recorder.value = null

    const finished = new Promise<void>((resolve) => {
      if (!rec || rec.state === 'inactive') {
        resolve()
        return
      }

      rec.onstop = () => resolve()
      // A browser that never fires onstop must not hang the stop button.
      const bail = window.setTimeout(resolve, 5000)
      const done = () => window.clearTimeout(bail)
      rec.addEventListener('stop', done, { once: true })

      rec.stop()
    })

    return finished.then(() => {
      activeStream = null
      stopTracks()
      return total
    })
  }

  return { recording, paused, elapsedSeconds, mimeType, start, pause, resume, stop }
}
