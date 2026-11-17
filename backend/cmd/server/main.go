// Command server is the notulen-ai API gateway.
//
// It is the only service the frontend talks to: the browser never reaches the
// STT service or the database directly (PRD §3).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"notulen-ai/backend/internal/config"
	"notulen-ai/backend/internal/database"
	"notulen-ai/backend/internal/health"
	"notulen-ai/backend/internal/meeting"
	"notulen-ai/backend/internal/sse"
	"notulen-ai/backend/internal/stt"
	"notulen-ai/backend/internal/summarizer"
	"notulen-ai/backend/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("server_fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.Info("config_loaded", "config", cfg.Redacted())

	// Cancelled on SIGINT/SIGTERM so shutdown unwinds in one place.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	slog.Info("database_connected")

	if err := os.MkdirAll(cfg.AudioTempDir, 0o750); err != nil {
		return err
	}
	// A restart abandons every queued transcription job, so anything still on
	// disk has nothing left to process it.
	meeting.SweepAudioDir(cfg.AudioTempDir, cfg.KeepAudio)

	sttClient := stt.NewClient(cfg.STTURL, cfg.STTTimeout)
	broker := sse.NewBroker()
	repo := meeting.NewRepository(db.Pool)

	sum := buildSummarizer(cfg)

	// The service implements worker.Handler, and the queue is one of its
	// dependencies — so the queue is built first with a nil handler slot and
	// wired after. Constructing the service first would be circular.
	var queue *worker.Queue
	svc := meeting.NewService(repo, queueRef{&queue}, broker, sum, meeting.ServiceConfig{
		AudioDir:       cfg.AudioTempDir,
		KeepAudio:      cfg.KeepAudio,
		Language:       cfg.WhisperLanguage,
		StopWait:       cfg.StopWaitTimeout,
		SummaryTimeout: cfg.SummaryTimeout,
	})

	queue = worker.New(sttClient, svc, worker.Config{
		MaxRetries: cfg.STTMaxRetries,
		JobTimeout: cfg.STTTimeout,
		KeepAudio:  cfg.KeepAudio,
	})
	queue.Start(ctx)
	defer queue.Shutdown()

	// Summarization runs in a goroutine, so a restart abandons whatever was in
	// flight and leaves the meeting stuck at PROCESSING. Pick those up now.
	svc.RecoverStuckSummaries(ctx)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           newRouter(cfg, db, svc),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE streams stay open for the whole meeting.
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server_listening", "port", cfg.Port, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("server_shutting_down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// queueRef defers resolution of the queue until it exists, breaking the
// service/queue construction cycle without exposing a setter.
type queueRef struct{ q **worker.Queue }

func (r queueRef) Submit(job worker.Job) error { return (*r.q).Submit(job) }

func (r queueRef) WaitFor(ctx context.Context, meetingID uuid.UUID) error {
	return (*r.q).WaitFor(ctx, meetingID)
}

func buildSummarizer(cfg *config.Config) summarizer.Summarizer {
	provider := strings.ToLower(strings.TrimSpace(cfg.LLMProvider))

	if cfg.LLMAPIKey == "" {
		// Running without a key is a supported state: recording and live
		// transcript work, only the notes step is unavailable.
		slog.Warn("summarizer_disabled", "reason", "LLM_API_KEY is not set")
		return nil
	}

	switch provider {
	case "", "gemini":
		g := summarizer.NewGemini(summarizer.GeminiOptions{
			Model:           cfg.LLMModel,
			Fallbacks:       cfg.LLMFallbacks,
			APIKey:          cfg.LLMAPIKey,
			Timeout:         cfg.SummaryTimeout,
			MaxRetries:      cfg.LLMMaxRetries,
			MaxOutputTokens: cfg.LLMMaxOutputTokens,
		})
		slog.Info("summarizer_ready",
			"provider", "gemini", "models", g.Models(), "max_retries", cfg.LLMMaxRetries)
		return g
	default:
		slog.Error("summarizer_unknown_provider", "provider", provider)
		return nil
	}
}

func newRouter(cfg *config.Config, db *database.DB, svc *meeting.Service) *gin.Engine {
	if cfg.AppEnv != "development" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(), cors(cfg.CORSOrigins))
	r.MaxMultipartMemory = cfg.MaxUploadMB << 20

	healthHandler := health.NewHandler(cfg, db)

	// Unversioned probe for container orchestration.
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	v1 := r.Group("/api/v1")
	v1.GET("/health", healthHandler.Check)
	meeting.NewHandler(svc, cfg.SSEHeartbeat).Register(v1)

	return r
}

// requestLogger emits one structured line per request. It records method,
// path, status and latency only — never bodies, which would mean transcripts
// in the logs (PRD §37).
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.Request.URL.Path
		// Health polling every few seconds would drown out everything else,
		// and an SSE stream logs once per meeting at disconnect — neither is
		// worth a line.
		if path == "/healthz" || path == "/api/v1/health" || strings.HasSuffix(path, "/stream") {
			return
		}

		slog.Info("http_request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}

func cors(origins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[o] = true
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowed[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Accept, Last-Event-ID")
			c.Header("Vary", "Origin")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
