// Package sse fans meeting events out to every browser watching a meeting.
package sse

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// Event names the frontend listens for (PRD §24).
const (
	EventTranscript      = "transcript"
	EventChunkProcessing = "chunk_processing"
	EventMeetingStatus   = "meeting_status"
	EventError           = "error"
)

type Event struct {
	Name string
	Data json.RawMessage
}

// bufferSize is per-subscriber. A browser that stalls briefly (a backgrounded
// tab, a slow network) can fall this far behind before we give up on it.
const bufferSize = 64

type subscriber struct {
	ch chan Event
}

// Broker maps a meeting to the set of clients streaming it.
//
// Delivery is best-effort by design: SSE is a live convenience, and the
// database remains the source of truth. A reconnecting client refetches the
// meeting and uses chunk_index to discard anything it already has (PRD §24),
// so a dropped event is never data loss.
type Broker struct {
	mu     sync.RWMutex
	topics map[uuid.UUID]map[*subscriber]struct{}
}

func NewBroker() *Broker {
	return &Broker{topics: make(map[uuid.UUID]map[*subscriber]struct{})}
}

// Subscribe registers a client and returns its event channel plus the
// unsubscribe function the caller must defer.
func (b *Broker) Subscribe(meetingID uuid.UUID) (<-chan Event, func()) {
	sub := &subscriber{ch: make(chan Event, bufferSize)}

	b.mu.Lock()
	if b.topics[meetingID] == nil {
		b.topics[meetingID] = make(map[*subscriber]struct{})
	}
	b.topics[meetingID][sub] = struct{}{}
	b.mu.Unlock()

	return sub.ch, func() { b.unsubscribe(meetingID, sub) }
}

func (b *Broker) unsubscribe(meetingID uuid.UUID, sub *subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subs, ok := b.topics[meetingID]
	if !ok {
		return
	}
	if _, present := subs[sub]; !present {
		return
	}
	delete(subs, sub)
	close(sub.ch)

	if len(subs) == 0 {
		delete(b.topics, meetingID)
	}
}

// Publish delivers an event to everyone watching the meeting.
//
// It never blocks: a subscriber whose buffer is full has its event dropped
// rather than stalling transcription for everyone else.
func (b *Broker) Publish(meetingID uuid.UUID, name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("sse_marshal_failed", "event", name, "error", err)
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for sub := range b.topics[meetingID] {
		select {
		case sub.ch <- Event{Name: name, Data: data}:
		default:
			slog.Warn("sse_subscriber_lagging", "meeting_id", meetingID, "event", name)
		}
	}
}

// Subscribers reports how many clients are watching a meeting. Used by tests
// and diagnostics.
func (b *Broker) Subscribers(meetingID uuid.UUID) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.topics[meetingID])
}
