package meeting

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"notulen-ai/backend/internal/httpx"
	"notulen-ai/backend/internal/worker"
)

type Handler struct {
	svc       *Service
	heartbeat time.Duration
}

func NewHandler(svc *Service, heartbeat time.Duration) *Handler {
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	return &Handler{svc: svc, heartbeat: heartbeat}
}

func (h *Handler) Register(r *gin.RouterGroup) {
	g := r.Group("/meetings")
	g.POST("", h.Create)
	g.GET("", h.List)
	g.POST("/combine", h.Combine)
	g.GET("/:id", h.Get)
	g.PATCH("/:id", h.Update)
	g.DELETE("/:id", h.Delete)
	g.POST("/:id/chunks", h.UploadChunk)
	g.POST("/:id/stop", h.Stop)
	g.POST("/:id/summarize", h.Resummarize)
	g.GET("/:id/stream", h.Stream)
}

func (h *Handler) Create(c *gin.Context) {
	var req CreateRequest
	// A body is optional: creating an untitled meeting is the common case.
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, "Invalid request body.")
			return
		}
	}

	m, err := h.svc.Create(c.Request.Context(), req.Title)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.CodeInternalError, "Could not create meeting.")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"meeting_id": m.ID,
		"title":      m.Title,
		"status":     m.Status,
		"created_at": m.CreatedAt,
	})
}

func (h *Handler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	result, err := h.svc.List(c.Request.Context(), c.Query("q"), page, limit)
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, httpx.CodeInternalError, "Could not list meetings.")
		return
	}
	c.JSON(http.StatusOK, result)
}

// Combine assembles several meetings into one set of notes, for a discussion
// that ended up split across recordings.
func (h *Handler) Combine(c *gin.Context) {
	var req CombineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, "Invalid request body.")
		return
	}

	m, err := h.svc.Combine(c.Request.Context(), req.MeetingIDs, req.Title)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		respondDomainError(c, err)
		return
	case errors.Is(err, ErrTooFewMeetings):
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest,
			"Pilih minimal dua diskusi untuk digabungkan.")
		return
	case errors.Is(err, ErrSourceRecording):
		httpx.Fail(c, http.StatusConflict, httpx.CodeInvalidRequest,
			"Ada diskusi yang masih berjalan. Selesaikan dulu sebelum menggabungkan.")
		return
	case errors.Is(err, ErrNoTranscript):
		httpx.Fail(c, http.StatusUnprocessableEntity, httpx.CodeInvalidRequest,
			"Diskusi yang dipilih tidak punya transkrip.")
		return
	case errors.Is(err, ErrNoSummarizer):
		httpx.Fail(c, http.StatusServiceUnavailable, httpx.CodeSummaryFailed,
			"LLM belum dikonfigurasi. Isi LLM_API_KEY lalu restart backend.")
		return
	default:
		httpx.Fail(c, http.StatusInternalServerError, httpx.CodeInternalError,
			"Tidak bisa menggabungkan diskusi.")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"meeting_id": m.ID,
		"title":      m.Title,
		"status":     m.Status,
		"sources":    len(m.SourceMeetingIDs),
	})
}

func (h *Handler) Get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	detail, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) Update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, "Invalid request body.")
		return
	}

	m, err := h.svc.Update(c.Request.Context(), id, req)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respondDomainError(c, err)
			return
		}
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *Handler) Delete(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		respondDomainError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) UploadChunk(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	index, err := requireInt(c, "chunk_index")
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, err.Error())
		return
	}
	start, err := requireInt(c, "start_time_sec")
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, err.Error())
		return
	}
	end, err := requireInt(c, "end_time_sec")
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, err.Error())
		return
	}
	if index < 0 || start < 0 || end < start {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, "Chunk timings are inconsistent.")
		return
	}

	header, err := c.FormFile("audio_file")
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidAudio, "Missing audio_file.")
		return
	}
	if header.Size == 0 {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidAudio, "Audio file is empty.")
		return
	}

	file, err := header.Open()
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidAudio, "Could not read audio_file.")
		return
	}
	defer file.Close()

	chunk, err := h.svc.AddChunk(c.Request.Context(), id, index, start, end, header.Filename, io.Reader(file))
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		respondDomainError(c, err)
		return
	case errors.Is(err, ErrChunkConflict):
		// The browser retried an upload that already landed. Idempotent by
		// design (PRD §24) — say so rather than creating a duplicate.
		httpx.Fail(c, http.StatusConflict, httpx.CodeChunkAlreadyExists, "Chunk already received.")
		return
	case errors.Is(err, worker.ErrQueueFull):
		httpx.Fail(c, http.StatusServiceUnavailable, httpx.CodeSTTUnavailable, "Transcription queue is full.")
		return
	default:
		httpx.Fail(c, http.StatusInternalServerError, httpx.CodeInternalError, "Could not accept chunk.")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"chunk_id":    chunk.ID,
		"chunk_index": chunk.ChunkIndex,
		"status":      chunk.Status,
	})
}

func (h *Handler) Stop(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	var req StopRequest
	if c.Request.ContentLength > 0 {
		_ = c.ShouldBindJSON(&req)
	}

	m, err := h.svc.Stop(c.Request.Context(), id, req.DurationSeconds)
	if err != nil {
		respondDomainError(c, err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"meeting_id": m.ID,
		"status":     m.Status,
	})
}

// Resummarize regenerates the notes on demand, for when the LLM was busy the
// first time round.
func (h *Handler) Resummarize(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	m, err := h.svc.Resummarize(c.Request.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		respondDomainError(c, err)
		return
	case errors.Is(err, ErrAlreadyRunning):
		httpx.Fail(c, http.StatusConflict, httpx.CodeInvalidRequest,
			"Notulen sedang dibuat. Tunggu sampai selesai.")
		return
	case errors.Is(err, ErrStillRecording):
		httpx.Fail(c, http.StatusConflict, httpx.CodeInvalidRequest,
			"Rapat masih berjalan. Hentikan rekaman dulu.")
		return
	case errors.Is(err, ErrNoTranscript):
		httpx.Fail(c, http.StatusUnprocessableEntity, httpx.CodeInvalidRequest,
			"Rapat ini tidak punya transkrip untuk diringkas.")
		return
	case errors.Is(err, ErrNoSummarizer):
		httpx.Fail(c, http.StatusServiceUnavailable, httpx.CodeSummaryFailed,
			"LLM belum dikonfigurasi. Isi LLM_API_KEY lalu restart backend.")
		return
	default:
		httpx.Fail(c, http.StatusInternalServerError, httpx.CodeInternalError,
			"Tidak bisa memulai pembuatan notulen.")
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"meeting_id": m.ID,
		"status":     m.Status,
	})
}

// Stream is the live transcript channel (PRD §15, §24).
func (h *Handler) Stream(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	if _, err := h.svc.Get(c.Request.Context(), id); err != nil {
		respondDomainError(c, err)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	// Tell nginx not to buffer, or events arrive in bursts at the end.
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		httpx.Fail(c, http.StatusInternalServerError, httpx.CodeInternalError, "Streaming unsupported.")
		return
	}

	events, unsubscribe := h.svc.Broker().Subscribe(id)
	defer unsubscribe()

	// An immediate comment opens the stream so the browser's EventSource
	// fires onopen without waiting for the first real event.
	fmt.Fprint(c.Writer, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case event, open := <-events:
			if !open {
				return
			}
			fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Name, event.Data)
			flusher.Flush()

		case <-ticker.C:
			// Keeps proxies and idle-connection timeouts from killing a
			// stream during a quiet stretch of the meeting (PRD §15).
			fmt.Fprint(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// --- helpers ---

func parseID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, httpx.CodeInvalidRequest, "Invalid meeting id.")
		return uuid.Nil, false
	}
	return id, true
}

func requireInt(c *gin.Context, field string) (int, error) {
	raw := c.PostForm(field)
	if raw == "" {
		return 0, fmt.Errorf("%s is required", field)
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", field)
	}
	return value, nil
}

func respondDomainError(c *gin.Context, err error) {
	if errors.Is(err, ErrNotFound) {
		httpx.Fail(c, http.StatusNotFound, httpx.CodeMeetingNotFound, "Meeting not found.")
		return
	}
	httpx.Fail(c, http.StatusInternalServerError, httpx.CodeInternalError, "Something went wrong.")
}
