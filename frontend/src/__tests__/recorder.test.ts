import { afterEach, describe, expect, it, vi } from 'vitest'

import { isRecordingSupported, pickMimeType, supportsSystemAudio } from '@/composables/useAudioRecorder'
import { formatDate, formatDuration, formatDurationShort } from '@/utils/format'

function stubMediaRecorder(supported: string[]) {
  vi.stubGlobal('MediaRecorder', {
    isTypeSupported: (type: string) => supported.includes(type),
  })
}

function stubMediaDevices(devices: Record<string, unknown> | undefined) {
  vi.stubGlobal('navigator', { mediaDevices: devices })
}

describe('MediaRecorder support detection', () => {
  afterEach(() => vi.unstubAllGlobals())

  // Opus in WebM is the preferred format (PRD §7).
  it('prefers webm/opus when available', () => {
    stubMediaRecorder(['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4'])
    expect(pickMimeType()).toBe('audio/webm;codecs=opus')
  })

  it('falls back through the candidate list', () => {
    stubMediaRecorder(['audio/mp4'])
    expect(pickMimeType()).toBe('audio/mp4')
  })

  it('returns null when nothing is supported', () => {
    stubMediaRecorder([])
    expect(pickMimeType()).toBeNull()
  })

  it('reports unsupported when MediaRecorder is missing entirely', () => {
    vi.stubGlobal('MediaRecorder', undefined)
    expect(pickMimeType()).toBeNull()
    expect(isRecordingSupported()).toBe(false)
  })

  it('requires both a codec and getUserMedia', () => {
    stubMediaRecorder(['audio/webm;codecs=opus'])

    stubMediaDevices({ getUserMedia: vi.fn() })
    expect(isRecordingSupported()).toBe(true)

    // A browser can expose MediaRecorder but no capture API.
    stubMediaDevices({})
    expect(isRecordingSupported()).toBe(false)
  })

  // System audio is the capability most likely to be missing (PRD §40).
  it('detects system-audio capability separately from the microphone', () => {
    stubMediaDevices({ getUserMedia: vi.fn() })
    expect(supportsSystemAudio()).toBe(false)

    stubMediaDevices({ getUserMedia: vi.fn(), getDisplayMedia: vi.fn() })
    expect(supportsSystemAudio()).toBe(true)
  })

  it('survives a browser with no mediaDevices at all', () => {
    stubMediaDevices(undefined)
    expect(supportsSystemAudio()).toBe(false)
    expect(isRecordingSupported()).toBe(false)
  })
})

describe('formatting', () => {
  it('shows mm:ss under an hour and hh:mm:ss beyond it', () => {
    expect(formatDuration(0)).toBe('00:00')
    expect(formatDuration(59)).toBe('00:59')
    expect(formatDuration(90)).toBe('01:30')
    expect(formatDuration(3599)).toBe('59:59')
    expect(formatDuration(3600)).toBe('01:00:00')
    // A 3-hour meeting is explicitly in scope (PRD §39).
    expect(formatDuration(10_800)).toBe('03:00:00')
  })

  it('never renders negative time', () => {
    expect(formatDuration(-5)).toBe('00:00')
  })

  it('summarises duration for list rows', () => {
    // Rounding a sub-minute meeting to "0 mnt" reads as a failed recording.
    expect(formatDurationShort(0)).toBe('0 dtk')
    expect(formatDurationShort(22)).toBe('22 dtk')
    expect(formatDurationShort(59)).toBe('59 dtk')
    expect(formatDurationShort(180)).toBe('3 mnt')
    expect(formatDurationShort(3480)).toBe('58 mnt')
    expect(formatDurationShort(3600)).toBe('1 j')
    expect(formatDurationShort(4320)).toBe('1 j 12 mnt')
  })

  it('does not crash on an unparseable date', () => {
    expect(formatDate('not-a-date')).toBe('-')
  })
})
