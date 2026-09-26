package janitor

import (
	"sync"

	"github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"
)

// Janitor — фоновая очистка истёкших ключей.
type Janitor struct {
	cache   *storage.Cache
	stopCh  chan struct{}
	done    chan struct{}
	start   sync.Once
	stop    sync.Once
	started bool
}
