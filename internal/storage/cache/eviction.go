package storage

import (
	"container/heap"
	"sync/atomic"
	"time"

	"imcs/internal/storage/cold"
)

const (
	coldThreshold  = 5 * time.Minute  // данные старше 5 минут → на диск
	flushDiskEvery = 30 * time.Second // сброс cold store на диск
	flushWorkers   = 4                // кол-во горутин для выгрузки на диск
	flushChanSize  = 1024             // буфер канала
)

// HasCold возвращает true если cold storage инициализирован.
func (c *Cache) HasCold() bool {
	return c.cold != nil
}

// ExpireByTTL — O(1) per expired key. Использует min-heap.
// Лимит: до 128 ключей за тик (чтобы не блокировать шарды надолго).
func (c *Cache) ExpireByTTL() {
	now := time.Now().UnixNano()
	const maxPerShard = 128

	for i := 0; i < shardCount; i++ {
		s := c.shards[i]
		s.Lock()

		removed := 0
		for s.pq.Len() > 0 && removed < maxPerShard {
			top := s.pq[0]
			if top.ExpireAt > now {
				break
			}
			heap.Pop(&s.pq)
			delete(s.items, top.Key)
			c.totalKeys.Add(-1)
			removed++
		}

		s.Unlock()
	}
}

// EvictCold — sample-based cold eviction.
// Смотрим до 16 ключей в каждом шарде.
// Если ключ не использовался > coldThreshold — выгружаем в cold storage.
func (c *Cache) EvictCold() {
	if c.cold == nil {
		return
	}

	now := time.Now().UnixNano()
	coldDeadline := now - int64(coldThreshold)
	const sampleSize = 16

	for i := 0; i < shardCount; i++ {
		s := c.shards[i]
		s.Lock()

		sampled := 0
		for key, item := range s.items {
			if sampled >= sampleSize {
				break
			}
			sampled++

			access := atomic.LoadInt64(&item.LastAccess)
			if access < coldDeadline {
				select {
				case c.flushCh <- coldItem{
					key:      key,
					value:    item.Value,
					expireAt: item.ExpireAt,
				}:
					delete(s.items, key)
					if item.HeapIndex >= 0 {
						heap.Remove(&s.pq, item.HeapIndex)
					}
					c.totalKeys.Add(-1)
				default:
					// канал полон — пропускаем
				}
			}
		}

		s.Unlock()
	}
}

// FlushCold сбрасывает cold storage на диск.
func (c *Cache) FlushCold() {
	if c.cold != nil {
		c.cold.Flush()
	}
}

// evictLRU вытесняет один ключ с минимальным LastAccess.
// Если cold storage включён — выгружает на диск вместо удаления.
func (c *Cache) evictLRU() {
	var (
		victimKey   string
		victimValue string
		victimExp   int64
		victimShard *shard
		minAccess   int64 = 1<<63 - 1
	)

	for i := 0; i < shardCount; i++ {
		s := c.shards[i]
		s.RLock()

		sampled := 0
		for _, item := range s.items {
			access := atomic.LoadInt64(&item.LastAccess)
			if access < minAccess {
				minAccess = access
				victimKey = item.Key
				victimValue = item.Value
				victimExp = item.ExpireAt
				victimShard = s
			}
			sampled++
			if sampled >= 5 {
				break
			}
		}

		s.RUnlock()
	}

	if victimShard != nil {
		if victimShard.del(victimKey) {
			c.totalKeys.Add(-1)
			if c.cold != nil {
				c.cold.Put(victimKey, victimValue, victimExp)
			}
		}
	}
}

// flushWorker — горутина-worker для записи в cold storage.
func (c *Cache) flushWorker(_ int) {
	batch := make([]cold.Item, 0, 64)

	for item := range c.flushCh {
		batch = append(batch, cold.Item{
			Key:      item.key,
			Value:    item.value,
			ExpireAt: item.expireAt,
		})

		// Drain: собираем пачку
		drained := true
		for drained && len(batch) < 64 {
			select {
			case it, ok := <-c.flushCh:
				if !ok {
					drained = false
					break
				}
				batch = append(batch, cold.Item{
					Key:      it.key,
					Value:    it.value,
					ExpireAt: it.expireAt,
				})
			default:
				drained = false
			}
		}

		c.cold.PutBatch(batch)
		batch = batch[:0]
	}
}
