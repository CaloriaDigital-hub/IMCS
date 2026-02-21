package janitor

import "imcs/internal/storage"

// Janitor — фоновый сборщик: TTL expiry, cold eviction, cold flush.
type Janitor struct {
	cache  *storage.Cache
	stopCh chan struct{}
}
