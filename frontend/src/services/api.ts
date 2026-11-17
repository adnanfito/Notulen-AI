import type {
  ChunkAccepted,
  CreatedMeeting,
  HealthResponse,
  MeetingDetail,
  MeetingList,
  SummaryData,
} from '@/types/api'

// Empty base means same-origin, which is what the Vite proxy and the nginx
// production config both give us.
const API_BASE = import.meta.env.VITE_API_BASE ?? ''

export const API_ROOT = `${API_BASE}/api/v1`

/** An error carrying the backend's error envelope (PRD §36). */
export class ApiError extends Error {
  constructor(
    public readonly code: string,
    message: string,
    public readonly status: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }

  /** Retrying an upload the server already has will never succeed. */
  get isDuplicate() {
    return this.code === 'CHUNK_ALREADY_EXISTS'
  }
}

interface ErrorEnvelope {
  error?: { code?: string; message?: string }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response
  try {
    response = await fetch(`${API_ROOT}${path}`, {
      headers: init?.body instanceof FormData ? init.headers : { Accept: 'application/json', ...(init?.headers ?? {}) },
      ...init,
    })
  } catch {
    // fetch only rejects on network-level failure, which the user reads as
    // "the backend is not running".
    throw new ApiError('NETWORK_ERROR', 'Tidak dapat terhubung ke backend.', 0)
  }

  if (!response.ok) {
    let code = 'INTERNAL_ERROR'
    let message = `Request gagal (${response.status})`
    try {
      const body = (await response.json()) as ErrorEnvelope
      code = body.error?.code ?? code
      message = body.error?.message ?? message
    } catch {
      // Non-JSON error body; keep the status-derived message.
    }
    throw new ApiError(code, message, response.status)
  }

  if (response.status === 204) {
    return undefined as T
  }
  return (await response.json()) as T
}

function json(body: unknown): RequestInit {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

export const api = {
  health: () => request<HealthResponse>('/health'),

  createMeeting: (title?: string) =>
    request<CreatedMeeting>('/meetings', json({ title: title ?? '' })),

  listMeetings: (params: { q?: string; page?: number; limit?: number } = {}) => {
    const search = new URLSearchParams()
    if (params.q) search.set('q', params.q)
    search.set('page', String(params.page ?? 1))
    search.set('limit', String(params.limit ?? 20))
    return request<MeetingList>(`/meetings?${search.toString()}`)
  },

  getMeeting: (id: string) => request<MeetingDetail>(`/meetings/${id}`),

  updateMeeting: (id: string, patch: { title?: string; summary_data?: SummaryData }) =>
    request<MeetingDetail>(`/meetings/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch),
    }),

  deleteMeeting: (id: string) => request<void>(`/meetings/${id}`, { method: 'DELETE' }),

  uploadChunk: (
    meetingId: string,
    chunk: { index: number; startSec: number; endSec: number; blob: Blob },
    signal?: AbortSignal,
  ) => {
    const form = new FormData()
    form.set('chunk_index', String(chunk.index))
    form.set('start_time_sec', String(Math.round(chunk.startSec)))
    form.set('end_time_sec', String(Math.round(chunk.endSec)))
    // The extension is a hint only; the server decides what it will accept.
    form.set('audio_file', chunk.blob, `chunk-${chunk.index}.webm`)

    return request<ChunkAccepted>(`/meetings/${meetingId}/chunks`, {
      method: 'POST',
      body: form,
      signal,
    })
  },

  /** Assemble several meetings into one set of notes. Sources are untouched. */
  combineMeetings: (meetingIds: string[], title?: string) =>
    request<{ meeting_id: string; title: string; status: string; sources: number }>(
      '/meetings/combine',
      json({ meeting_ids: meetingIds, title: title ?? '' }),
    ),

  /** Ask the backend to generate the notes again — the LLM may have been busy. */
  resummarize: (id: string) =>
    request<{ meeting_id: string; status: string }>(`/meetings/${id}/summarize`, {
      method: 'POST',
    }),

  stopMeeting: (id: string, durationSeconds: number) =>
    request<{ meeting_id: string; status: string }>(
      `/meetings/${id}/stop`,
      json({ duration_seconds: Math.round(durationSeconds) }),
    ),

  streamUrl: (id: string) => `${API_ROOT}/meetings/${id}/stream`,
}
