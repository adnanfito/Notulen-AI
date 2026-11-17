import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { ApiError } from '@/services/api'
import { useRecordingStore } from '@/stores/recording'

const uploadChunk = vi.fn()

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api')
  return {
    ...actual,
    api: {
      uploadChunk: (...args: unknown[]) => uploadChunk(...args),
      stopMeeting: vi.fn().mockResolvedValue({ meeting_id: 'm1', status: 'PROCESSING' }),
    },
  }
})

function chunk(index: number) {
  return {
    index,
    blob: new Blob(['audio'], { type: 'audio/webm' }),
    startSec: index * 90,
    endSec: (index + 1) * 90,
  }
}

describe('recording store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    uploadChunk.mockReset()
    vi.useRealTimers()
  })

  // STT finishes out of order; the UI must still read top to bottom (PRD §10).
  it('orders chunks by index regardless of arrival order', () => {
    const store = useRecordingStore()

    store.markChunk(2, { text: 'ketiga', status: 'COMPLETED' })
    store.markChunk(0, { text: 'pertama', status: 'COMPLETED' })
    store.markChunk(1, { text: 'kedua', status: 'COMPLETED' })

    expect(store.orderedChunks.map((c) => c.index)).toEqual([0, 1, 2])
    expect(store.liveTranscript).toBe('pertama kedua ketiga')
  })

  // A reconnect can replay events already applied (PRD §24).
  it('is idempotent when the same chunk arrives twice', () => {
    const store = useRecordingStore()

    store.markChunk(0, { text: 'halo', status: 'COMPLETED' })
    store.markChunk(0, { text: 'halo', status: 'COMPLETED' })

    expect(store.orderedChunks).toHaveLength(1)
    expect(store.liveTranscript).toBe('halo')
  })

  it('upgrades a processing placeholder to completed text', () => {
    const store = useRecordingStore()

    store.markChunk(0, { status: 'PROCESSING' })
    expect(store.processedCount).toBe(0)

    store.markChunk(0, { text: 'sudah jadi', status: 'COMPLETED' })

    expect(store.orderedChunks).toHaveLength(1)
    expect(store.processedCount).toBe(1)
  })

  // Silence transcribes to empty text; it must not leave a gap of spaces.
  it('leaves silent chunks out of the transcript without counting them failed', () => {
    const store = useRecordingStore()

    store.markChunk(0, { text: 'sebelum', status: 'COMPLETED' })
    store.markChunk(1, { text: '', status: 'COMPLETED' })
    store.markChunk(2, { text: 'sesudah', status: 'COMPLETED' })

    expect(store.liveTranscript).toBe('sebelum sesudah')
    expect(store.failedCount).toBe(0)
  })

  it('counts failed chunks separately', () => {
    const store = useRecordingStore()

    store.markChunk(0, { text: 'ok', status: 'COMPLETED' })
    store.markChunk(1, { status: 'FAILED', error: 'stt down' })

    expect(store.failedCount).toBe(1)
    expect(store.processedCount).toBe(1)
    expect(store.liveTranscript).toBe('ok')
  })

  describe('upload', () => {
    it('uploads a chunk once when the network cooperates', async () => {
      const store = useRecordingStore()
      store.meetingId = 'm1'
      uploadChunk.mockResolvedValue({ chunk_id: 'c1', chunk_index: 0, status: 'PROCESSING' })

      await store.uploadChunk(chunk(0))

      expect(uploadChunk).toHaveBeenCalledTimes(1)
      expect(store.uploadedCount).toBe(1)
      expect(store.failedUploads).toEqual([])
    })

    // A dropped chunk is a permanent hole in the transcript, so a blip is
    // worth retrying.
    it('retries a transient failure and succeeds', async () => {
      const store = useRecordingStore()
      store.meetingId = 'm1'
      uploadChunk
        .mockRejectedValueOnce(new ApiError('NETWORK_ERROR', 'offline', 0))
        .mockResolvedValueOnce({ chunk_id: 'c1', chunk_index: 0, status: 'PROCESSING' })

      await store.uploadChunk(chunk(0))

      expect(uploadChunk).toHaveBeenCalledTimes(2)
      expect(store.uploadedCount).toBe(1)
    })

    it('gives up after the retry budget and records the failure', async () => {
      const store = useRecordingStore()
      store.meetingId = 'm1'
      uploadChunk.mockRejectedValue(new ApiError('NETWORK_ERROR', 'offline', 0))

      await store.uploadChunk(chunk(3))

      expect(uploadChunk).toHaveBeenCalledTimes(3)
      expect(store.failedUploads).toEqual([3])
      expect(store.chunks.get(3)?.status).toBe('FAILED')
    })

    // 409 means the first attempt actually landed — a "failure" that is a
    // success.
    it('treats a duplicate rejection as delivered', async () => {
      const store = useRecordingStore()
      store.meetingId = 'm1'
      uploadChunk.mockRejectedValue(new ApiError('CHUNK_ALREADY_EXISTS', 'sudah ada', 409))

      await store.uploadChunk(chunk(0))

      expect(uploadChunk).toHaveBeenCalledTimes(1)
      expect(store.uploadedCount).toBe(1)
      expect(store.failedUploads).toEqual([])
    })

    it('does nothing without a meeting', async () => {
      const store = useRecordingStore()
      await store.uploadChunk(chunk(0))
      expect(uploadChunk).not.toHaveBeenCalled()
    })
  })

  it('reset clears everything for the next meeting', () => {
    const store = useRecordingStore()
    store.meetingId = 'm1'
    store.active = true
    store.markChunk(0, { text: 'lama', status: 'COMPLETED' })

    store.reset()

    expect(store.meetingId).toBeNull()
    expect(store.active).toBe(false)
    expect(store.orderedChunks).toEqual([])
    expect(store.liveTranscript).toBe('')
  })
})
