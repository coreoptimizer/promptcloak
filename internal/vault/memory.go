package vault

import (
	"context"
	"sync"
	"time"
)

// Memory is an in-process Vault with TTL expiry. It is intended for local
// development and tests. It does NOT survive restarts and is not shared across
// replicas, so a token created on one pod cannot be reversed on another.
type Memory struct {
	mu   sync.RWMutex
	m    map[string]memEntry
	ttl  time.Duration
	now  func() time.Time // injectable for tests
	stop chan struct{}
}

type memEntry struct {
	value string
	exp   time.Time
}

// NewMemory returns a Memory vault and starts a background janitor that evicts
// expired entries. Call Close to stop it.
func NewMemory(ttl time.Duration) *Memory {
	m := &Memory{
		m:    make(map[string]memEntry),
		ttl:  ttl,
		now:  time.Now,
		stop: make(chan struct{}),
	}
	go m.janitor()
	return m
}

func (m *Memory) Put(_ context.Context, token, value string) error {
	m.mu.Lock()
	m.m[token] = memEntry{value: value, exp: m.now().Add(m.ttl)}
	m.mu.Unlock()
	return nil
}

func (m *Memory) Get(_ context.Context, token string) (string, bool, error) {
	m.mu.RLock()
	e, ok := m.m[token]
	m.mu.RUnlock()
	if !ok {
		return "", false, nil
	}
	if m.now().After(e.exp) {
		m.mu.Lock()
		// Re-check under the write lock before deleting to avoid racing a Put.
		if cur, ok := m.m[token]; ok && m.now().After(cur.exp) {
			delete(m.m, token)
		}
		m.mu.Unlock()
		return "", false, nil
	}
	return e.value, true, nil
}

func (m *Memory) Close() error {
	close(m.stop)
	return nil
}

func (m *Memory) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			now := m.now()
			m.mu.Lock()
			for k, e := range m.m {
				if now.After(e.exp) {
					delete(m.m, k)
				}
			}
			m.mu.Unlock()
		}
	}
}
