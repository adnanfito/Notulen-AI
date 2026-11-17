// Package summarizer turns a raw transcript into structured meeting notes.
//
// The provider sits behind an interface (PRD §17) so swapping Gemini for
// another model never reaches the meeting handlers.
package summarizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrTranscriptTooShort means there was not enough material to summarize.
// The caller shows the transcript and skips the notes rather than sending
// meaningless input to a model (PRD §33).
var ErrTranscriptTooShort = errors.New("transcript too short to summarize")

// MinTranscriptChars is the floor below which summarization is pointless.
const MinTranscriptChars = 80

type ActionItem struct {
	Task     string  `json:"task"`
	Assignee *string `json:"assignee"`
	Deadline *string `json:"deadline"`
	// Completed is local application state the user toggles; the model always
	// emits false (PRD §16).
	Completed bool `json:"completed"`
}

type Topic struct {
	TopicName        string   `json:"topic_name"`
	DiscussionPoints []string `json:"discussion_points"`
}

type Summary struct {
	Title            string       `json:"title"`
	LanguageUsed     string       `json:"language_used"`
	ExecutiveSummary string       `json:"executive_summary"`
	KeyDecisions     []string     `json:"key_decisions"`
	MainTopics       []Topic      `json:"main_topics"`
	ActionItems      []ActionItem `json:"action_items"`
	Tags             []string     `json:"tags"`
}

type Summarizer interface {
	Summarize(ctx context.Context, transcript string) (*Summary, error)
}

// Normalize repairs the shapes a model gets wrong without inventing content:
// nil slices become empty arrays so the frontend never has to null-check, and
// blank assignees/deadlines collapse to null as the PRD requires.
func (s *Summary) Normalize() {
	s.Title = strings.TrimSpace(s.Title)
	s.ExecutiveSummary = strings.TrimSpace(s.ExecutiveSummary)
	s.LanguageUsed = normalizeLanguage(s.LanguageUsed)

	if s.KeyDecisions == nil {
		s.KeyDecisions = []string{}
	}
	if s.MainTopics == nil {
		s.MainTopics = []Topic{}
	}
	if s.ActionItems == nil {
		s.ActionItems = []ActionItem{}
	}
	if s.Tags == nil {
		s.Tags = []string{}
	}

	for i := range s.MainTopics {
		if s.MainTopics[i].DiscussionPoints == nil {
			s.MainTopics[i].DiscussionPoints = []string{}
		}
	}

	for i := range s.ActionItems {
		s.ActionItems[i].Assignee = nilIfBlank(s.ActionItems[i].Assignee)
		s.ActionItems[i].Deadline = nilIfBlank(s.ActionItems[i].Deadline)
	}
}

// Validate rejects output that would be useless or misleading if stored.
func (s *Summary) Validate() error {
	if s.Title == "" {
		return errors.New("summary has no title")
	}
	if s.ExecutiveSummary == "" {
		return errors.New("summary has no executive_summary")
	}
	for i, item := range s.ActionItems {
		if strings.TrimSpace(item.Task) == "" {
			return fmt.Errorf("action item %d has no task", i)
		}
	}
	return nil
}

// languageCodes maps what models actually answer to the ISO codes the schema
// asks for. Asked for "language_used", a model will happily reply "Bahasa
// Indonesia", which then renders as "BAHASA INDONESIA" in the UI's language
// chip.
var languageCodes = map[string]string{
	"id":                           "id",
	"ind":                          "id",
	"indonesia":                    "id",
	"indonesian":                   "id",
	"bahasa":                       "id",
	"bahasa indonesia":             "id",
	"en":                           "en",
	"eng":                          "en",
	"english":                      "en",
	"inggris":                      "en",
	"bahasa inggris":               "en",
	"mixed":                        "id-en",
	"mixed indonesian-english":     "id-en",
	"indonesian-english":           "id-en",
	"campuran":                     "id-en",
	"bahasa indonesia dan english": "id-en",
	"bahasa indonesia dan inggris": "id-en",
}

// MaxLanguageLen matches the language_used column. A value longer than this
// aborts the whole finalize write, which would cost the meeting its stored
// transcript over a metadata field — so the cap is enforced here.
const MaxLanguageLen = 50

// normalizeLanguage reduces a model's answer to an ISO-ish code.
//
// Asked for "language_used", a model may answer with a code, a language name,
// or a whole sentence like "Bahasa Indonesia dengan campuran istilah teknis
// Bahasa Inggris". All three have to come out short and useful.
func normalizeLanguage(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	if code, ok := languageCodes[lower]; ok {
		return code
	}

	// Fall back to looking for the languages inside a longer answer.
	hasID := strings.Contains(lower, "indonesia")
	hasEN := strings.Contains(lower, "english") || strings.Contains(lower, "inggris")

	switch {
	case hasID && hasEN:
		return "id-en"
	case hasID:
		return "id"
	case hasEN:
		return "en"
	}

	// Something genuinely unexpected. Keep it, but never wide enough to break
	// the insert.
	runes := []rune(trimmed)
	if len(runes) > MaxLanguageLen {
		return string(runes[:MaxLanguageLen])
	}
	return trimmed
}

func nilIfBlank(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	// Models like to answer the "use null" instruction with the word instead.
	if trimmed == "" || strings.EqualFold(trimmed, "null") || strings.EqualFold(trimmed, "none") {
		return nil
	}
	return &trimmed
}

// ParseSummary decodes model output into a validated Summary.
//
// Models wrap JSON in markdown fences despite being told not to, so the fence
// is stripped before decoding rather than failing the whole meeting over it.
func ParseSummary(raw string) (*Summary, error) {
	cleaned := stripCodeFence(strings.TrimSpace(raw))

	var summary Summary
	if err := json.Unmarshal([]byte(cleaned), &summary); err != nil {
		return nil, fmt.Errorf("model did not return valid JSON: %w", err)
	}

	summary.Normalize()
	if err := summary.Validate(); err != nil {
		return nil, err
	}
	return &summary, nil
}

func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}

	// Drop the opening fence line, which may carry a language tag.
	if newline := strings.IndexByte(s, '\n'); newline >= 0 {
		s = s[newline+1:]
	} else {
		return s
	}

	if end := strings.LastIndex(s, "```"); end >= 0 {
		s = s[:end]
	}
	return strings.TrimSpace(s)
}
