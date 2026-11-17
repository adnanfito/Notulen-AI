import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { ApiError, api } from '@/services/api'
import { RecorderError, useAudioRecorder } from '@/composables/useAudioRecorder'
import { useSSE } from '@/composables/useSSE'
import { useMeetingStore } from '@/stores/meeting'
import type { AudioSource, RecordedChunk } from '@/composables/useAudioRecorder'
import type {
  ChunkProcessingEvent,
  ChunkStatus,
  ErrorEvent,
  MeetingStatusEvent,
  TranscriptEvent,
} from '@/types/api'

export interface LiveChunk {
  index: number
  text: string
  status: ChunkStatus
  error?: string
}

// Mirrors AUDIO_CHUNK_SECONDS on the backend (PRD §8). Kept configurable so
// the chunk boundary can be exercised without sitting through 90 s.
const DEFAULT_CHUNK_SECONDS = Number(import.meta.env.VITE_AUDIO_CHUNK_SECONDS) || 90
const MAX_UPLOAD_ATTEMPTS = 3

export const useRecordingStore = defineStore('recording', () => {
  const meetingId = ref<string | null>(null)
  const meetingTitle = ref('')
  const source = ref<AudioSource>('MIC_ONLY')
  const chunkSeconds = ref(DEFAULT_CHUNK_SECONDS)

  const active = ref(false)
  const stopping = ref(false)
  const elapsedSeconds = ref(0)

  const uploading = ref(0)
  const uploadedCount = ref(0)
  const failedUploads = ref<number[]>([])

  const error = ref<string | null>(null)
  const hint = ref<string | null>(null)

  // Keyed by chunk_index so a replayed SSE event overwrites rather than
  // duplicates — the idempotency rule from PRD §24.
  const chunks = ref<Map<number, LiveChunk>>(new Map())

  const orderedChunks = computed(() =>
    [...chunks.value.values()].sort((a, b) => a.index - b.index),
  )

  const liveTranscript = computed(() =>
    orderedChunks.value
      .filter((c) => c.status === 'COMPLETED' && c.text)
      .map((c) => c.text)
      .join(' '),
  )

  const processedCount = computed(
    () => orderedChunks.value.filter((c) => c.status === 'COMPLETED').length,
  )
  const failedCount = computed(
    () => orderedChunks.value.filter((c) => c.status === 'FAILED').length,
  )

  function reset() {
    meetingId.value = null
    meetingTitle.value = ''
    active.value = false
    stopping.value = false
    elapsedSeconds.value = 0
    uploading.value = 0
    uploadedCount.value = 0
    failedUploads.value = []
    error.value = null
    hint.value = null
    chunks.value = new Map()
  }

  function markChunk(index: number, patch: Partial<LiveChunk>) {
    const next = new Map(chunks.value)
    const existing = next.get(index) ?? { index, text: '', status: 'PROCESSING' as ChunkStatus }
    next.set(index, { ...existing, ...patch })
    chunks.value = next
  }

  /**
   * Upload one chunk, retrying transient failures.
   *
   * A dropped chunk is a permanent hole in the transcript, so a brief network
   * blip is worth a few retries with backoff.
   */
  async function uploadChunk(chunk: RecordedChunk) {
    if (!meetingId.value) return

    const id = meetingId.value
    uploading.value++
    markChunk(chunk.index, { status: 'PROCESSING' })

    try {
      for (let attempt = 1; attempt <= MAX_UPLOAD_ATTEMPTS; attempt++) {
        try {
          await api.uploadChunk(id, {
            index: chunk.index,
            startSec: chunk.startSec,
            endSec: chunk.endSec,
            blob: chunk.blob,
          })
          uploadedCount.value++
          failedUploads.value = failedUploads.value.filter((i) => i !== chunk.index)
          return
        } catch (err) {
          // The server already has it — a retry that "failed" because it
          // succeeded the first time.
          if (err instanceof ApiError && err.isDuplicate) {
            uploadedCount.value++
            return
          }
          if (attempt === MAX_UPLOAD_ATTEMPTS) throw err
          await new Promise((resolve) => setTimeout(resolve, attempt * 1500))
        }
      }
    } catch (err) {
      if (!failedUploads.value.includes(chunk.index)) {
        failedUploads.value = [...failedUploads.value, chunk.index]
      }
      markChunk(chunk.index, {
        status: 'FAILED',
        error: err instanceof ApiError ? err.message : 'Upload gagal.',
      })
    } finally {
      uploading.value--
    }
  }

  /**
   * The recorder and the event stream live in the store, not in the view.
   *
   * A component-owned recorder dies the moment the user navigates away —
   * Vue unmounts the view and the live MediaRecorder goes with it. Keeping
   * them here lets the user browse the rest of the app while a meeting is
   * still being recorded.
   */
  const recorder = useAudioRecorder({
    chunkSeconds: chunkSeconds.value,
    onChunk: (chunk) => {
      // Show the placeholder immediately, before the transcript comes back.
      markChunk(chunk.index, { status: 'PROCESSING' })
      void uploadChunk(chunk)
    },
    onError: (err) => {
      error.value = err.message
    },
  })

  const sse = useSSE()

  const paused = recorder.paused
  const connection = sse.status

  function connectStream(id: string) {
    sse.connect(api.streamUrl(id), {
      onChunkProcessing: (data) => {
        markChunk((data as ChunkProcessingEvent).chunk_index, { status: 'PROCESSING' })
      },
      onTranscript: (data) => {
        const event = data as TranscriptEvent
        // Keyed by chunk_index, so a replayed event overwrites instead of
        // duplicating (PRD §24).
        markChunk(event.chunk_index, {
          text: event.text,
          status: 'COMPLETED',
          error: undefined,
        })
      },
      onError: (data) => {
        const event = data as ErrorEvent
        markChunk(event.chunk_index, { status: 'FAILED', error: event.message })
      },
      onMeetingStatus: (data) => {
        const event = data as MeetingStatusEvent
        if (event.status === 'COMPLETED' || event.status === 'FAILED') {
          sse.disconnect()
        }
      },
      onReconnect: async () => {
        // Events published while we were offline are gone for good; the
        // database still has them.
        if (!meetingId.value) return
        const detail = await api.getMeeting(meetingId.value).catch(() => null)
        detail?.transcript_chunks.forEach((chunk) => {
          markChunk(chunk.chunk_index, {
            text: chunk.transcript_text,
            status: chunk.status,
            error: chunk.error_message,
          })
        })
      },
    })
  }

  /** Create the meeting and start recording. Returns its id, or null. */
  async function begin(title: string, audioSource: AudioSource) {
    reset()
    source.value = audioSource

    const meetings = useMeetingStore()
    const created = await meetings.createMeeting(title.trim())
    if (!created) {
      error.value = meetings.error ?? 'Tidak bisa membuat rapat.'
      return null
    }

    meetingId.value = created.meeting_id
    meetingTitle.value = created.title
    // Subscribe before recording starts, so no early transcript is missed.
    connectStream(created.meeting_id)

    try {
      await recorder.start(audioSource)
      active.value = true
      return created.meeting_id
    } catch (err) {
      if (err instanceof RecorderError) {
        error.value = err.message
        hint.value = err.hint ?? null
      } else {
        error.value = (err as Error)?.message ?? 'Perekaman gagal dimulai.'
      }
      sse.disconnect()

      // Nothing was recorded, so the row is pure clutter — and it would sit in
      // the list as RECORDING, looking like an unfinished meeting.
      void meetings.deleteMeeting(created.meeting_id)
      meetingId.value = null
      active.value = false
      return null
    }
  }

  /** Stop recording and finish the meeting. Returns its id, or null. */
  async function finish() {
    if (!active.value && !recorder.recording.value) return null
    const id = meetingId.value

    // Awaited: the closing chunk is emitted asynchronously, and finalizing the
    // meeting before it lands drops the end of the transcript.
    const duration = await recorder.stop()
    elapsedSeconds.value = duration
    active.value = false

    const result = await stop(duration)
    sse.disconnect()

    return result ? id : null
  }

  function togglePause() {
    if (recorder.paused.value) {
      recorder.resume()
    } else {
      recorder.pause()
    }
  }

  /** True when this meeting is the one currently being recorded. */
  function isLive(id: string) {
    return active.value && meetingId.value === id
  }

  async function stop(durationSeconds: number) {
    if (!meetingId.value) return null

    stopping.value = true
    active.value = false

    // The closing chunk reaches uploadChunk on a microtask, so `uploading`
    // can still read 0 the instant we get here. Yield once before looking,
    // or the drain exits immediately and the backend finalizes without it.
    await new Promise((resolve) => setTimeout(resolve, 0))

    const deadline = Date.now() + 30_000
    while (uploading.value > 0 && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 250))
    }

    try {
      return await api.stopMeeting(meetingId.value, durationSeconds)
    } catch (err) {
      error.value = err instanceof ApiError ? err.message : 'Gagal menghentikan rapat.'
      return null
    } finally {
      stopping.value = false
    }
  }

  return {
    meetingId,
    meetingTitle,
    source,
    chunkSeconds,
    active,
    stopping,
    elapsedSeconds,
    uploading,
    uploadedCount,
    failedUploads,
    error,
    hint,
    chunks,
    orderedChunks,
    liveTranscript,
    processedCount,
    failedCount,
    reset,
    markChunk,
    uploadChunk,
    stop,
    recorder,
    paused,
    connection,
    begin,
    finish,
    togglePause,
    isLive,
  }
})
