package summarizer

import (
	"strings"
	"testing"
)

func TestParseSummaryAcceptsPlainJSON(t *testing.T) {
	raw := `{
		"title": "Rapat Mingguan Tim Backend",
		"language_used": "id",
		"executive_summary": "Tim membahas deployment dan database.",
		"key_decisions": ["Deploy hari Jumat"],
		"main_topics": [{"topic_name": "Deployment", "discussion_points": ["Pakai Docker"]}],
		"action_items": [{"task": "Siapkan staging", "assignee": "Budi", "deadline": "Jumat", "completed": false}],
		"tags": ["backend"]
	}`

	got, err := ParseSummary(raw)
	if err != nil {
		t.Fatalf("ParseSummary: %v", err)
	}
	if got.Title != "Rapat Mingguan Tim Backend" {
		t.Errorf("title = %q", got.Title)
	}
	if len(got.ActionItems) != 1 || *got.ActionItems[0].Assignee != "Budi" {
		t.Errorf("action items not parsed: %+v", got.ActionItems)
	}
}

// Models wrap JSON in fences despite being told not to. Failing the whole
// meeting over that would be a poor trade.
func TestParseSummaryStripsMarkdownFence(t *testing.T) {
	for _, fence := range []string{"```json", "```"} {
		raw := fence + "\n" + minimalSummaryJSON + "\n```"

		got, err := ParseSummary(raw)
		if err != nil {
			t.Fatalf("fence %q: %v", fence, err)
		}
		if got.Title != "Judul Rapat" {
			t.Errorf("fence %q: title = %q", fence, got.Title)
		}
	}
}

func TestParseSummaryNormalizesNullLikeAssignees(t *testing.T) {
	raw := `{
		"title": "Judul Rapat",
		"language_used": "id",
		"executive_summary": "Ringkasan.",
		"action_items": [
			{"task": "A", "assignee": "null", "deadline": "  ", "completed": false},
			{"task": "B", "assignee": "None", "deadline": "besok", "completed": false}
		]
	}`

	got, err := ParseSummary(raw)
	if err != nil {
		t.Fatalf("ParseSummary: %v", err)
	}

	// The prompt asks for null; models often answer with the *word* null.
	if got.ActionItems[0].Assignee != nil {
		t.Errorf("literal \"null\" assignee should normalize to nil, got %q", *got.ActionItems[0].Assignee)
	}
	if got.ActionItems[0].Deadline != nil {
		t.Errorf("blank deadline should normalize to nil")
	}
	if got.ActionItems[1].Assignee != nil {
		t.Errorf("literal \"None\" assignee should normalize to nil")
	}
	if got.ActionItems[1].Deadline == nil || *got.ActionItems[1].Deadline != "besok" {
		t.Errorf("real deadline must survive")
	}
}

// The frontend renders these arrays directly; nil would force a null check at
// every use site.
func TestParseSummaryNeverLeavesNilSlices(t *testing.T) {
	got, err := ParseSummary(minimalSummaryJSON)
	if err != nil {
		t.Fatalf("ParseSummary: %v", err)
	}

	if got.KeyDecisions == nil || got.MainTopics == nil || got.ActionItems == nil || got.Tags == nil {
		t.Fatalf("nil slice survived normalization: %+v", got)
	}
	if len(got.KeyDecisions) != 0 {
		t.Errorf("empty decisions should stay empty, got %v", got.KeyDecisions)
	}
}

func TestParseSummaryRejectsInvalidJSON(t *testing.T) {
	if _, err := ParseSummary("I'm sorry, I cannot help with that."); err == nil {
		t.Fatal("expected an error for non-JSON output")
	}
}

func TestParseSummaryRejectsMissingRequiredFields(t *testing.T) {
	cases := map[string]string{
		"no title":   `{"executive_summary": "ada isinya"}`,
		"no summary": `{"title": "Ada Judul"}`,
		"empty task": `{"title": "T", "executive_summary": "S", "action_items": [{"task": "   "}]}`,
	}

	for name, raw := range cases {
		if _, err := ParseSummary(raw); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestBuildPromptEmbedsTranscriptBetweenMarkers(t *testing.T) {
	prompt := BuildPrompt("halo semuanya")

	if !strings.Contains(prompt, "[TRANSCRIPT_START]") || !strings.Contains(prompt, "[TRANSCRIPT_END]") {
		t.Fatal("markers missing")
	}
	if !strings.Contains(prompt, "halo semuanya") {
		t.Fatal("transcript missing")
	}
	if !strings.Contains(prompt, "Do not hallucinate missing facts.") {
		t.Fatal("accuracy rules missing — these are the point of the prompt")
	}
}

// A transcript containing the literal "%s" must not corrupt the template.
func TestBuildPromptIsNotFormatString(t *testing.T) {
	prompt := BuildPrompt("kita pakai printf %s dan %d di kode")

	if !strings.Contains(prompt, "kita pakai printf %s dan %d di kode") {
		t.Fatal("transcript with format verbs was mangled")
	}
	if strings.Contains(prompt, "%!") {
		t.Fatal("prompt shows formatting errors")
	}
}

// A 3-hour meeting can exceed the model's context window. Truncating the
// middle keeps the opening and the closing, where decisions actually land.
func TestBuildPromptTruncatesVeryLongTranscriptsFromTheMiddle(t *testing.T) {
	head := strings.Repeat("A", 1000)
	tail := strings.Repeat("Z", 1000)
	long := head + strings.Repeat("m", maxTranscriptRunes) + tail

	prompt := BuildPrompt(long)

	if !strings.Contains(prompt, "middle of transcript omitted") {
		t.Fatal("expected a truncation marker")
	}
	if !strings.Contains(prompt, head) {
		t.Error("opening of the meeting was dropped")
	}
	if !strings.Contains(prompt, tail) {
		t.Error("closing of the meeting was dropped — that is where decisions are")
	}
}

func TestSummarizeRejectsShortTranscriptWithoutCallingProvider(t *testing.T) {
	// No HTTP client is configured, so reaching the network would panic or
	// hang — the point is that it returns before trying.
	g := NewGemini(GeminiOptions{APIKey: "fake-key"})

	_, err := g.Summarize(t.Context(), "halo")
	if err != ErrTranscriptTooShort {
		t.Fatalf("got %v, want ErrTranscriptTooShort", err)
	}
}

const minimalSummaryJSON = `{
	"title": "Judul Rapat",
	"language_used": "id",
	"executive_summary": "Ringkasan singkat rapat."
}`

// Asked for "language_used", models answer with a language name as often as a
// code. The UI renders this as an uppercase chip, so "BAHASA INDONESIA" is the
// failure this guards.
func TestNormalizeLanguageMapsNamesToCodes(t *testing.T) {
	cases := map[string]string{
		"Bahasa Indonesia":   "id",
		"bahasa indonesia":   "id",
		"Indonesian":         "id",
		"id":                 "id",
		"English":            "en",
		"en":                 "en",
		"Inggris":            "en",
		"Mixed":              "id-en",
		"campuran":           "id-en",
		"Indonesian-English": "id-en",
		"":                   "",
		// Anything unrecognised is left as the model said it, not guessed at.
		"Javanese": "Javanese",
	}

	for input, want := range cases {
		if got := normalizeLanguage(input); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseSummaryNormalizesLanguage(t *testing.T) {
	raw := `{
		"title": "Judul Rapat",
		"language_used": "Bahasa Indonesia",
		"executive_summary": "Ringkasan singkat."
	}`

	got, err := ParseSummary(raw)
	if err != nil {
		t.Fatalf("ParseSummary: %v", err)
	}
	if got.LanguageUsed != "id" {
		t.Errorf("language_used = %q, want \"id\"", got.LanguageUsed)
	}
}

// Regression: a model answered `language_used` with a whole sentence, which
// overflowed the VARCHAR(50) column and aborted the finalize write — costing
// the meeting its stored transcript over a metadata field.
func TestNormalizeLanguageNeverExceedsColumnWidth(t *testing.T) {
	cases := []string{
		"Bahasa Indonesia dengan campuran istilah teknis Bahasa Inggris",
		"Indonesian with a significant amount of English technical terminology",
		strings.Repeat("x", 500),
		"",
	}

	for _, input := range cases {
		got := normalizeLanguage(input)
		if len([]rune(got)) > MaxLanguageLen {
			t.Errorf("normalizeLanguage(%.40q...) = %d runes, must be <= %d",
				input, len([]rune(got)), MaxLanguageLen)
		}
	}
}

// A long free-text answer should still be understood, not merely truncated.
func TestNormalizeLanguageReadsLongAnswers(t *testing.T) {
	cases := map[string]string{
		"Bahasa Indonesia dengan campuran istilah teknis Bahasa Inggris":        "id-en",
		"Indonesian with a significant amount of English technical terminology": "id-en",
		"Sebagian besar Bahasa Indonesia":                                       "id",
		"Mostly English with some loanwords":                                    "en",
	}

	for input, want := range cases {
		if got := normalizeLanguage(input); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseSummaryClampsLongLanguage(t *testing.T) {
	raw := `{
		"title": "Judul Rapat",
		"language_used": "Bahasa Indonesia dengan campuran istilah teknis Bahasa Inggris",
		"executive_summary": "Ringkasan singkat."
	}`

	got, err := ParseSummary(raw)
	if err != nil {
		t.Fatalf("ParseSummary: %v", err)
	}
	if got.LanguageUsed != "id-en" {
		t.Errorf("language_used = %q, want \"id-en\"", got.LanguageUsed)
	}
}

// The prompt has to ask for depth without licensing invention. If either half
// goes missing the notes get worse in opposite directions.
func TestPromptDemandsDepthAndForbidsInvention(t *testing.T) {
	// Collapse wrapping so the assertions describe intent, not line breaks.
	prompt := strings.Join(strings.Fields(BuildPrompt("halo semuanya")), " ")

	depth := []string{
		"Cover EVERY substantive topic",
		"Scale the notes to the meeting",
		"numbers, amounts, dates",
		"several discussion points per topic",
		"Capture every task, commitment, or follow-up",
	}
	for _, want := range depth {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lost its depth instruction: %q", want)
		}
	}

	accuracy := []string{
		"Do not hallucinate missing facts",
		"Never invent people, deadlines, decisions",
		"Writing less is always better than writing something that was not said",
		"Do not turn a suggestion or casual discussion into a decision",
	}
	for _, want := range accuracy {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lost its accuracy guard: %q", want)
		}
	}
}

// Asking for a code keeps language_used from arriving as a sentence, which is
// what overflowed the column.
func TestPromptAsksForALanguageCode(t *testing.T) {
	prompt := strings.Join(strings.Fields(BuildPrompt("halo")), " ")
	if !strings.Contains(prompt, `"id", "en", or "id-en"`) {
		t.Error("prompt no longer constrains language_used to a short code")
	}
}
