// Package config loads runtime configuration from the environment.
//
// Nothing here is hard-coded to a deployment: every value comes from the
// environment so the same binary runs in dev and on the user's server.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv string
	Port   string

	DatabaseURL string

	STTURL         string
	STTMaxRetries  int
	STTTimeout     time.Duration
	STTHealthCheck time.Duration

	AudioChunkSeconds int
	AudioTempDir      string
	KeepAudio         bool

	WhisperLanguage string

	StopWaitTimeout time.Duration
	SummaryTimeout  time.Duration

	LLMProvider        string
	LLMModel           string
	LLMFallbacks       []string
	LLMAPIKey          string
	LLMMaxRetries      int
	LLMMaxOutputTokens int

	SSEHeartbeat time.Duration

	CORSOrigins []string
	MaxUploadMB int64
}

// Load reads configuration from the environment, applying PRD §29 defaults.
// It fails fast on values the service cannot sensibly guess.
func Load() (*Config, error) {
	cfg := &Config{
		AppEnv: getEnv("APP_ENV", "development"),
		Port:   getEnv("PORT", "8080"),

		DatabaseURL: os.Getenv("DATABASE_URL"),

		STTURL:        getEnv("STT_URL", "http://stt:8000"),
		STTMaxRetries: getEnvInt("STT_MAX_RETRIES", 2),
		STTTimeout:    time.Duration(getEnvInt("STT_TIMEOUT_SECONDS", 600)) * time.Second,
		// Deliberately not STTTimeout: a health probe must not hang for
		// ten minutes behind a transcription-sized budget.
		STTHealthCheck: 2 * time.Second,

		AudioChunkSeconds: getEnvInt("AUDIO_CHUNK_SECONDS", 90),
		AudioTempDir:      getEnv("AUDIO_TEMP_DIR", "/tmp/meeting-chunks"),
		KeepAudio:         getEnvBool("KEEP_AUDIO", false),

		// Empty means auto-detect; the STT service treats "auto" the same way.
		WhisperLanguage: normalizeLanguage(getEnv("WHISPER_LANGUAGE", "auto")),

		StopWaitTimeout: time.Duration(getEnvInt("STOP_WAIT_SECONDS", 300)) * time.Second,
		SummaryTimeout:  time.Duration(getEnvInt("SUMMARY_TIMEOUT_SECONDS", 120)) * time.Second,

		LLMProvider: getEnv("LLM_PROVIDER", "gemini"),
		LLMModel:    strings.TrimSpace(os.Getenv("LLM_MODEL")),
		// Tried in order when the primary is overloaded (HTTP 503) or rate
		// limited. Partial notes from a second model beat no notes at all.
		LLMFallbacks:  splitList(getEnv("LLM_FALLBACK_MODELS", "gemini-2.5-flash")),
		LLMAPIKey:     os.Getenv("LLM_API_KEY"),
		LLMMaxRetries: getEnvInt("LLM_MAX_RETRIES", 3),
		// Room for thorough notes on a multi-hour meeting.
		LLMMaxOutputTokens: getEnvInt("LLM_MAX_OUTPUT_TOKENS", 32768),

		SSEHeartbeat: time.Duration(getEnvInt("SSE_HEARTBEAT_SECONDS", 15)) * time.Second,

		CORSOrigins: splitList(getEnv("CORS_ORIGINS", "http://localhost:5173,http://localhost:3000")),
		MaxUploadMB: int64(getEnvInt("MAX_UPLOAD_MB", 64)),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required (see .env.example)")
	}
	if cfg.AudioChunkSeconds <= 0 {
		return nil, fmt.Errorf("AUDIO_CHUNK_SECONDS must be positive, got %d", cfg.AudioChunkSeconds)
	}

	return cfg, nil
}

// Redacted returns a log-safe view of the config. Secrets never appear in
// logs (PRD §31, §37), so LLMAPIKey is reduced to whether it is set at all.
func (c *Config) Redacted() map[string]any {
	return map[string]any{
		"app_env":             c.AppEnv,
		"port":                c.Port,
		"stt_url":             c.STTURL,
		"stt_max_retries":     c.STTMaxRetries,
		"audio_chunk_seconds": c.AudioChunkSeconds,
		"audio_temp_dir":      c.AudioTempDir,
		"keep_audio":          c.KeepAudio,
		"llm_provider":        c.LLMProvider,
		"llm_model":           c.LLMModel,
		"llm_fallbacks":       c.LLMFallbacks,
		"llm_max_retries":     c.LLMMaxRetries,
		"llm_max_output":      c.LLMMaxOutputTokens,
		"llm_api_key_set":     c.LLMAPIKey != "",
		"sse_heartbeat":       c.SSEHeartbeat.String(),
	}
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return v
}

func getEnvBool(key string, fallback bool) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return v
}

// normalizeLanguage maps the "auto" sentinel to an empty string, which is what
// the STT client sends to mean "detect".
func normalizeLanguage(v string) string {
	if strings.EqualFold(v, "auto") {
		return ""
	}
	return v
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
