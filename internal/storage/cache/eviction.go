package storage

import (
	"math"
	"math/rand/v2"
	"sync/atomic"
	"time"
)

const (
	expirePerShard = 256 // ключей за один проход по шарду
	evictShards    = 4   // шардов в выборке LRU
	evictSamples   = 5   // ключей с каждого шарда (как maxmemory-samples в Redis)
)

// ExpireByTTL удаляет истёкшие ключи через min-heap (O(log n) на ключ).
//
// Шард держится под Lock не дольше expirePerShard удалений. Если хотя бы в
// одном шарде остались истёкшие ключи, проход повторяется, пока не выйдет budget —
// так скорость чистки подстраивается под поток истекающих ключей.
// Возвращает число удалённых ключей.
func (c *Cache) ExpireByTTL(budget time.Duration) int {
	start := time.Now()
	total := 0
	for {
		more := false
		for _, s := range c.shards {
			now := time.Now().UnixNano()
			s.Lock()
			n := 0
			for len(s.pq) > 0 && n < expirePerShard {
				top := s.pq[0]
				if top.ExpireAt > now {
					break
				}
				s.remove(top)
				n++
			}
			if n == expirePerShard {
				more = true
			}
			s.Unlock()
			total += n
		}
		if !more || time.Since(start) >= budget {
			return total
		}
	}
}

// makeRoom освобождает место под новый ключ, если достигнут лимит maxKeys.
// Вызывать без локов.
func (c *Cache) makeRoom(key string) {
	if c.maxKeys <= 0 || c.totalKeys.Load() < c.maxKeys {
		return
	}
	s := c.shardFor(key)
	s.RLock()
	_, exists := s.items[key]
	s.RUnlock()
	if exists {
		return
	}
	for i := 0; i < 32 && c.totalKeys.Load() >= c.maxKeys; i++ {
		if !c.evictOne() {
			return
		}
	}
}

// evictOne вытесняет один ключ (sampled LRU; истёкшие — в первую очередь).
// Вытеснение пишется в журнал как DEL, иначе ключ вернётся после рестарта.
func (c *Cache) evictOne() bool {
	now := time.Now().UnixNano()
	var (
		victim    *Item
		victimIdx int
		minAccess int64 = math.MaxInt64
	)

	sample := func(i int) {
		s := c.shards[i]
		s.RLock()
		n := 0
		for _, it := range s.items {
			access := atomic.LoadInt64(&it.LastAccess)
			if it.expired(now) {
				access = math.MinInt64
			}
			if access < minAccess {
				minAccess, victim, victimIdx = access, it, i
			}
			if n++; n >= evictSamples {
				break
			}
		}
		s.RUnlock()
	}

	start := rand.IntN(shardCount)
	for i := 0; i < evictShards; i++ {
		sample((start + i*(shardCount/evictShards)) % shardCount)
	}
	// Ключи могут быть сосредоточены в нескольких шардах — добираем полным обходом.
	for i := 0; victim == nil && i < shardCount; i++ {
		sample(i)
	}
	if victim == nil {
		return false
	}

	s := c.shards[victimIdx]
	s.Lock()
	defer s.Unlock()
	if s.items[victim.Key] != victim {
		return true // уже удалён или заменён — место, возможно, освободилось
	}
	s.remove(victim)
	// Если журнал отвалился, следующая запись всё равно получит ErrPersistence.
	_ = c.log(logDel, victim.Key, "", 0)
	return true
}
