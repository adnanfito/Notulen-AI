// Package meeting owns meetings and their transcripts — the core domain.
package meeting

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Meeting lifecycle (PRD §32).
const (
	StatusRecording  = "RECORDING"
	StatusProcessing = "PROCESSING"
	StatusCompleted  = "COMPLETED"
	StatusFailed     = "FAILED"
)

// Per-chunk transcription state.
const (
	ChunkProcessing = "PROCESSING"
	ChunkCompleted  = "COMPLETED"
	ChunkFailed     = "FAILED"
)

type Meeting struct {
	ID              uuid.UUID       `json:"id"`
	Title           string          `json:"title"`
	Status          string          `json:"status"`
	LanguageUsed    *string         `json:"language_used"`
	DurationSeconds int             `json:"duration_seconds"`
	RawTranscript   *string         `json:"raw_transcript,omitempty"`
	SummaryData     json.RawMessage `json:"summary_data"`
	// SourceMeetingIDs is set only on meetings assembled from others, in the
	// order they were joined. Nil for an ordinary recorded meeting.
	SourceMeetingIDs []uuid.UUID `json:"source_meeting_ids,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

type TranscriptChunk struct {
	ID           uuid.UUID `json:"id"`
	MeetingID    uuid.UUID `json:"meeting_id"`
	ChunkIndex   int       `json:"chunk_index"`
	StartTimeSec int       `json:"start_time_sec"`
	EndTimeSec   int       `json:"end_time_sec"`
	Text         string    `json:"transcript_text"`
	Status       string    `json:"status"`
	ErrorMessage *string   `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ListItem is the lighter shape used for the dashboard: it deliberately omits
// transcripts, which would make the list response enormous.
type ListItem struct {
	ID              uuid.UUID `json:"id"`
	Title           string    `json:"title"`
	Status          string    `json:"status"`
	LanguageUsed    *string   `json:"language_used"`
	DurationSeconds int       `json:"duration_seconds"`
	CreatedAt       time.Time `json:"created_at"`
	HasSummary      bool      `json:"has_summary"`
	IsCombined      bool      `json:"is_combined"`
}

type ListResult struct {
	Items []ListItem `json:"items"`
	Page  int        `json:"page"`
	Limit int        `json:"limit"`
	Total int        `json:"total"`
}

type Detail struct {
	Meeting
	Chunks []TranscriptChunk `json:"transcript_chunks"`
}

// --- requests ---

type CreateRequest struct {
	Title string `json:"title"`
}

type UpdateRequest struct {
	Title       *string         `json:"title"`
	SummaryData json.RawMessage `json:"summary_data"`
}

type ChunkUpload struct {
	Index        int
	StartTimeSec int
	EndTimeSec   int
	Filename     string
	Path         string
}

type StopRequest struct {
	DurationSeconds int `json:"duration_seconds"`
}

type CombineRequest struct {
	MeetingIDs []uuid.UUID `json:"meeting_ids"`
	Title      string      `json:"title"`
}
