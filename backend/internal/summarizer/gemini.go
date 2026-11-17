package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGeminiModel = "gemini-3.5-flash"
	geminiEndpoint     = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

	// Thorough notes for a multi-hour meeting run long. Too low a ceiling
	// truncates the JSON mid-structure, which then fails to parse and burns
	// every retry on the same wall.
	defaultMaxOutputTokens = 32768
)

// Gemini talks to the Generative Language REST API directly.
//
// REST rather than an SDK: the only call this service makes is a single
// generateContent, the endpoint is stable, and it keeps the dependency tree
// small. Everything sits behind Summarizer, so another provider can be added
// without touching meeting handlers (PRD §17).
type Gemini struct {
	// models is the chain tried in order. A model that is overloaded or
	// unavailable hands off to the next one rather than failing the meeting.
	models          []string
	apiKey          string
	maxRetries      int
	maxOutputTokens int
	http            *http.Client

	// Overridden in tests to point at a stub server and to collapse the
	// backoff; production always uses the values set in NewGemini.
	endpoint  string
	baseDelay time.Duration
}

type GeminiOptions struct {
	Model     string
	Fallbacks []string
	APIKey    string
	Timeout   time.Duration
	// MaxRetries is per model, not across the whole chain.
	MaxRetries int
	// MaxOutputTokens caps the generated notes. Zero uses the default.
	MaxOutputTokens int
}

func NewGemini(opts GeminiOptions) *Gemini {
	primary := strings.TrimSpace(opts.Model)
	if primary == "" {
		primary = defaultGeminiModel
	}

	models := []string{primary}
	for _, f := range opts.Fallbacks {
		f = strings.TrimSpace(f)
		// A fallback identical to the primary buys nothing: if the model is
		// overloaded, asking it again under a different label still fails.
		if f != "" && f != primary {
			models = append(models, f)
		}
	}

	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.MaxOutputTokens <= 0 {
		opts.MaxOutputTokens = defaultMaxOutputTokens
	}

	return &Gemini{
		models:          models,
		apiKey:          opts.APIKey,
		maxRetries:      opts.MaxRetries,
		maxOutputTokens: opts.MaxOutputTokens,
		http:            &http.Client{Timeout: opts.Timeout},
		endpoint:        geminiEndpoint,
		baseDelay:       time.Second,
	}
}

// Models reports the chain, for logging at startup.
func (g *Gemini) Models() []string { return g.models }

// --- error classification ---

// retryableError means the same model may work if asked again.
type retryableError struct {
	status     int
	message    string
	retryAfter time.Duration
}

func (e *retryableError) Error() string {
	if e.status > 0 {
		return fmt.Sprintf("gemini %d: %s", e.status, e.message)
	}
	return e.message
}

// overloadedError means this model is busy; another model may not be.
type overloadedError struct{ *retryableError }

func classify(status int, message string, retryAfter time.Duration) error {
	base := &retryableError{status: status, message: message, retryAfter: retryAfter}

	switch status {
	case http.StatusTooManyRequests, // quota or rate limit
		http.StatusServiceUnavailable, // model overloaded — the 503 case
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusGatewayTimeout:
		// 503 and 429 are about this model's capacity, so switching models is
		// worth trying once retries are spent.
		if status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests {
			return &overloadedError{base}
		}
		return base

	case http.StatusNotFound:
		// A model id that does not exist. Retrying is pointless, but the next
		// model in the chain might be spelled correctly.
		return &overloadedError{&retryableError{
			status:  status,
			message: "model not found: " + message,
		}}

	default:
		// 400 (malformed request), 401/403 (bad key) — nothing to retry.
		return fmt.Errorf("gemini returned %d: %s", status, message)
	}
}

func isRetryable(err error) bool {
	var r *retryableError
	var o *overloadedError
	return errors.As(err, &r) || errors.As(err, &o)
}

func shouldTryNextModel(err error) bool {
	var o *overloadedError
	return errors.As(err, &o)
}

func (g *Gemini) retryDelay(err error, attempt int) time.Duration {
	// The server's own Retry-After beats any guess we make.
	var o *overloadedError
	if errors.As(err, &o) && o.retryAfter > 0 {
		return o.retryAfter
	}
	var r *retryableError
	if errors.As(err, &r) && r.retryAfter > 0 {
		return r.retryAfter
	}

	// Exponential backoff with jitter, so repeated failures do not resend in
	// lockstep with whatever else is hammering the same overloaded model.
	unit := g.baseDelay
	if unit <= 0 {
		unit = time.Second
	}
	base := time.Duration(1<<attempt) * unit
	if max := 30 * time.Second; base > max {
		base = max
	}
	if base < 2 {
		return base
	}
	jitter := time.Duration(rand.Int63n(int64(base / 2)))
	return base/2 + jitter
}

// --- summarization ---

func (g *Gemini) Summarize(ctx context.Context, transcript string) (*Summary, error) {
	trimmed := strings.TrimSpace(transcript)
	if len([]rune(trimmed)) < MinTranscriptChars {
		return nil, ErrTranscriptTooShort
	}
	if g.apiKey == "" {
		return nil, errors.New("LLM_API_KEY is not configured")
	}

	prompt := BuildPrompt(trimmed)
	var lastErr error

	for i, model := range g.models {
		if i > 0 {
			slog.Warn("summary_model_fallback",
				"from", g.models[i-1], "to", model, "reason", lastErr)
		}

		summary, err := g.summarizeWithModel(ctx, model, prompt)
		if err == nil {
			if i > 0 {
				slog.Info("summary_recovered_on_fallback", "model", model)
			}
			return summary, nil
		}

		lastErr = err

		// Context cancellation is the caller giving up; do not keep trying.
		if ctx.Err() != nil {
			return nil, err
		}
		// A malformed request or bad key fails the same way on every model.
		if !shouldTryNextModel(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("all models exhausted (%s): %w", strings.Join(g.models, ", "), lastErr)
}

// summarizeWithModel runs one model, retrying transient failures.
func (g *Gemini) summarizeWithModel(ctx context.Context, model, prompt string) (*Summary, error) {
	var lastErr error

	for attempt := 0; attempt <= g.maxRetries; attempt++ {
		if attempt > 0 {
			delay := g.retryDelay(lastErr, attempt)
			slog.Warn("summary_retry",
				"model", model, "attempt", attempt, "of", g.maxRetries,
				"in", delay.String(), "error", lastErr)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		summary, err := g.attempt(ctx, model, prompt)
		if err == nil {
			return summary, nil
		}

		lastErr = err
		if !isRetryable(err) || ctx.Err() != nil {
			return nil, err
		}
	}

	return nil, lastErr
}

func (g *Gemini) attempt(ctx context.Context, model, prompt string) (*Summary, error) {
	body, err := json.Marshal(geminiRequest{
		Contents: []geminiContent{{
			Role:  "user",
			Parts: []geminiPart{{Text: prompt}},
		}},
		GenerationConfig: geminiGenConfig{
			Temperature:      0.2,
			ResponseMIMEType: "application/json",
			MaxOutputTokens:  g.maxOutputTokens,
		},
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf(g.endpoint, model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Header rather than query string, so the key cannot leak into request logs.
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A dropped connection or DNS blip is worth another go.
		return nil, &retryableError{message: "gemini request failed: " + err.Error()}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &retryableError{message: "reading gemini response: " + err.Error()}
	}

	var parsed geminiResponse
	if jsonErr := json.Unmarshal(raw, &parsed); jsonErr != nil && resp.StatusCode == http.StatusOK {
		return nil, &retryableError{message: "gemini response was not JSON"}
	}

	if resp.StatusCode != http.StatusOK {
		message := parsed.Error.Message
		if message == "" {
			message = resp.Status
		}
		// Never echo the raw body: it can contain the transcript back again.
		return nil, classify(resp.StatusCode, message, parseRetryAfter(resp.Header.Get("Retry-After")))
	}

	if parsed.PromptFeedback.BlockReason != "" {
		// A safety block is a verdict on the content, not a transient fault.
		return nil, fmt.Errorf("gemini blocked the prompt: %s", parsed.PromptFeedback.BlockReason)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return nil, &retryableError{message: "gemini returned no content"}
	}

	// Hitting the ceiling cuts the JSON mid-structure. Retrying the same
	// prompt at the same cap hits the same wall, so this is not retryable —
	// it is a configuration problem, and the message says which knob.
	if parsed.Candidates[0].FinishReason == "MAX_TOKENS" {
		return nil, fmt.Errorf(
			"notes exceeded LLM_MAX_OUTPUT_TOKENS (%d); raise it for meetings this long",
			g.maxOutputTokens)
	}

	var text strings.Builder
	for _, part := range parsed.Candidates[0].Content.Parts {
		text.WriteString(part.Text)
	}

	summary, err := ParseSummary(text.String())
	if err != nil {
		// Generation is not deterministic, so malformed or incomplete output
		// is worth one more roll rather than costing the meeting its notes.
		return nil, &retryableError{message: "unusable model output: " + err.Error()}
	}
	return summary, nil
}

// parseRetryAfter reads the header in either of its legal forms.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// --- wire types ---

type geminiRequest struct {
	Contents         []geminiContent `json:"contents"`
	GenerationConfig geminiGenConfig `json:"generationConfig"`
	SafetySettings   []geminiSafety  `json:"safetySettings,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenConfig struct {
	// Low temperature: notes must reflect the transcript, not embellish it.
	Temperature float64 `json:"temperature"`
	// JSON mode removes the whole class of "model wrapped it in prose" failures.
	ResponseMIMEType string `json:"responseMimeType"`
	MaxOutputTokens  int    `json:"maxOutputTokens"`
}

type geminiSafety struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}
