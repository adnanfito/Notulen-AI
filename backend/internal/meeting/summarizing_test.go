package meeting

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func newTestService() *Service {
	return &Service{summarizing: make(map[uuid.UUID]struct{})}
}

func TestClaimSummaryIsExclusive(t *testing.T) {
	svc := newTestService()
	id := uuid.New()

	if !svc.claimSummary(id) {
		t.Fatal("first claim must succeed")
	}
	if svc.claimSummary(id) {
		t.Error("second claim must fail while the first holds it")
	}

	svc.releaseSummary(id)

	if !svc.claimSummary(id) {
		t.Error("claim must succeed again once released")
	}
}

func TestClaimsArePerMeeting(t *testing.T) {
	svc := newTestService()
	a, b := uuid.New(), uuid.New()

	if !svc.claimSummary(a) || !svc.claimSummary(b) {
		t.Fatal("different meetings must not block each other")
	}
}

// The bug this guards: a restart abandoned the goroutine but left the row at
// PROCESSING, and the retry endpoint read that status as proof work was under
// way — locking the meeting out of every retry, permanently.
func TestNothingIsSummarizingInAFreshProcess(t *testing.T) {
	svc := newTestService()

	if svc.isSummarizing(uuid.New()) {
		t.Fatal("a fresh process owns no summaries, whatever the database says")
	}
}

func TestReleasingSomethingUnclaimedIsSafe(t *testing.T) {
	newTestService().releaseSummary(uuid.New())
}

// Two requests arriving together must not both start summarizing.
func TestConcurrentClaimsElectOneWinner(t *testing.T) {
	svc := newTestService()
	id := uuid.New()

	const racers = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0

	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			if svc.claimSummary(id) {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if won != 1 {
		t.Errorf("%d goroutines claimed the same meeting, want exactly 1", won)
	}
}
