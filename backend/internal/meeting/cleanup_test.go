package meeting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
)

func seedMeetingDir(t *testing.T, root string, id uuid.UUID, files ...string) string {
	t.Helper()
	dir := filepath.Join(root, id.String())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("audio"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Chunks are deleted as they transcribe, so this usually clears an empty
// directory — but it must not leave one behind per meeting.
func TestRemoveAudioDirDeletesTheMeetingDirectory(t *testing.T) {
	root := t.TempDir()
	id := uuid.New()
	dir := seedMeetingDir(t, root, id)

	svc := &Service{audioDir: root}
	svc.removeAudioDir(id)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("meeting directory survived cleanup")
	}
}

// A failure between upload and transcription can orphan real audio.
func TestRemoveAudioDirDeletesOrphanedChunks(t *testing.T) {
	root := t.TempDir()
	id := uuid.New()
	dir := seedMeetingDir(t, root, id, "000000.webm", "000001.webm")

	svc := &Service{audioDir: root}
	svc.removeAudioDir(id)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("orphaned audio survived cleanup")
	}
}

func TestRemoveAudioDirHonoursKeepAudio(t *testing.T) {
	root := t.TempDir()
	id := uuid.New()
	dir := seedMeetingDir(t, root, id, "000000.webm")

	svc := &Service{audioDir: root, keepAudio: true}
	svc.removeAudioDir(id)

	if _, err := os.Stat(dir); err != nil {
		t.Fatal("KEEP_AUDIO must preserve the audio, got:", err)
	}
}

// One meeting's cleanup must never touch another's.
func TestRemoveAudioDirLeavesOtherMeetingsAlone(t *testing.T) {
	root := t.TempDir()
	mine, theirs := uuid.New(), uuid.New()
	seedMeetingDir(t, root, mine)
	other := seedMeetingDir(t, root, theirs, "000000.webm")

	svc := &Service{audioDir: root}
	svc.removeAudioDir(mine)

	if _, err := os.Stat(other); err != nil {
		t.Fatal("cleanup crossed into another meeting:", err)
	}
}

func TestRemoveAudioDirIsSafeWhenNothingIsThere(t *testing.T) {
	svc := &Service{audioDir: t.TempDir()}
	svc.removeAudioDir(uuid.New())
}

// The in-memory queue loses its jobs on restart, so leftover audio has nothing
// left to process it.
func TestSweepAudioDirClearsEverythingAtStartup(t *testing.T) {
	root := t.TempDir()
	seedMeetingDir(t, root, uuid.New(), "000000.webm")
	seedMeetingDir(t, root, uuid.New())
	if err := os.WriteFile(filepath.Join(root, "stray.webm"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	SweepAudioDir(root, false)

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("sweep left %d entries behind", len(entries))
	}
	// The directory itself must survive; the service writes into it next.
	if _, err := os.Stat(root); err != nil {
		t.Fatal("sweep removed the audio root:", err)
	}
}

func TestSweepAudioDirHonoursKeepAudio(t *testing.T) {
	root := t.TempDir()
	seedMeetingDir(t, root, uuid.New(), "000000.webm")

	SweepAudioDir(root, true)

	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("KEEP_AUDIO must preserve audio across restarts, got %d entries", len(entries))
	}
}

func TestSweepAudioDirIsSafeWhenDirectoryIsMissing(t *testing.T) {
	SweepAudioDir(filepath.Join(t.TempDir(), "does-not-exist"), false)
}

// Regression: an over-long language_used aborted the finalize write and left
// the meeting with no stored transcript. The clamp is the last line of defence
// behind normalizeLanguage.
func TestClampLanguage(t *testing.T) {
	long := strings.Repeat("a", 200)
	if got := clampLanguage(&long); len([]rune(*got)) != maxLanguageLen {
		t.Errorf("clamped to %d runes, want %d", len([]rune(*got)), maxLanguageLen)
	}

	short := "id"
	if got := clampLanguage(&short); *got != "id" {
		t.Errorf("short value was altered: %q", *got)
	}

	if clampLanguage(nil) != nil {
		t.Error("nil must stay nil")
	}

	// Multi-byte text must be cut on rune boundaries, not bytes.
	unicode := strings.Repeat("é", 200)
	got := clampLanguage(&unicode)
	if len([]rune(*got)) != maxLanguageLen {
		t.Errorf("unicode clamped to %d runes, want %d", len([]rune(*got)), maxLanguageLen)
	}
	if !utf8.ValidString(*got) {
		t.Error("clamping produced invalid UTF-8")
	}
}
