export type DependencyStatus = 'ok' | 'degraded' | 'down'

export interface HealthDependency {
  status: DependencyStatus
  detail?: string
  error?: string
  model?: string
  device?: string
  compute_type?: string
}

export interface HealthResponse {
  status: 'ok' | 'degraded'
  service: string
  time: string
  dependencies: Record<string, HealthDependency>
}

export type MeetingStatus = 'RECORDING' | 'PROCESSING' | 'COMPLETED' | 'FAILED'
export type ChunkStatus = 'PROCESSING' | 'COMPLETED' | 'FAILED'

export interface ActionItem {
  task: string
  assignee: string | null
  deadline: string | null
  completed: boolean
}

export interface Topic {
  topic_name: string
  discussion_points: string[]
}

export interface SummaryData {
  title: string
  language_used: string
  executive_summary: string
  key_decisions: string[]
  main_topics: Topic[]
  action_items: ActionItem[]
  tags: string[]
}

export interface TranscriptChunk {
  id: string
  meeting_id: string
  chunk_index: number
  start_time_sec: number
  end_time_sec: number
  transcript_text: string
  status: ChunkStatus
  error_message?: string
}

export interface MeetingListItem {
  id: string
  title: string
  status: MeetingStatus
  language_used: string | null
  duration_seconds: number
  created_at: string
  has_summary: boolean
  is_combined: boolean
}

export interface MeetingList {
  items: MeetingListItem[]
  page: number
  limit: number
  total: number
}

export interface MeetingDetail {
  id: string
  title: string
  status: MeetingStatus
  language_used: string | null
  duration_seconds: number
  raw_transcript?: string | null
  summary_data: SummaryData | null
  source_meeting_ids?: string[]
  created_at: string
  updated_at: string
  transcript_chunks: TranscriptChunk[]
}

export interface CreatedMeeting {
  meeting_id: string
  title: string
  status: MeetingStatus
  created_at: string
}

export interface ChunkAccepted {
  chunk_id: string
  chunk_index: number
  status: ChunkStatus
}

// --- SSE payloads (PRD §24) ---

export interface TranscriptEvent {
  chunk_index: number
  text: string
  language?: string
  status: ChunkStatus
}

export interface ChunkProcessingEvent {
  chunk_index: number
  status: ChunkStatus
}

export interface MeetingStatusEvent {
  status: MeetingStatus
  failed_chunks?: number
  has_summary?: boolean
  message?: string
}

export interface ErrorEvent {
  chunk_index: number
  status: ChunkStatus
  message: string
}
