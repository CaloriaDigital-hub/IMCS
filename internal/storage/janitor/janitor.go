package janitor

import (
	"imcs/internal/storage"
	"time"
)

// Janitor — фоновый сборщик: TTL expiry, cold eviction, cold flush.
type Janitor struct {
	cache  *storage.Cache
	stopCh chan struct{}
}

// New создаёт janitor.
func New(cache *storage.Cache) *Janitor {
	return &Janitor{
		cache:  cache,
		stopCh: make(chan struct{}),
	}
}

// Start запускает фоновые тикеры.
// TTL expiry: каждую секунду (O(1) через heap).
// Cold eviction: каждые 10 секунд (sample 16 ключей/шард).
// Cold flush: каждые 30 секунд (gob на диск).
func (j *Janitor) Start() {
	go j.run()
}

// Stop останавливает janitor.
func (j *Janitor) Stop() {
	close(j.stopCh)
}

func (j *Janitor) run() {
	ttlTicker := time.NewTicker(1 * time.Second)
	defer ttlTicker.Stop()

	coldTicker := time.NewTicker(10 * time.Second)
	defer coldTicker.Stop()

	var flushTicker *time.Ticker
	var flushCh <-chan time.Time

	for {
		// Динамическая инициализация flush ticker при появлении cold
		if j.cache.HasCold() && flushTicker == nil {
			flushTicker = time.NewTicker(30 * time.Second)
			flushCh = flushTicker.C
			defer flushTicker.Stop()
		}

		select {
		case <-ttlTicker.C:
			j.cache.ExpireByTTL()
		case <-coldTicker.C:
			j.cache.EvictCold()
		case <-flushCh:
			j.cache.FlushCold()
		case <-j.stopCh:
			return
		}
	}
}
