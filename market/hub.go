package market

import "sync"

// Hub is a minimal fan-out of book state to subscribers, keyed by emiten id.
// Each subscriber owns a buffered channel. The flow is strictly one-way: the
// order domain publishes, the orderbook domain's WebSocket controller receives.
type Hub struct {
	mu   sync.Mutex
	subs map[int64]map[*Subscription]struct{}
}

// Subscription is a single subscriber's delivery channel.
type Subscription struct {
	ch chan BookState
}

// Updates is the receive-only stream of book states for this subscription.
func (s *Subscription) Updates() <-chan BookState { return s.ch }

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[int64]map[*Subscription]struct{})}
}

// Subscribe registers a new subscriber for an emiten and returns it.
func (h *Hub) Subscribe(emitenID int64) *Subscription {
	sub := &Subscription{ch: make(chan BookState, 16)}
	h.mu.Lock()
	if h.subs[emitenID] == nil {
		h.subs[emitenID] = make(map[*Subscription]struct{})
	}
	h.subs[emitenID][sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// Unsubscribe removes a subscriber.
func (h *Hub) Unsubscribe(emitenID int64, sub *Subscription) {
	h.mu.Lock()
	if set := h.subs[emitenID]; set != nil {
		delete(set, sub)
		if len(set) == 0 {
			delete(h.subs, emitenID)
		}
	}
	h.mu.Unlock()
}

// Broadcast pushes state to every subscriber of an emiten. A slow consumer whose
// buffer is full is skipped rather than blocking the matching path.
func (h *Hub) Broadcast(emitenID int64, state BookState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[emitenID] {
		select {
		case sub.ch <- state:
		default: // drop the update for a subscriber that can't keep up
		}
	}
}
