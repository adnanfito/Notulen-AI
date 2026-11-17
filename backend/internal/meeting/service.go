package meeting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"notulen-ai/backend/internal/sse"
	"notulen-ai/backend/internal/stt"
	"notulen-ai/backend/internal/summarizer"
	"notulen-ai/backend/internal/worker"
)

type Queue interface {
	Submit(job worker.Job) error
	WaitFor(ctx context.Context, meetingID uuid.UUID) error
}

type Service struct {
	// summarizing holds the meetings a goroutine is actually working on right
	// now. The database status cannot answer this: a restart kills the
	// goroutine but leaves the row at PROCESSING, and trusting that would lock
	// the meeting out of every retry — forever.
	summarizingMu sync.Mutex
	summarizing   map[uuid.UUID]struct{}

	repo       *Repository
	queue      Queue
	broker     *sse.Broker
	summarizer summarizer.Summarizer

	audioDir   string
	keepAudio  bool
	language   string
	stopWait   time.Duration
	summaryTTL time.Duration
}

type ServiceConfig struct {
	AudioDir       string
	KeepAudio      bool
	Language       string
	StopWait       time.Duration
	SummaryTimeout time.Duration
}

func NewService(repo *Repository, queue Queue, broker *sse.Broker, sum summarizer.Summarizer, cfg ServiceConfig) *Service {
	if cfg.StopWait <= 0 {
		cfg.StopWait = 5 * time.Minute
	}
	if cfg.SummaryTimeout <= 0 {
		cfg.SummaryTimeout = 2 * time.Minute
	}
	return &Service{
		summarizing: make(map[uuid.UUID]struct{}),
		repo:        repo,
		queue:       queue,
		broker:      broker,
		summarizer:  sum,
		audioDir:    cfg.AudioDir,
		keepAudio:   cfg.KeepAudio,
		language:    cfg.Language,
		stopWait:    cfg.StopWait,
		summaryTTL:  cfg.SummaryTimeout,
	}
}

func (s *Service) Create(ctx context.Context, title string) (*Meeting, error) {
	m, err := s.repo.Create(ctx, strings.TrimSpace(title))
	if err != nil {
		return nil, err
	}
	slog.Info("meeting_created", "meeting_id", m.ID)
	return m, nil
}

func (s *Service) List(ctx context.Context, query string, page, limit int) (*ListResult, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return s.repo.List(ctx, strings.TrimSpace(query), page, limit)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Detail, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	chunks, err := s.repo.Chunks(ctx, id)
	if err != nil {
		return nil, err
	}
	return &Detail{Meeting: *m, Chunks: chunks}, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateRequest) (*Meeting, error) {
	var title *string
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		if trimmed == "" {
			return nil, errors.New("title cannot be empty")
		}
		title = &trimmed
	}

	// Validate edited notes before they reach the database, so a malformed
	// client edit can never corrupt stored notes.
	if len(req.SummaryData) > 0 {
		var probe summarizer.Summary
		if err := json.Unmarshal(req.SummaryData, &probe); err != nil {
			return nil, fmt.Errorf("summary_data is not valid meeting notes: %w", err)
		}
	}

	return s.repo.Update(ctx, id, title, req.SummaryData)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	// Deleting a meeting mid-recording leaves chunks the worker will never
	// reach, so the directory goes with the row.
	s.removeAudioDir(id)
	return nil
}

// AddChunk stores the upload, records the chunk, and hands it to the worker.
// It deliberately does no transcription: the endpoint must return immediately
// so recording is never blocked (PRD §9).
func (s *Service) AddChunk(ctx context.Context, meetingID uuid.UUID, idx, start, end int, filename string, body io.Reader) (*TranscriptChunk, error) {
	if _, err := s.repo.Get(ctx, meetingID); err != nil {
		return nil, err
	}

	chunk, err := s.repo.CreateChunk(ctx, meetingID, idx, start, end)
	if err != nil {
		return nil, err
	}

	path, err := s.storeAudio(meetingID, idx, filename, body)
	if err != nil {
		_ = s.repo.FailChunk(ctx, chunk.ID, "could not store audio: "+err.Error())
		return nil, err
	}

	job := worker.Job{
		MeetingID:  meetingID,
		ChunkID:    chunk.ID,
		ChunkIndex: idx,
		AudioPath:  path,
		Language:   s.language,
	}

	if err := s.queue.Submit(job); err != nil {
		_ = os.Remove(path)
		_ = s.repo.FailChunk(ctx, chunk.ID, err.Error())
		s.publishChunkFailed(meetingID, idx, err.Error())
		return nil, err
	}

	// Duration grows as chunks arrive, so a meeting shows a sane length even
	// if the browser never reaches the stop call.
	_ = s.repo.SetDuration(ctx, meetingID, end)

	slog.Info("chunk_uploaded", "meeting_id", meetingID, "chunk_index", idx)
	s.broker.Publish(meetingID, sse.EventChunkProcessing, map[string]any{
		"chunk_index": idx,
		"status":      ChunkProcessing,
	})

	return chunk, nil
}

func (s *Service) storeAudio(meetingID uuid.UUID, idx int, filename string, body io.Reader) (string, error) {
	dir := filepath.Join(s.audioDir, meetingID.String())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create audio dir: %w", err)
	}

	// The client's filename is never used as a path component (PRD §31) —
	// only a conservative extension is taken from it.
	path := filepath.Join(dir, fmt.Sprintf("%06d%s", idx, safeExt(filename)))

	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create audio file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, body); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write audio: %w", err)
	}
	return path, nil
}

func safeExt(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".webm", ".ogg", ".opus", ".wav", ".mp4", ".m4a", ".mp3", ".flac":
		return ext
	default:
		return ".webm"
	}
}

// --- worker.Handler ---

func (s *Service) OnTranscribed(ctx context.Context, job worker.Job, result *stt.Result) {
	text := strings.TrimSpace(result.Text)

	if err := s.repo.CompleteChunk(ctx, job.ChunkID, text); err != nil {
		slog.Error("chunk_persist_failed", "chunk_id", job.ChunkID, "error", err)
		return
	}

	slog.Info("transcription_completed",
		"meeting_id", job.MeetingID, "chunk_index", job.ChunkIndex,
		"language", result.Language, "chars", len(text))

	s.broker.Publish(job.MeetingID, sse.EventTranscript, map[string]any{
		"chunk_index": job.ChunkIndex,
		"text":        text,
		"language":    result.Language,
		"status":      ChunkCompleted,
	})
}

func (s *Service) OnFailed(ctx context.Context, job worker.Job, cause error) {
	reason := "transcription failed"
	if cause != nil {
		reason = cause.Error()
	}

	if err := s.repo.FailChunk(ctx, job.ChunkID, reason); err != nil {
		slog.Error("chunk_fail_persist_failed", "chunk_id", job.ChunkID, "error", err)
	}
	s.publishChunkFailed(job.MeetingID, job.ChunkIndex, reason)
}

func (s *Service) publishChunkFailed(meetingID uuid.UUID, idx int, reason string) {
	s.broker.Publish(meetingID, sse.EventError, map[string]any{
		"chunk_index": idx,
		"status":      ChunkFailed,
		"message":     reason,
	})
}

// --- stop & summarize ---

// Stop marks the meeting as processing and finishes it in the background.
// The HTTP call returns immediately; the browser watches SSE for completion
// (PRD §15).
func (s *Service) Stop(ctx context.Context, id uuid.UUID, durationSeconds int) (*Meeting, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if m.Status == StatusProcessing || m.Status == StatusCompleted {
		return m, nil
	}

	if durationSeconds > 0 {
		_ = s.repo.SetDuration(ctx, id, durationSeconds)
	}
	if err := s.repo.SetStatus(ctx, id, StatusProcessing); err != nil {
		return nil, err
	}

	s.broker.Publish(id, sse.EventMeetingStatus, map[string]any{"status": StatusProcessing})
	slog.Info("meeting_stopping", "meeting_id", id)

	// Detached from the request context on purpose: finishing must not be
	// cancelled because the browser navigated away.
	go s.finish(context.WithoutCancel(ctx), id)

	m.Status = StatusProcessing
	return m, nil
}

func (s *Service) finish(ctx context.Context, id uuid.UUID) {
	waitCtx, cancel := context.WithTimeout(ctx, s.stopWait)
	defer cancel()

	if err := s.queue.WaitFor(waitCtx, id); err != nil {
		// Outstanding chunks timed out. Carry on with what transcribed —
		// partial notes beat no notes (PRD §15).
		slog.Warn("stop_wait_timeout", "meeting_id", id, "error", err)
	}

	chunks, err := s.repo.Chunks(ctx, id)
	if err != nil {
		slog.Error("stop_chunks_failed", "meeting_id", id, "error", err)
		s.markFailed(ctx, id, "could not read transcript chunks")
		return
	}

	transcript, failed := combine(chunks)

	summary, language, generatedTitle, summaryErr := s.generateSummary(ctx, id, transcript)

	status := StatusCompleted
	if summaryErr != nil {
		slog.Error("summary_failed", "meeting_id", id, "error", summaryErr)
		// A failed summary must never destroy the transcript (PRD §32), so
		// the meeting still completes and the transcript is still stored.
		if errors.Is(summaryErr, summarizer.ErrTranscriptTooShort) {
			slog.Info("summary_skipped", "meeting_id", id, "reason", "transcript_too_short")
		}
	}

	if err := s.repo.Finalize(ctx, id, transcript, language, summary, status); err != nil {
		slog.Error("finalize_failed", "meeting_id", id, "error", err)
		s.markFailed(ctx, id, "could not store meeting results")
		return
	}

	// Every chunk's audio was already removed as it finished transcribing;
	// this clears the now-empty per-meeting directory so they do not pile up
	// one per meeting forever.
	s.removeAudioDir(id)

	if err := s.repo.AdoptGeneratedTitle(ctx, id, generatedTitle); err != nil {
		slog.Warn("title_adopt_failed", "meeting_id", id, "error", err)
	}

	slog.Info("meeting_completed",
		"meeting_id", id, "chars", len(transcript),
		"failed_chunks", failed, "has_summary", summary != nil)

	s.broker.Publish(id, sse.EventMeetingStatus, map[string]any{
		"status":        status,
		"failed_chunks": failed,
		"has_summary":   summary != nil,
	})
}

// Errors a manual re-run can hit that the user needs explained, not logged.
var (
	ErrStillRecording = errors.New("meeting is still recording")
	ErrAlreadyRunning = errors.New("notes are already being generated")
	ErrNoTranscript   = errors.New("meeting has no transcript to summarize")
	ErrNoSummarizer   = errors.New("no LLM is configured")

	ErrTooFewMeetings  = errors.New("pick at least two meetings to combine")
	ErrSourceRecording = errors.New("one of the meetings is still recording")
)

// MaxCombinedMeetings bounds a single combine. Well past any real "I stopped
// the recording by accident" case, and it keeps one request from assembling a
// transcript nothing can summarize.
const MaxCombinedMeetings = 20

// claimSummary reserves a meeting for summarization. False means a goroutine
// in this process already holds it.
func (s *Service) claimSummary(id uuid.UUID) bool {
	s.summarizingMu.Lock()
	defer s.summarizingMu.Unlock()

	if _, taken := s.summarizing[id]; taken {
		return false
	}
	s.summarizing[id] = struct{}{}
	return true
}

func (s *Service) isSummarizing(id uuid.UUID) bool {
	s.summarizingMu.Lock()
	defer s.summarizingMu.Unlock()
	_, running := s.summarizing[id]
	return running
}

func (s *Service) releaseSummary(id uuid.UUID) {
	s.summarizingMu.Lock()
	defer s.summarizingMu.Unlock()
	delete(s.summarizing, id)
}

// RecoverStuckSummaries finishes meetings left mid-summarize by a restart.
//
// Summarization runs in a goroutine, so a restart abandons it and the row sits
// at PROCESSING with nothing left to move it. Any such meeting found at
// startup is orphaned by definition — this process was not running when it was
// marked, so no goroutine can own it.
func (s *Service) RecoverStuckSummaries(ctx context.Context) {
	if s.summarizer == nil {
		return
	}

	ids, err := s.repo.StuckInProcessing(ctx)
	if err != nil {
		slog.Error("summary_recovery_scan_failed", "error", err)
		return
	}
	if len(ids) == 0 {
		return
	}

	slog.Info("summary_recovery_started", "meetings", len(ids))

	// Sequentially: these run through the LLM, and a burst of them on startup
	// would collide with whatever the user is doing right now.
	go func() {
		for _, id := range ids {
			m, err := s.repo.Get(ctx, id)
			if err != nil {
				continue
			}

			transcript, err := s.transcriptFor(ctx, m)
			if err != nil || strings.TrimSpace(transcript) == "" {
				// Nothing to summarize. Leave it COMPLETED rather than stuck,
				// so the page stops claiming work is under way.
				_ = s.repo.SetStatus(ctx, id, StatusCompleted)
				s.broker.Publish(id, sse.EventMeetingStatus, map[string]any{"status": StatusCompleted})
				continue
			}

			slog.Info("summary_recovery_resuming", "meeting_id", id)
			s.rerunSummary(ctx, id, transcript, StatusCompleted)
		}
	}()
}

// Combine assembles several meetings into one and summarizes them together.
//
// A single discussion often ends up split across rows — the recording is
// stopped by accident, or the browser is closed, and the rest is captured as a
// new meeting. Summarizing each fragment separately gives several partial sets
// of notes and no account of the discussion as a whole.
//
// The sources are never modified: this produces a new meeting holding copies.
func (s *Service) Combine(ctx context.Context, ids []uuid.UUID, title string) (*Meeting, error) {
	unique := dedupe(ids)
	if len(unique) < 2 {
		return nil, ErrTooFewMeetings
	}
	if len(unique) > MaxCombinedMeetings {
		return nil, fmt.Errorf("%w: at most %d", ErrTooFewMeetings, MaxCombinedMeetings)
	}
	if s.summarizer == nil {
		return nil, ErrNoSummarizer
	}

	type loaded struct {
		meeting *Meeting
		chunks  []TranscriptChunk
	}

	items := make([]loaded, 0, len(unique))
	for _, id := range unique {
		m, err := s.repo.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		// A meeting still being recorded has no settled end, and combining it
		// would silently freeze a snapshot of a moving target.
		if m.Status == StatusRecording || m.Status == StatusProcessing {
			return nil, ErrSourceRecording
		}

		chunks, err := s.repo.Chunks(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, loaded{meeting: m, chunks: chunks})
	}

	// Chronological, so the combined transcript follows the discussion even if
	// the caller listed them in another order.
	sort.Slice(items, func(a, b int) bool {
		return items[a].meeting.CreatedAt.Before(items[b].meeting.CreatedAt)
	})

	var transcript strings.Builder
	sources := make([]CombinedInput, 0, len(items))

	for _, item := range items {
		text, _ := combine(item.chunks)
		if strings.TrimSpace(text) == "" && item.meeting.RawTranscript != nil {
			text = *item.meeting.RawTranscript
		}
		if strings.TrimSpace(text) != "" {
			if transcript.Len() > 0 {
				// A blank line between meetings, so the model reads them as
				// consecutive stretches of one discussion.
				transcript.WriteString("\n\n")
			}
			transcript.WriteString(strings.TrimSpace(text))
		}

		duration := item.meeting.DurationSeconds
		if duration <= 0 {
			for _, c := range item.chunks {
				if c.EndTimeSec > duration {
					duration = c.EndTimeSec
				}
			}
		}

		sources = append(sources, CombinedInput{
			MeetingID: item.meeting.ID,
			Chunks:    item.chunks,
			Duration:  duration,
		})
	}

	if strings.TrimSpace(transcript.String()) == "" {
		return nil, ErrNoTranscript
	}

	combined, err := s.repo.CreateCombined(ctx, strings.TrimSpace(title), transcript.String(), sources)
	if err != nil {
		return nil, err
	}

	slog.Info("meetings_combined",
		"meeting_id", combined.ID, "sources", len(sources), "chars", transcript.Len())

	// Detached: the notes must finish even if the browser navigates away.
	go s.rerunSummary(context.WithoutCancel(ctx), combined.ID, transcript.String(), StatusCompleted)

	return combined, nil
}

// dedupe keeps the caller's order while dropping repeats, so the same meeting
// cannot be folded in twice.
func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// Resummarize regenerates the notes for a meeting that already has a
// transcript, on the user's command.
//
// Notes generation is the one step that depends on a third party, so it is the
// one step that fails for reasons the user can simply wait out — an overloaded
// model, a quota, a network blip. Without this they would have to re-record a
// meeting whose transcript is sitting right there.
func (s *Service) Resummarize(ctx context.Context, id uuid.UUID) (*Meeting, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if m.Status == StatusRecording {
		return nil, ErrStillRecording
	}
	// Only a goroutine in this process counts as "already running". A row left
	// at PROCESSING by a restart must stay retryable.
	if s.isSummarizing(id) {
		return nil, ErrAlreadyRunning
	}

	if s.summarizer == nil {
		return nil, ErrNoSummarizer
	}

	transcript, err := s.transcriptFor(ctx, m)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(transcript) == "" {
		return nil, ErrNoTranscript
	}

	// A meeting that failed at the finalize step still has its chunks, so a
	// successful re-run legitimately completes it.
	previous := m.Status
	if previous == StatusFailed {
		previous = StatusCompleted
	}
	if err := s.repo.SetStatus(ctx, id, StatusProcessing); err != nil {
		return nil, err
	}

	s.broker.Publish(id, sse.EventMeetingStatus, map[string]any{"status": StatusProcessing})
	slog.Info("summary_rerun_requested", "meeting_id", id)

	// Detached: regeneration must survive the user navigating away.
	go s.rerunSummary(context.WithoutCancel(ctx), id, transcript, previous)

	m.Status = StatusProcessing
	return m, nil
}

// transcriptFor rebuilds the transcript from the chunks, which are the source
// of truth, and falls back to the stored copy only when there are no usable
// chunks left.
//
// Preferring the stored copy would re-summarize a stale transcript whenever a
// chunk landed after the meeting was finalized — the notes would then be
// missing the end of the meeting with nothing to show for it.
func (s *Service) transcriptFor(ctx context.Context, m *Meeting) (string, error) {
	chunks, err := s.repo.Chunks(ctx, m.ID)
	if err != nil {
		return "", err
	}

	if transcript, _ := combine(chunks); strings.TrimSpace(transcript) != "" {
		return transcript, nil
	}

	if m.RawTranscript != nil {
		return *m.RawTranscript, nil
	}
	return "", nil
}

func (s *Service) rerunSummary(ctx context.Context, id uuid.UUID, transcript, previousStatus string) {
	if !s.claimSummary(id) {
		slog.Warn("summary_already_running", "meeting_id", id)
		return
	}
	defer s.releaseSummary(id)

	summary, language, generatedTitle, err := s.generateSummary(ctx, id, transcript)
	if err != nil {
		slog.Error("summary_rerun_failed", "meeting_id", id, "error", err)

		// Put the meeting back exactly as it was. A failed re-run is not a
		// failed meeting: the transcript, and any notes already stored, are
		// untouched (PRD §32).
		if restoreErr := s.repo.SetStatus(ctx, id, previousStatus); restoreErr != nil {
			slog.Error("summary_rerun_status_restore_failed", "meeting_id", id, "error", restoreErr)
		}

		s.broker.Publish(id, sse.EventError, map[string]any{
			"scope":   "summary",
			"status":  previousStatus,
			"message": userFacingSummaryError(err),
		})
		s.broker.Publish(id, sse.EventMeetingStatus, map[string]any{"status": previousStatus})
		return
	}

	// Bring the stored transcript back in step with the chunks, which may have
	// moved on since the meeting was finalized.
	if err := s.repo.SyncTranscript(ctx, id, transcript); err != nil {
		slog.Warn("transcript_sync_failed", "meeting_id", id, "error", err)
	}

	// UpdateSummary, not Finalize: regeneration never rewrites the transcript.
	if err := s.repo.UpdateSummary(ctx, id, summary, language, StatusCompleted); err != nil {
		slog.Error("summary_rerun_persist_failed", "meeting_id", id, "error", err)
		s.markFailed(ctx, id, "could not store the regenerated notes")
		return
	}

	if err := s.repo.AdoptGeneratedTitle(ctx, id, generatedTitle); err != nil {
		slog.Warn("title_adopt_failed", "meeting_id", id, "error", err)
	}

	slog.Info("summary_rerun_completed", "meeting_id", id)
	s.broker.Publish(id, sse.EventMeetingStatus, map[string]any{
		"status":      StatusCompleted,
		"has_summary": true,
	})
}

// userFacingSummaryError turns provider noise into something worth reading.
func userFacingSummaryError(err error) string {
	switch {
	case errors.Is(err, summarizer.ErrTranscriptTooShort):
		return "Transkripnya terlalu pendek untuk dibuatkan notulen."
	case strings.Contains(err.Error(), "all models exhausted"):
		return "Semua model AI sedang sibuk. Coba lagi beberapa saat."
	case strings.Contains(err.Error(), "API key"):
		return "API key LLM belum benar. Periksa LLM_API_KEY."
	default:
		return "Pembuatan notulen gagal. Coba lagi."
	}
}

func (s *Service) generateSummary(ctx context.Context, id uuid.UUID, transcript string) (json.RawMessage, *string, string, error) {
	if s.summarizer == nil {
		return nil, nil, "", errors.New("no summarizer configured")
	}

	slog.Info("summary_started", "meeting_id", id)

	sumCtx, cancel := context.WithTimeout(ctx, s.summaryTTL)
	defer cancel()

	result, err := s.summarizer.Summarize(sumCtx, transcript)
	if err != nil {
		return nil, nil, "", err
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, nil, "", err
	}

	slog.Info("summary_completed", "meeting_id", id,
		"action_items", len(result.ActionItems), "decisions", len(result.KeyDecisions))

	var language *string
	if result.LanguageUsed != "" {
		language = &result.LanguageUsed
	}
	return encoded, language, result.Title, nil
}

func (s *Service) markFailed(ctx context.Context, id uuid.UUID, message string) {
	_ = s.repo.SetStatus(ctx, id, StatusFailed)
	s.removeAudioDir(id)
	s.broker.Publish(id, sse.EventMeetingStatus, map[string]any{
		"status":  StatusFailed,
		"message": message,
	})
}

// removeAudioDir deletes a meeting's audio directory and anything still in it.
//
// Individual chunks are removed by the worker as soon as they transcribe, so
// this is normally deleting an empty directory. It still uses RemoveAll, to
// catch chunks orphaned by a failure between upload and transcription.
func (s *Service) removeAudioDir(id uuid.UUID) {
	if s.keepAudio {
		return
	}
	dir := filepath.Join(s.audioDir, id.String())
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("audio_dir_cleanup_failed", "meeting_id", id, "error", err)
	}
}

// SweepAudioDir clears anything left in the audio directory at startup.
//
// The transcription queue is in memory, so a restart abandons every job that
// had not run yet — their audio would otherwise sit on disk forever with
// nothing left to process it (PRD §31).
func SweepAudioDir(dir string, keepAudio bool) {
	if keepAudio {
		slog.Info("audio_sweep_skipped", "reason", "KEEP_AUDIO is set")
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("audio_sweep_failed", "dir", dir, "error", err)
		}
		return
	}

	removed := 0
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			slog.Warn("audio_sweep_entry_failed", "entry", entry.Name(), "error", err)
			continue
		}
		removed++
	}

	if removed > 0 {
		slog.Info("audio_swept", "entries", removed)
	}
}

// combine joins completed chunks in index order and reports how many failed.
// Chunks are already ordered by the repository; completion order is never
// trusted (PRD §10).
func combine(chunks []TranscriptChunk) (string, int) {
	var b strings.Builder
	failed := 0

	for _, c := range chunks {
		if c.Status == ChunkFailed {
			failed++
			continue
		}
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(text)
	}

	return b.String(), failed
}

// Broker exposes the SSE broker for the handler layer.
func (s *Service) Broker() *sse.Broker { return s.broker }
