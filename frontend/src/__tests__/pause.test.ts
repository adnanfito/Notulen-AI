/**
 * Pause bookkeeping.
 *
 * Chunk timings are transcript timestamps, so they have to measure recorded
 * audio rather than the clock. Get this wrong and every chunk after a pause
 * claims a span longer than the audio it contains — the transcript drifts and
 * nobody notices until they try to find a quote by its timestamp.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useAudioRecorder, type RecordedChunk } from '@/composables/useAudioRecorder'

// A MediaRecorder stand-in: one blob per recorder, emitted on stop.
class FakeMediaRecorder {
  static instances: FakeMediaRecorder[] = []
  static isTypeSupported = () => true

  state: 'inactive' | 'recording' | 'paused' = 'inactive'
  ondataavailable: ((e: { data: Blob }) => void) | null = null
  onstop: (() => void) | null = null
  onerror: ((e: unknown) => void) | null = null

  constructor(
    public stream: MediaStream,
    public options?: { mimeType?: string },
  ) {
    FakeMediaRecorder.instances.push(this)
  }

  start() {
    this.state = 'recording'
  }

  stop() {
    if (this.state === 'inactive') return
    this.state = 'inactive'
    this.ondataavailable?.({ data: new Blob(['audio'], { type: 'audio/webm' }) })
    this.onstop?.()
  }

  addEventListener(type: string, fn: () => void, opts?: { once?: boolean }) {
    void type
    void fn
    void opts
  }
}

function fakeTrack() {
  return { stop: vi.fn(), addEventListener: vi.fn() } as unknown as MediaStreamTrack
}

function fakeStream() {
  const tracks = [fakeTrack()]
  return {
    getTracks: () => tracks,
    getAudioTracks: () => tracks,
    getVideoTracks: () => [],
    removeTrack: vi.fn(),
  } as unknown as MediaStream
}

describe('pause and resume', () => {
  let chunks: RecordedChunk[]

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-12T10:00:00Z'))
    FakeMediaRecorder.instances = []
    chunks = []

    vi.stubGlobal('MediaRecorder', FakeMediaRecorder)
    vi.stubGlobal('navigator', {
      mediaDevices: { getUserMedia: vi.fn().mockResolvedValue(fakeStream()) },
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  function makeRecorder() {
    return useAudioRecorder({
      chunkSeconds: 90,
      onChunk: (c) => chunks.push(c),
    })
  }

  it('freezes the elapsed timer while paused', async () => {
    const rec = makeRecorder()
    await rec.start('MIC_ONLY')

    await vi.advanceTimersByTimeAsync(10_000)
    expect(rec.elapsedSeconds.value).toBe(10)

    rec.pause()
    await vi.advanceTimersByTimeAsync(30_000)

    // Thirty seconds of wall clock, none of it recorded.
    expect(rec.elapsedSeconds.value).toBe(10)

    rec.resume()
    await vi.advanceTimersByTimeAsync(5_000)
    expect(rec.elapsedSeconds.value).toBe(15)
  })

  it('excludes paused time from chunk timings', async () => {
    const rec = makeRecorder()
    await rec.start('MIC_ONLY')

    await vi.advanceTimersByTimeAsync(20_000)
    rec.pause()

    // The pause flushes the chunk recorded so far.
    expect(chunks).toHaveLength(1)
    expect(chunks[0].startSec).toBe(0)
    expect(chunks[0].endSec).toBeCloseTo(20, 1)

    await vi.advanceTimersByTimeAsync(60_000) // a full minute paused
    rec.resume()
    await vi.advanceTimersByTimeAsync(15_000)
    await rec.stop()

    expect(chunks).toHaveLength(2)
    // Continuous with the first chunk, and the paused minute is not counted.
    expect(chunks[1].startSec).toBeCloseTo(20, 1)
    expect(chunks[1].endSec).toBeCloseTo(35, 1)
  })

  it('stays continuous across several pauses', async () => {
    const rec = makeRecorder()
    await rec.start('MIC_ONLY')

    for (let i = 0; i < 3; i++) {
      await vi.advanceTimersByTimeAsync(10_000)
      rec.pause()
      await vi.advanceTimersByTimeAsync(20_000)
      rec.resume()
    }
    await vi.advanceTimersByTimeAsync(10_000)
    await rec.stop()

    // 4 spans of 10s recorded; 60s of pause discarded.
    const last = chunks[chunks.length - 1]
    expect(last.endSec).toBeCloseTo(40, 1)

    // Chunks must tile the timeline with no gaps or overlaps.
    let previousEnd = 0
    for (const chunk of chunks) {
      expect(chunk.startSec).toBeCloseTo(previousEnd, 1)
      previousEnd = chunk.endSec
    }
  })

  it('reports a duration that matches the audio, not the clock', async () => {
    const rec = makeRecorder()
    await rec.start('MIC_ONLY')

    await vi.advanceTimersByTimeAsync(30_000)
    rec.pause()
    await vi.advanceTimersByTimeAsync(120_000)
    rec.resume()
    await vi.advanceTimersByTimeAsync(30_000)

    // Three and a half minutes elapsed; one minute recorded.
    await expect(rec.stop()).resolves.toBe(60)
  })

  it('does not start a new chunk while paused', async () => {
    const rec = makeRecorder()
    await rec.start('MIC_ONLY')
    const before = FakeMediaRecorder.instances.length

    rec.pause()
    await vi.advanceTimersByTimeAsync(200_000) // well past chunkSeconds

    expect(FakeMediaRecorder.instances.length).toBe(before)
    expect(chunks).toHaveLength(1) // only the one the pause flushed
  })

  it('starts a fresh recorder on resume so each chunk stays standalone', async () => {
    const rec = makeRecorder()
    await rec.start('MIC_ONLY')
    const before = FakeMediaRecorder.instances.length

    rec.pause()
    rec.resume()

    expect(FakeMediaRecorder.instances.length).toBe(before + 1)
  })

  it('ignores pause and resume when they do not apply', async () => {
    const rec = makeRecorder()

    // Not recording yet.
    rec.pause()
    expect(rec.paused.value).toBe(false)

    await rec.start('MIC_ONLY')

    // Resuming while running, and pausing twice, must both be no-ops.
    rec.resume()
    expect(rec.paused.value).toBe(false)

    rec.pause()
    rec.pause()
    expect(rec.paused.value).toBe(true)
    expect(chunks).toHaveLength(1)
  })

  it('can stop while paused', async () => {
    const rec = makeRecorder()
    await rec.start('MIC_ONLY')

    await vi.advanceTimersByTimeAsync(15_000)
    rec.pause()
    await vi.advanceTimersByTimeAsync(5_000)

    await expect(rec.stop()).resolves.toBe(15)
    expect(rec.recording.value).toBe(false)
    expect(rec.paused.value).toBe(false)
  })

  it('clears paused state for the next meeting', async () => {
    const rec = makeRecorder()
    await rec.start('MIC_ONLY')
    rec.pause()
    await rec.stop()

    await rec.start('MIC_ONLY')
    expect(rec.paused.value).toBe(false)
    expect(rec.elapsedSeconds.value).toBe(0)
  })
})
