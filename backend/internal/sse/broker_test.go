package sse

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func receive(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestPublishReachesEverySubscriber(t *testing.T) {
	b := NewBroker()
	id := uuid.New()

	a, closeA := b.Subscribe(id)
	defer closeA()
	c, closeC := b.Subscribe(id)
	defer closeC()

	b.Publish(id, EventTranscript, map[string]any{"chunk_index": 3, "text": "halo"})

	for name, ch := range map[string]<-chan Event{"a": a, "c": c} {
		event := receive(t, ch)
		if event.Name != EventTranscript {
			t.Errorf("%s: event name = %q", name, event.Name)
		}

		var payload struct {
			ChunkIndex int    `json:"chunk_index"`
			Text       string `json:"text"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if payload.ChunkIndex != 3 || payload.Text != "halo" {
			t.Errorf("%s: payload = %+v", name, payload)
		}
	}
}

// Two meetings running at once must not see each other's transcripts.
func TestPublishIsScopedToOneMeeting(t *testing.T) {
	b := NewBroker()
	mine, closeMine := b.Subscribe(uuid.New())
	defer closeMine()

	b.Publish(uuid.New(), EventTranscript, map[string]any{"text": "bukan punyamu"})

	select {
	case e := <-mine:
		t.Fatalf("received another meeting's event: %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUnsubscribeClosesChannelAndDropsTopic(t *testing.T) {
	b := NewBroker()
	id := uuid.New()

	ch, unsubscribe := b.Subscribe(id)
	if got := b.Subscribers(id); got != 1 {
		t.Fatalf("subscribers = %d, want 1", got)
	}

	unsubscribe()

	if _, open := <-ch; open {
		t.Error("channel should be closed after unsubscribe")
	}
	if got := b.Subscribers(id); got != 0 {
		t.Errorf("subscribers = %d, want 0", got)
	}
}

// Unsubscribing twice happens naturally: the handler defers it and the request
// context can fire first. It must not panic on a double close.
func TestUnsubscribeIsIdempotent(t *testing.T) {
	b := NewBroker()
	id := uuid.New()

	_, unsubscribe := b.Subscribe(id)
	unsubscribe()
	unsubscribe()
}

// A stalled browser must never block transcription for everyone else.
func TestSlowSubscriberIsDroppedNotBlocking(t *testing.T) {
	b := NewBroker()
	id := uuid.New()

	_, unsubscribe := b.Subscribe(id)
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more than the per-subscriber buffer, never read.
		for i := 0; i < bufferSize*3; i++ {
			b.Publish(id, EventTranscript, map[string]any{"chunk_index": i})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}
}

func TestPublishToNobodyIsSafe(t *testing.T) {
	NewBroker().Publish(uuid.New(), EventMeetingStatus, map[string]any{"status": "COMPLETED"})
}
