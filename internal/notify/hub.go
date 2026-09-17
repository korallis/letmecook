// Package notify provides coalescing, in-process wakeups. Notifications are hints;
// consumers must re-read durable state, including after missed notifications.
package notify

import "sync"

// Hub is safe for concurrent use. Its zero value is ready to use.
type Hub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan struct{}]struct{}
}

// Subscribe watches a key until cancel is called. Cancel is idempotent and closes
// the channel. An empty key watches all notifications; no goroutine is allocated.
func (h *Hub) Subscribe(key string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	if h == nil {
		close(ch)
		return ch, func() {}
	}
	h.mu.Lock()
	if h.subscribers == nil {
		h.subscribers = make(map[string]map[chan struct{}]struct{})
	}
	if h.subscribers[key] == nil {
		h.subscribers[key] = make(map[chan struct{}]struct{})
	}
	h.subscribers[key][ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.subscribers[key], ch)
			if len(h.subscribers[key]) == 0 {
				delete(h.subscribers, key)
			}
			close(ch)
		})
	}
}

// Notify wakes subscribers without blocking; repeated wakeups are coalesced.
func (h *Hub) Notify(key string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	send := func(group map[chan struct{}]struct{}) {
		for ch := range group {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
	send(h.subscribers[key])
	if key != "" {
		send(h.subscribers[""])
	}
}
