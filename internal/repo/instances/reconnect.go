package instances

import (
	"errors"
	"sync"
	"time"
)

var ErrReconnectInProgress = errors.New("reconexão em andamento; aguarde alguns instantes")

var reconnectAttempts = struct {
	sync.Mutex
	active map[string]bool
	next   map[string]time.Time
}{active: make(map[string]bool), next: make(map[string]time.Time)}

func beginReconnect(key string) bool {
	reconnectAttempts.Lock()
	defer reconnectAttempts.Unlock()
	if reconnectAttempts.active[key] || time.Now().Before(reconnectAttempts.next[key]) {
		return false
	}
	reconnectAttempts.active[key] = true
	return true
}

func endReconnect(key string) {
	reconnectAttempts.Lock()
	defer reconnectAttempts.Unlock()
	delete(reconnectAttempts.active, key)
	reconnectAttempts.next[key] = time.Now().Add(30 * time.Second)
	for candidate, next := range reconnectAttempts.next {
		if time.Now().After(next) {
			delete(reconnectAttempts.next, candidate)
		}
	}
}
