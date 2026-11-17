// Package worker runs transcription off the request path.
//
// The queue is in memory, which the PRD accepts for V1 (§14) on one condition:
// jobs are lost if the backend restarts mid-meeting. That trade is documented
// in the README, and the transcripts already written to PostgreSQL survive —
// only the not-yet-transcribed chunks are lost.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"notulen-ai/backend/internal/stt"
)

// Job is one audio chunk waiting to be transcribed.
type Job struct {
	MeetingID  uuid.UUID
	ChunkID    uuid.UUID
	ChunkIndex int
	AudioPath  string
	Language   string
}

// Handler receives the outcome of a job. Implemented by the meeting service.
type Handler interface {
	OnTranscribed(ctx context.Context, job Job, result *stt.Result)
	OnFailed(ctx context.Context, job Job, err error)
}

type Config struct {
	MaxRetries int
	QueueSize  int
	JobTimeout time.Duration
	KeepAudio  bool
	RetryDelay time.Duration
}

type Queue struct {
	jobs    chan Job
	client  *stt.Client
	handler Handler
	cfg     Config

	wg      sync.WaitGroup
	closing sync.Once

	// inFlight tracks queued + running jobs so stop can wait for a real
	// number rather than guessing.
	mu       sync.Mutex
	inFlight map[uuid.UUID]int
	waiters  map[uuid.UUID][]chan struct{}
}

func New(client *stt.Client, handler Handler, cfg Config) *Queue {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 256
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = 10 * time.Minute
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 2 * time.Second
	}

	return &Queue{
		jobs:     make(chan Job, cfg.QueueSize),
		client:   client,
		handler:  handler,
		cfg:      cfg,
		inFlight: make(map[uuid.UUID]int),
		waiters:  make(map[uuid.UUID][]chan struct{}),
	}
}

// Start launches a single worker.
//
// One worker, deliberately: the STT service serializes on one GPU anyway, so
// extra workers would only queue up inside Python and make timeouts harder to
// reason about (PRD §5, §39).
func (q *Queue) Start(ctx context.Context) {
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		for job := range q.jobs {
			q.process(ctx, job)
			q.done(job.MeetingID)
		}
	}()
}

var ErrQueueFull = errors.New("transcription queue is full")

// Submit enqueues a chunk. It never blocks the HTTP handler: the upload
// endpoint must return immediately (PRD §9).
func (q *Queue) Submit(job Job) error {
	q.mu.Lock()
	q.inFlight[job.MeetingID]++
	q.mu.Unlock()

	select {
	case q.jobs <- job:
		return nil
	default:
		q.done(job.MeetingID)
		return ErrQueueFull
	}
}

func (q *Queue) done(meetingID uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.inFlight[meetingID]--
	if q.inFlight[meetingID] > 0 {
		return
	}

	delete(q.inFlight, meetingID)
	for _, waiter := range q.waiters[meetingID] {
		close(waiter)
	}
	delete(q.waiters, meetingID)
}

// WaitFor blocks until every queued chunk for a meeting has been handled, or
// the context expires. Used by stop before combining the transcript.
func (q *Queue) WaitFor(ctx context.Context, meetingID uuid.UUID) error {
	q.mu.Lock()
	if q.inFlight[meetingID] == 0 {
		q.mu.Unlock()
		return nil
	}
	waiter := make(chan struct{})
	q.waiters[meetingID] = append(q.waiters[meetingID], waiter)
	q.mu.Unlock()

	select {
	case <-waiter:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *Queue) process(ctx context.Context, job Job) {
	defer q.cleanup(job)

	var lastErr error

	for attempt := 0; attempt <= q.cfg.MaxRetries; attempt++ {
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}

		if attempt > 0 {
			slog.Warn("transcription_retry",
				"meeting_id", job.MeetingID, "chunk_index", job.ChunkIndex,
				"attempt", attempt, "of", q.cfg.MaxRetries)

			select {
			case <-ctx.Done():
				lastErr = ctx.Err()
				continue
			case <-time.After(q.cfg.RetryDelay):
			}
		}

		jobCtx, cancel := context.WithTimeout(ctx, q.cfg.JobTimeout)
		result, err := q.client.Transcribe(jobCtx, job.AudioPath, job.Language, "")
		cancel()

		if err == nil {
			q.handler.OnTranscribed(ctx, job, result)
			return
		}

		lastErr = err

		// Undecodable audio will still be undecodable next time — retrying
		// only burns the GPU and delays the rest of the meeting.
		if errors.Is(err, stt.ErrInvalidAudio) {
			slog.Warn("transcription_unretryable",
				"meeting_id", job.MeetingID, "chunk_index", job.ChunkIndex, "error", err)
			break
		}
	}

	slog.Error("transcription_failed",
		"meeting_id", job.MeetingID, "chunk_index", job.ChunkIndex, "error", lastErr)
	q.handler.OnFailed(ctx, job, lastErr)
}

// cleanup removes the temporary audio once the chunk is finished with, whether
// it succeeded or not (PRD §31).
func (q *Queue) cleanup(job Job) {
	if q.cfg.KeepAudio {
		return
	}
	if err := os.Remove(job.AudioPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("audio_cleanup_failed", "chunk_index", job.ChunkIndex, "error", err)
	}
}

// Shutdown stops accepting work and waits for the in-flight job to finish.
func (q *Queue) Shutdown() {
	q.closing.Do(func() { close(q.jobs) })
	q.wg.Wait()
}
