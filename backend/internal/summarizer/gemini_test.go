package summarizer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// longTranscript clears MinTranscriptChars so Summarize actually calls out.
const longTranscript = "Rapat dimulai pukul sembilan pagi dengan pembahasan anggaran kuartal empat " +
	"dan rencana belanja tim operasional untuk tiga bulan ke depan."

// fakeGemini stands in for the API and records what was asked of it.
type fakeGemini struct {
	mu sync.Mutex
	// responses is consumed in order; the last one repeats once exhausted.
	responses []response
	calls     []string
	server    *httptest.Server
}

type response struct {
	status     int
	body       string
	retryAfter string
}

func okBody() string {
	payload, _ := json.Marshal(map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{"text": minimalSummaryJSON}},
			},
		}},
	})
	return string(payload)
}

func errBody(message string) string {
	payload, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": message},
	})
	return string(payload)
}

func newFake(t *testing.T, responses ...response) *fakeGemini {
	t.Helper()
	f := &fakeGemini{responses: responses}

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		// Path is /v1beta/models/<model>:generateContent
		model := strings.TrimSuffix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], ":generateContent")
		f.calls = append(f.calls, model)

		i := len(f.calls) - 1
		if i >= len(f.responses) {
			i = len(f.responses) - 1
		}
		resp := f.responses[i]

		if resp.retryAfter != "" {
			w.Header().Set("Retry-After", resp.retryAfter)
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGemini) models() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// client points the summarizer at the fake server.
func (f *fakeGemini) client(t *testing.T, opts GeminiOptions) *Gemini {
	t.Helper()
	opts.APIKey = "test-key"
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	g := NewGemini(opts)
	g.http = f.server.Client()
	g.endpoint = f.server.URL + "/v1beta/models/%s:generateContent"
	// Real backoff would make this suite take minutes; the delay logic itself
	// is covered by TestRetryAfterHeaderIsHonoured.
	g.baseDelay = time.Microsecond
	return g
}

func TestSucceedsFirstTry(t *testing.T) {
	f := newFake(t, response{status: 200, body: okBody()})
	g := f.client(t, GeminiOptions{Model: "gemini-3.5-flash"})

	summary, err := g.Summarize(context.Background(), longTranscript)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary.Title != "Judul Rapat" {
		t.Errorf("title = %q", summary.Title)
	}
	if got := f.models(); len(got) != 1 {
		t.Errorf("made %d calls, want 1", len(got))
	}
}

// 503 is the "model overloaded" case the retry exists for.
func TestRetriesOn503ThenSucceeds(t *testing.T) {
	f := newFake(t,
		response{status: 503, body: errBody("The model is overloaded.")},
		response{status: 503, body: errBody("The model is overloaded.")},
		response{status: 200, body: okBody()},
	)
	g := f.client(t, GeminiOptions{Model: "gemini-3.5-flash", MaxRetries: 3})

	if _, err := g.Summarize(context.Background(), longTranscript); err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	calls := f.models()
	if len(calls) != 3 {
		t.Fatalf("made %d calls, want 3", len(calls))
	}
	for _, m := range calls {
		if m != "gemini-3.5-flash" {
			t.Errorf("retried on %q; retries must stay on the same model", m)
		}
	}
}

// Once a model's retries are spent, a different model may still have capacity.
func TestFallsBackToNextModelWhenOverloaded(t *testing.T) {
	f := newFake(t,
		response{status: 503, body: errBody("overloaded")},
		response{status: 503, body: errBody("overloaded")},
		response{status: 200, body: okBody()},
	)
	g := f.client(t, GeminiOptions{
		Model:      "gemini-3.5-flash",
		Fallbacks:  []string{"gemini-2.5-flash"},
		MaxRetries: 1,
	})

	if _, err := g.Summarize(context.Background(), longTranscript); err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	calls := f.models()
	want := []string{"gemini-3.5-flash", "gemini-3.5-flash", "gemini-2.5-flash"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d was %q, want %q", i, calls[i], want[i])
		}
	}
}

func TestRateLimitAlsoFallsBack(t *testing.T) {
	f := newFake(t,
		response{status: 429, body: errBody("quota exceeded")},
		response{status: 200, body: okBody()},
	)
	g := f.client(t, GeminiOptions{
		Model:      "primary",
		Fallbacks:  []string{"secondary"},
		MaxRetries: 0,
	})

	if _, err := g.Summarize(context.Background(), longTranscript); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if calls := f.models(); calls[len(calls)-1] != "secondary" {
		t.Errorf("did not reach the fallback: %v", calls)
	}
}

// A misspelled model id should move on, not burn retries on a 404.
func TestUnknownModelFallsThroughWithoutRetrying(t *testing.T) {
	f := newFake(t,
		response{status: 404, body: errBody("models/typo is not found")},
		response{status: 200, body: okBody()},
	)
	g := f.client(t, GeminiOptions{
		Model:      "typo",
		Fallbacks:  []string{"gemini-2.5-flash"},
		MaxRetries: 3,
	})

	if _, err := g.Summarize(context.Background(), longTranscript); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if calls := f.models(); len(calls) != 2 {
		t.Errorf("calls = %v; a 404 must not be retried", calls)
	}
}

// A bad API key fails identically everywhere; trying more models wastes time.
func TestAuthFailureDoesNotRetryOrFallBack(t *testing.T) {
	f := newFake(t, response{status: 403, body: errBody("API key not valid")})
	g := f.client(t, GeminiOptions{
		Model:      "primary",
		Fallbacks:  []string{"secondary"},
		MaxRetries: 3,
	})

	if _, err := g.Summarize(context.Background(), longTranscript); err == nil {
		t.Fatal("expected an error")
	}
	if calls := f.models(); len(calls) != 1 {
		t.Errorf("calls = %v, want exactly 1", calls)
	}
}

// Generation is nondeterministic, so unusable output deserves another roll.
func TestRetriesUnusableModelOutput(t *testing.T) {
	garbage, _ := json.Marshal(map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{"text": "Maaf, saya tidak bisa membantu."}},
			},
		}},
	})

	f := newFake(t,
		response{status: 200, body: string(garbage)},
		response{status: 200, body: okBody()},
	)
	g := f.client(t, GeminiOptions{Model: "primary", MaxRetries: 2})

	if _, err := g.Summarize(context.Background(), longTranscript); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if calls := f.models(); len(calls) != 2 {
		t.Errorf("calls = %v, want 2", calls)
	}
}

func TestGivesUpAfterEveryModelIsExhausted(t *testing.T) {
	f := newFake(t, response{status: 503, body: errBody("overloaded")})
	g := f.client(t, GeminiOptions{
		Model:      "primary",
		Fallbacks:  []string{"secondary"},
		MaxRetries: 1,
	})

	_, err := g.Summarize(context.Background(), longTranscript)
	if err == nil {
		t.Fatal("expected an error once everything is exhausted")
	}
	// The message must name what was tried, or diagnosing this is guesswork.
	if !strings.Contains(err.Error(), "primary") || !strings.Contains(err.Error(), "secondary") {
		t.Errorf("error should name the chain, got: %v", err)
	}
	if calls := f.models(); len(calls) != 4 {
		t.Errorf("calls = %v, want 4 (2 models x 2 attempts)", calls)
	}
}

// A cancelled context is the caller giving up; do not keep hammering.
func TestStopsImmediatelyWhenContextIsCancelled(t *testing.T) {
	f := newFake(t, response{status: 503, body: errBody("overloaded")})
	g := f.client(t, GeminiOptions{Model: "primary", Fallbacks: []string{"secondary"}, MaxRetries: 5})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := g.Summarize(ctx, longTranscript); err == nil {
		t.Fatal("expected an error")
	}
	if calls := f.models(); len(calls) > 1 {
		t.Errorf("kept calling after cancellation: %v", calls)
	}
}

func TestRetryAfterHeaderIsHonoured(t *testing.T) {
	if got := parseRetryAfter("7"); got != 7*time.Second {
		t.Errorf("numeric Retry-After = %v, want 7s", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("absent Retry-After = %v, want 0", got)
	}
	if got := parseRetryAfter("not-a-date"); got != 0 {
		t.Errorf("junk Retry-After = %v, want 0", got)
	}
	// A date in the past must not produce a negative delay.
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("past Retry-After = %v, want 0", got)
	}
}

// A fallback equal to the primary is not a fallback.
func TestDuplicateFallbackIsDropped(t *testing.T) {
	g := NewGemini(GeminiOptions{
		Model:     "gemini-3.5-flash",
		Fallbacks: []string{"gemini-3.5-flash", "", "  ", "gemini-2.5-flash"},
		APIKey:    "k",
	})

	want := []string{"gemini-3.5-flash", "gemini-2.5-flash"}
	got := g.Models()
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("models = %v, want %v", got, want)
		}
	}
}

func TestDefaultModelWhenUnset(t *testing.T) {
	g := NewGemini(GeminiOptions{APIKey: "k"})
	if got := g.Models()[0]; got != defaultGeminiModel {
		t.Errorf("default model = %q, want %q", got, defaultGeminiModel)
	}
}
