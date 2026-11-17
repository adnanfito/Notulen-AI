package meeting

import (
	"strings"
	"testing"
)

// STT finishes out of order, so combine must trust chunk_index ordering and
// nothing else (PRD §10).
func TestCombineJoinsInIndexOrder(t *testing.T) {
	chunks := []TranscriptChunk{
		{ChunkIndex: 0, Text: "Halo semuanya.", Status: ChunkCompleted},
		{ChunkIndex: 1, Text: "Kita bahas deployment.", Status: ChunkCompleted},
		{ChunkIndex: 2, Text: "Selesai.", Status: ChunkCompleted},
	}

	text, failed := combine(chunks)

	if want := "Halo semuanya. Kita bahas deployment. Selesai."; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
}

// One bad chunk must not cost the meeting the rest of its transcript (PRD §12).
func TestCombineSkipsFailedChunksButKeepsTheRest(t *testing.T) {
	chunks := []TranscriptChunk{
		{ChunkIndex: 0, Text: "Bagian pertama.", Status: ChunkCompleted},
		{ChunkIndex: 1, Text: "", Status: ChunkFailed},
		{ChunkIndex: 2, Text: "Bagian ketiga.", Status: ChunkCompleted},
	}

	text, failed := combine(chunks)

	if !strings.Contains(text, "Bagian pertama.") || !strings.Contains(text, "Bagian ketiga.") {
		t.Errorf("surviving chunks were lost: %q", text)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
}

// VAD strips silence, so a quiet chunk is COMPLETED with empty text. It must
// not leave double spaces in the transcript.
func TestCombineIgnoresSilentChunks(t *testing.T) {
	chunks := []TranscriptChunk{
		{ChunkIndex: 0, Text: "Sebelum jeda.", Status: ChunkCompleted},
		{ChunkIndex: 1, Text: "   ", Status: ChunkCompleted},
		{ChunkIndex: 2, Text: "Setelah jeda.", Status: ChunkCompleted},
	}

	text, failed := combine(chunks)

	if want := "Sebelum jeda. Setelah jeda."; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if failed != 0 {
		t.Errorf("a silent chunk is not a failure, got failed = %d", failed)
	}
}

func TestCombineHandlesNoChunks(t *testing.T) {
	text, failed := combine(nil)
	if text != "" || failed != 0 {
		t.Errorf("combine(nil) = %q, %d", text, failed)
	}
}

func TestCombineAllFailed(t *testing.T) {
	chunks := []TranscriptChunk{
		{ChunkIndex: 0, Status: ChunkFailed},
		{ChunkIndex: 1, Status: ChunkFailed},
	}

	text, failed := combine(chunks)

	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
	if failed != 2 {
		t.Errorf("failed = %d, want 2", failed)
	}
}

// The browser's filename is untrusted; only a known-good extension survives.
func TestSafeExt(t *testing.T) {
	cases := map[string]string{
		"chunk.webm":          ".webm",
		"CHUNK.WEBM":          ".webm",
		"recording.ogg":       ".ogg",
		"audio.wav":           ".wav",
		"":                    ".webm",
		"noextension":         ".webm",
		"../../../etc/passwd": ".webm",
		"evil.sh":             ".webm",
		"payload.php":         ".webm",
		"x.exe":               ".webm",
	}

	for input, want := range cases {
		if got := safeExt(input); got != want {
			t.Errorf("safeExt(%q) = %q, want %q", input, got, want)
		}
	}
}
