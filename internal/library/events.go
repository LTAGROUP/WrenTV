package library

import (
	"sync"
)

// Event is pushed to connected clients (server-sent events) so UIs update
// the moment something is added, matched or changes state.
type Event struct {
	Type      string `json:"type"` // library.updated, item.added, item.updated, scan.started, scan.finished, stream.updated, download.updated
	LibraryID string `json:"libraryId,omitempty"`
	ItemID    string `json:"itemId,omitempty"`
	Message   string `json:"message,omitempty"`
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan Event]struct{}{}} }

func (h *Hub) Subscribe() (chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // slow client: drop rather than block the scanner
		}
	}
}
