package janitor

import "imcs/internal/storage/cache"

// New создаёт janitor.
func New(cache *storage.Cache) *Janitor {
	return &Janitor{
		cache:  cache,
		stopCh: make(chan struct{}),
	}
}