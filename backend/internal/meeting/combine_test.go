package meeting

import (
	"testing"

	"github.com/google/uuid"
)

// The same meeting folded in twice would duplicate that stretch of the
// discussion in the combined transcript.
func TestDedupeDropsRepeatsAndKeepsOrder(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	got := dedupe([]uuid.UUID{a, b, a, c, b, a})

	want := []uuid.UUID{a, b, c}
	if len(got) != len(want) {
		t.Fatalf("dedupe returned %d ids, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDedupeHandlesEmptyAndSingle(t *testing.T) {
	if got := dedupe(nil); len(got) != 0 {
		t.Errorf("dedupe(nil) = %v", got)
	}

	id := uuid.New()
	if got := dedupe([]uuid.UUID{id, id, id}); len(got) != 1 {
		t.Errorf("dedupe collapsed to %d ids, want 1", len(got))
	}
}

// Combining is only worth doing across several meetings; one is just a
// re-summarize, and zero is a mistake.
func TestCombineRejectsFewerThanTwo(t *testing.T) {
	svc := &Service{}
	id := uuid.New()

	for name, ids := range map[string][]uuid.UUID{
		"none":       nil,
		"one":        {id},
		"same twice": {id, id},
	} {
		if _, err := svc.Combine(t.Context(), ids, ""); err != ErrTooFewMeetings {
			t.Errorf("%s: got %v, want ErrTooFewMeetings", name, err)
		}
	}
}

// The guard must run before anything touches the database.
func TestCombineRejectsTooManyBeforeLoading(t *testing.T) {
	svc := &Service{}

	ids := make([]uuid.UUID, MaxCombinedMeetings+1)
	for i := range ids {
		ids[i] = uuid.New()
	}

	if _, err := svc.Combine(t.Context(), ids, ""); err == nil {
		t.Fatal("expected a limit error")
	}
}
