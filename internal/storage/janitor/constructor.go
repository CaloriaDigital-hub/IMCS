package janitor

import "github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"

// New создаёт janitor.
func New(cache *storage.Cache) *Janitor {
	return &Janitor{
		cache:  cache,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}
