package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected Load to fail without DATABASE_URL")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.AudioChunkSeconds != 90 {
		t.Errorf("AudioChunkSeconds = %d, want 90", cfg.AudioChunkSeconds)
	}
	if cfg.STTMaxRetries != 2 {
		t.Errorf("STTMaxRetries = %d, want 2", cfg.STTMaxRetries)
	}
	if cfg.SSEHeartbeat != 15*time.Second {
		t.Errorf("SSEHeartbeat = %v, want 15s", cfg.SSEHeartbeat)
	}
	// A health probe must not inherit the 10-minute transcription budget.
	if cfg.STTHealthCheck >= cfg.STTTimeout {
		t.Errorf("STTHealthCheck %v must be well below STTTimeout %v", cfg.STTHealthCheck, cfg.STTTimeout)
	}
}

func TestLoadRejectsNonPositiveChunkSeconds(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("AUDIO_CHUNK_SECONDS", "0")

	if _, err := Load(); err == nil {
		t.Fatal("expected Load to reject AUDIO_CHUNK_SECONDS=0")
	}
}

// The API key must never reach the logs (PRD §31, §37).
func TestRedactedOmitsAPIKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:secret@localhost/test")
	t.Setenv("LLM_API_KEY", "sk-super-secret-value")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	redacted := cfg.Redacted()
	for key, value := range redacted {
		if s, ok := value.(string); ok && s == "sk-super-secret-value" {
			t.Fatalf("Redacted leaked the API key in field %q", key)
		}
	}

	if _, present := redacted["database_url"]; present {
		t.Error("Redacted must not expose database_url: it embeds the password")
	}
	if set, _ := redacted["llm_api_key_set"].(bool); !set {
		t.Error("llm_api_key_set should report true when a key is configured")
	}
}
