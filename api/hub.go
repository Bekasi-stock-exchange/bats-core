package api

import "sync"

// hub is a minimal fan-out for WebSocket subscribers, keyed by emiten id. Each
// subscriber owns a buffered channel of book snapshots. The flow is strictly
// one-way: the order handler calls broadcast; subscribers only receive.
type hub struct {
	mu   sync.Mutex
	subs map[int64]map[*subscriber]struct{}
}

type subscriber struct {
	ch chan bookSnapshot
}

func newHub() *hub {
	return &hub{subs: make(map[int64]map[*subscriber]struct{})}
}

// subscribe registers a new subscriber for an emiten and returns it.
func (h *hub) subscribe(emitenID int64) *subscriber {
	sub := &subscriber{ch: make(chan bookSnapshot, 16)}
	h.mu.Lock()
	if h.subs[emitenID] == nil {
		h.subs[emitenID] = make(map[*subscriber]struct{})
	}
	h.subs[emitenID][sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// unsubscribe removes a subscriber and closes its channel.
func (h *hub) unsubscribe(emitenID int64, sub *subscriber) {
	h.mu.Lock()
	if set := h.subs[emitenID]; set != nil {
		delete(set, sub)
		if len(set) == 0 {
			delete(h.subs, emitenID)
		}
	}
	h.mu.Unlock()
}

// broadcast pushes a snapshot to every subscriber of an emiten. A slow consumer
// whose buffer is full is skipped rather than blocking the matching path.
func (h *hub) broadcast(emitenID int64, snap bookSnapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[emitenID] {
		select {
		case sub.ch <- snap:
		default: // drop update for a subscriber that can't keep up
		}
	}
}
