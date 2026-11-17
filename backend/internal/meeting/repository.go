package meeting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("meeting not found")
	ErrChunkConflict = errors.New("chunk already exists")
)

// maxLanguageLen mirrors the language_used column width.
const maxLanguageLen = 50

// clampLanguage keeps an over-long value from aborting the write it rides
// along with. Losing a transcript because a model was chatty about which
// language it used is not a trade worth making.
func clampLanguage(v *string) *string {
	if v == nil {
		return nil
	}
	runes := []rune(*v)
	if len(runes) <= maxLanguageLen {
		return v
	}
	clamped := string(runes[:maxLanguageLen])
	return &clamped
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, title string) (*Meeting, error) {
	const q = `
		INSERT INTO meetings (title)
		VALUES (COALESCE(NULLIF($1, ''), 'Untitled Meeting'))
		RETURNING id, title, status, language_used, duration_seconds,
		          summary_data, source_meeting_ids, created_at, updated_at`

	var m Meeting
	err := r.pool.QueryRow(ctx, q, title).Scan(
		&m.ID, &m.Title, &m.Status, &m.LanguageUsed, &m.DurationSeconds,
		&m.SummaryData, &m.SourceMeetingIDs, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create meeting: %w", err)
	}
	return &m, nil
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (*Meeting, error) {
	const q = `
		SELECT id, title, status, language_used, duration_seconds,
		       raw_transcript, summary_data, source_meeting_ids, created_at, updated_at
		FROM meetings WHERE id = $1`

	var m Meeting
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&m.ID, &m.Title, &m.Status, &m.LanguageUsed, &m.DurationSeconds,
		&m.RawTranscript, &m.SummaryData, &m.SourceMeetingIDs, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get meeting: %w", err)
	}
	return &m, nil
}

// List searches title, raw transcript and generated summary (PRD §23).
// ILIKE is adequate for a personal archive; swap in full-text search if the
// database ever grows past it.
func (r *Repository) List(ctx context.Context, query string, page, limit int) (*ListResult, error) {
	const countQ = `
		SELECT count(*) FROM meetings
		WHERE $1 = ''
		   OR title ILIKE '%' || $1 || '%'
		   OR raw_transcript ILIKE '%' || $1 || '%'
		   OR summary_data::text ILIKE '%' || $1 || '%'`

	var total int
	if err := r.pool.QueryRow(ctx, countQ, query).Scan(&total); err != nil {
		return nil, fmt.Errorf("count meetings: %w", err)
	}

	const listQ = `
		SELECT id, title, status, language_used, duration_seconds, created_at,
		       summary_data IS NOT NULL AS has_summary,
		       source_meeting_ids IS NOT NULL AS is_combined
		FROM meetings
		WHERE $1 = ''
		   OR title ILIKE '%' || $1 || '%'
		   OR raw_transcript ILIKE '%' || $1 || '%'
		   OR summary_data::text ILIKE '%' || $1 || '%'
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := r.pool.Query(ctx, listQ, query, limit, (page-1)*limit)
	if err != nil {
		return nil, fmt.Errorf("list meetings: %w", err)
	}
	defer rows.Close()

	items := make([]ListItem, 0, limit)
	for rows.Next() {
		var it ListItem
		if err := rows.Scan(&it.ID, &it.Title, &it.Status, &it.LanguageUsed,
			&it.DurationSeconds, &it.CreatedAt, &it.HasSummary, &it.IsCombined); err != nil {
			return nil, fmt.Errorf("scan meeting: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate meetings: %w", err)
	}

	return &ListResult{Items: items, Page: page, Limit: limit, Total: total}, nil
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, title *string, summary json.RawMessage) (*Meeting, error) {
	const q = `
		UPDATE meetings
		SET title        = COALESCE($2, title),
		    summary_data = COALESCE($3, summary_data)
		WHERE id = $1
		RETURNING id, title, status, language_used, duration_seconds,
		          raw_transcript, summary_data, source_meeting_ids, created_at, updated_at`

	var summaryArg any
	if len(summary) > 0 {
		summaryArg = []byte(summary)
	}

	var m Meeting
	err := r.pool.QueryRow(ctx, q, id, title, summaryArg).Scan(
		&m.ID, &m.Title, &m.Status, &m.LanguageUsed, &m.DurationSeconds,
		&m.RawTranscript, &m.SummaryData, &m.SourceMeetingIDs, &m.CreatedAt, &m.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update meeting: %w", err)
	}
	return &m, nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM meetings WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete meeting: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE meetings SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	return nil
}

func (r *Repository) SetDuration(ctx context.Context, id uuid.UUID, seconds int) error {
	// Never shrink the duration: the client's stop value and the sum of chunk
	// timings can disagree slightly, and the longer one is the truth.
	_, err := r.pool.Exec(ctx,
		`UPDATE meetings SET duration_seconds = GREATEST(duration_seconds, $2) WHERE id = $1`,
		id, seconds)
	if err != nil {
		return fmt.Errorf("set duration: %w", err)
	}
	return nil
}

// Finalize stores the combined transcript and the generated notes together, so
// a meeting is never left holding notes that do not match its transcript.
func (r *Repository) Finalize(ctx context.Context, id uuid.UUID, transcript string, language *string, summary json.RawMessage, status string) error {
	const q = `
		UPDATE meetings
		SET raw_transcript = $2,
		    language_used  = COALESCE($3, language_used),
		    summary_data   = COALESCE($4, summary_data),
		    status         = $5
		WHERE id = $1`

	var summaryArg any
	if len(summary) > 0 {
		summaryArg = []byte(summary)
	}

	_, err := r.pool.Exec(ctx, q, id, transcript, clampLanguage(language), summaryArg, status)
	if err != nil {
		return fmt.Errorf("finalize meeting: %w", err)
	}
	return nil
}

// PlaceholderTitles are the names this app assigns when the user gave none.
// Only these may be replaced by a generated title.
var PlaceholderTitles = []string{"Untitled Meeting", "Gabungan Diskusi"}

// AdoptGeneratedTitle replaces a placeholder title with the model's.
//
// The PRD asks for a generated title, but a title the user typed is theirs and
// must survive every regeneration — so the update is conditional on the stored
// title still being one this app assigned.
func (r *Repository) AdoptGeneratedTitle(ctx context.Context, id uuid.UUID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}

	const q = `UPDATE meetings SET title = $2 WHERE id = $1 AND title = ANY($3)`

	_, err := r.pool.Exec(ctx, q, id, title, PlaceholderTitles)
	if err != nil {
		return fmt.Errorf("adopt generated title: %w", err)
	}
	return nil
}

// StuckInProcessing lists meetings left mid-summarize.
//
// Called at startup only, where the answer is unambiguous: this process was
// not running when these rows were marked, so nothing owns them.
func (r *Repository) StuckInProcessing(ctx context.Context) ([]uuid.UUID, error) {
	const q = `SELECT id FROM meetings WHERE status = $1 ORDER BY created_at`

	rows, err := r.pool.Query(ctx, q, StatusProcessing)
	if err != nil {
		return nil, fmt.Errorf("scan stuck meetings: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan stuck meeting id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SyncTranscript refreshes the stored transcript from the chunks.
//
// raw_transcript is a cached join of transcript_chunks, and the chunks are the
// source of truth. The cache goes stale whenever a chunk lands after the
// meeting was finalized — a late upload, or a finalize that failed part-way
// and left it empty. Nothing edits raw_transcript by hand, so rewriting it
// from the chunks is always a repair.
//
// The empty guard is on the incoming value, not the stored one: a meeting with
// no usable chunks must not have a real transcript blanked.
func (r *Repository) SyncTranscript(ctx context.Context, id uuid.UUID, transcript string) error {
	if strings.TrimSpace(transcript) == "" {
		return nil
	}

	const q = `UPDATE meetings SET raw_transcript = $2 WHERE id = $1`

	_, err := r.pool.Exec(ctx, q, id, transcript)
	if err != nil {
		return fmt.Errorf("sync transcript: %w", err)
	}
	return nil
}

// UpdateSummary replaces only the generated notes and the meeting status.
//
// Separate from Finalize on purpose: a manual re-run must never touch
// raw_transcript. The transcript is the irreplaceable part — audio is already
// gone by then — so regeneration is only ever allowed to write the notes.
func (r *Repository) UpdateSummary(ctx context.Context, id uuid.UUID, summary json.RawMessage, language *string, status string) error {
	const q = `
		UPDATE meetings
		SET summary_data  = COALESCE($2, summary_data),
		    language_used = COALESCE($3, language_used),
		    status        = $4
		WHERE id = $1`

	var summaryArg any
	if len(summary) > 0 {
		summaryArg = []byte(summary)
	}

	_, err := r.pool.Exec(ctx, q, id, summaryArg, clampLanguage(language), status)
	if err != nil {
		return fmt.Errorf("update summary: %w", err)
	}
	return nil
}

// CombinedInput is one source meeting's contribution to a combined meeting.
type CombinedInput struct {
	MeetingID uuid.UUID
	Chunks    []TranscriptChunk
	Duration  int
}

// CreateCombined builds a meeting from several others in one transaction.
//
// The chunks are copied rather than referenced, with their timings shifted so
// the combined meeting reads as one continuous recording. Copies mean deleting
// a source later cannot hollow out the combined meeting — and the sources are
// left completely untouched.
func (r *Repository) CreateCombined(ctx context.Context, title, transcript string, sources []CombinedInput) (*Meeting, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin combine: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ids := make([]uuid.UUID, 0, len(sources))
	total := 0
	for _, s := range sources {
		ids = append(ids, s.MeetingID)
		total += s.Duration
	}

	const insert = `
		INSERT INTO meetings (title, status, duration_seconds, raw_transcript, source_meeting_ids)
		VALUES (COALESCE(NULLIF($1, ''), 'Gabungan Diskusi'), $2, $3, $4, $5)
		RETURNING id, title, status, language_used, duration_seconds,
		          raw_transcript, summary_data, source_meeting_ids, created_at, updated_at`

	var m Meeting
	err = tx.QueryRow(ctx, insert, title, StatusProcessing, total, transcript, ids).Scan(
		&m.ID, &m.Title, &m.Status, &m.LanguageUsed, &m.DurationSeconds,
		&m.RawTranscript, &m.SummaryData, &m.SourceMeetingIDs, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create combined meeting: %w", err)
	}

	const copyChunk = `
		INSERT INTO transcript_chunks
		    (meeting_id, chunk_index, start_time_sec, end_time_sec, transcript_text, status)
		VALUES ($1, $2, $3, $4, $5, $6)`

	index := 0
	offset := 0
	for _, source := range sources {
		for _, c := range source.Chunks {
			// Shift each source's timings past everything before it, so the
			// transcript timestamps run continuously end to end.
			_, err = tx.Exec(ctx, copyChunk, m.ID, index,
				offset+c.StartTimeSec, offset+c.EndTimeSec, c.Text, c.Status)
			if err != nil {
				return nil, fmt.Errorf("copy chunk: %w", err)
			}
			index++
		}
		offset += source.Duration
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit combine: %w", err)
	}
	return &m, nil
}

// --- chunks ---

func (r *Repository) CreateChunk(ctx context.Context, meetingID uuid.UUID, idx, start, end int) (*TranscriptChunk, error) {
	const q = `
		INSERT INTO transcript_chunks (meeting_id, chunk_index, start_time_sec, end_time_sec)
		VALUES ($1, $2, $3, $4)
		RETURNING id, meeting_id, chunk_index, start_time_sec, end_time_sec,
		          transcript_text, status, error_message, created_at, updated_at`

	var c TranscriptChunk
	err := r.pool.QueryRow(ctx, q, meetingID, idx, start, end).Scan(
		&c.ID, &c.MeetingID, &c.ChunkIndex, &c.StartTimeSec, &c.EndTimeSec,
		&c.Text, &c.Status, &c.ErrorMessage, &c.CreatedAt, &c.UpdatedAt,
	)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			// UNIQUE(meeting_id, chunk_index) — a retried upload, not a new chunk.
			return nil, ErrChunkConflict
		case "23503":
			return nil, ErrNotFound
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create chunk: %w", err)
	}
	return &c, nil
}

func (r *Repository) CompleteChunk(ctx context.Context, chunkID uuid.UUID, text string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE transcript_chunks
		 SET transcript_text = $2, status = $3, error_message = NULL
		 WHERE id = $1`,
		chunkID, text, ChunkCompleted)
	if err != nil {
		return fmt.Errorf("complete chunk: %w", err)
	}
	return nil
}

func (r *Repository) FailChunk(ctx context.Context, chunkID uuid.UUID, reason string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE transcript_chunks SET status = $2, error_message = $3 WHERE id = $1`,
		chunkID, ChunkFailed, reason)
	if err != nil {
		return fmt.Errorf("fail chunk: %w", err)
	}
	return nil
}

// Chunks always come back ordered by index — STT finishes out of order, and
// nothing downstream may depend on completion order (PRD §10).
func (r *Repository) Chunks(ctx context.Context, meetingID uuid.UUID) ([]TranscriptChunk, error) {
	const q = `
		SELECT id, meeting_id, chunk_index, start_time_sec, end_time_sec,
		       transcript_text, status, error_message, created_at, updated_at
		FROM transcript_chunks
		WHERE meeting_id = $1
		ORDER BY chunk_index ASC`

	rows, err := r.pool.Query(ctx, q, meetingID)
	if err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}
	defer rows.Close()

	chunks := make([]TranscriptChunk, 0, 16)
	for rows.Next() {
		var c TranscriptChunk
		if err := rows.Scan(&c.ID, &c.MeetingID, &c.ChunkIndex, &c.StartTimeSec,
			&c.EndTimeSec, &c.Text, &c.Status, &c.ErrorMessage,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// PendingChunks counts chunks still being transcribed, so stop knows whether
// there is anything left to wait for.
func (r *Repository) PendingChunks(ctx context.Context, meetingID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM transcript_chunks WHERE meeting_id = $1 AND status = $2`,
		meetingID, ChunkProcessing).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count pending chunks: %w", err)
	}
	return n, nil
}
