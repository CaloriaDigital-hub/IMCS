package storage

import (
	"container/heap"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

const shardCount = 64

func newShard() *shard {
	s := &shard{
		items: make(map[string]*Item),
		pq:    make(priorityQueue, 0),
	}
	heap.Init(&s.pq)
	return s
}

// getShard возвращает шард для данного ключа по FNV-хешу.
func (c *Cache) getShard(key string) *shard {
	h := fnv.New32a()
	h.Write([]byte(key))
	return c.shards[h.Sum32()&(shardCount-1)]
}

// set записывает значение в шард. Возвращает true, если ключ новый.
func (s *shard) set(key, value string, expireAt int64) bool {
	s.Lock()
	defer s.Unlock()

	now := nowCached()

	if item, exist := s.items[key]; exist {
		item.Value = value
		atomic.StoreInt64(&item.ExpireAt, expireAt)
		atomic.StoreInt64(&item.LastAccess, now)
		// Обновляем heap
		if expireAt > 0 {
			if item.HeapIndex >= 0 {
				heap.Fix(&s.pq, item.HeapIndex)
			} else {
				heap.Push(&s.pq, item)
			}
		} else if item.HeapIndex >= 0 {
			heap.Remove(&s.pq, item.HeapIndex)
		}
		return false
	}

	item := &Item{
		Key:        key,
		Value:      value,
		ExpireAt:   expireAt,
		LastAccess: now,
		HeapIndex:  -1,
	}
	s.items[key] = item
	if expireAt > 0 {
		heap.Push(&s.pq, item)
	}
	return true
}

// get возвращает значение из шарда. Если ключ истёк — удаляет его (lazy expiry).
func (s *shard) get(key string) (string, bool) {
	s.RLock()
	item, exists := s.items[key]
	if !exists {
		s.RUnlock()
		return "", false
	}

	// Читаем всё под RLock — race-safe
	expireAt := atomic.LoadInt64(&item.ExpireAt)
	if expireAt > 0 && time.Now().UnixNano() > expireAt {
		// Ключ протух — нужен write lock для удаления
		s.RUnlock()
		s.Lock()
		item, exists = s.items[key]
		if exists && item.IsExpired() {
			delete(s.items, key)
			if item.HeapIndex >= 0 {
				heap.Remove(&s.pq, item.HeapIndex)
			}
			s.Unlock()
			return "", false
		}
		// Ключ обновили пока ждали Lock — вернём актуальное значение
		if !exists {
			s.Unlock()
			return "", false
		}
		val := item.Value
		atomic.StoreInt64(&item.LastAccess, nowCached())
		s.Unlock()
		return val, true
	}

	// Fast path: не протух — копируем и возвращаем
	val := item.Value
	atomic.StoreInt64(&item.LastAccess, nowCached())
	s.RUnlock()
	return val, true
}

// del удаляет ключ из шарда. Возвращает true, если ключ был удалён.
func (s *shard) del(key string) bool {
	s.Lock()
	defer s.Unlock()

	item, exists := s.items[key]
	if !exists {
		return false
	}

	delete(s.items, key)
	if item.HeapIndex >= 0 {
		heap.Remove(&s.pq, item.HeapIndex)
	}
	return true
}

// exists проверяет существование ключа (с учётом TTL).
func (s *shard) exists(key string) bool {
	s.RLock()
	item, exists := s.items[key]
	if !exists {
		s.RUnlock()
		return false
	}
	expireAt := atomic.LoadInt64(&item.ExpireAt)
	s.RUnlock()

	if expireAt > 0 && time.Now().UnixNano() > expireAt {
		return false
	}
	return true
}

// expire устанавливает TTL на существующий ключ. Возвращает true если ключ найден.
func (s *shard) expire(key string, expireAt int64) bool {
	s.Lock()
	defer s.Unlock()

	item, exists := s.items[key]
	if !exists || item.IsExpired() {
		return false
	}

	atomic.StoreInt64(&item.ExpireAt, expireAt)
	if expireAt > 0 {
		if item.HeapIndex >= 0 {
			heap.Fix(&s.pq, item.HeapIndex)
		} else {
			heap.Push(&s.pq, item)
		}
	} else if item.HeapIndex >= 0 {
		heap.Remove(&s.pq, item.HeapIndex)
	}
	return true
}

// ttl возвращает оставшееся время жизни в наносекундах.
// -1 = ключ без TTL, -2 = ключ не найден.
func (s *shard) ttl(key string) int64 {
	s.RLock()
	item, exists := s.items[key]
	if !exists {
		s.RUnlock()
		return -2
	}
	expireAt := atomic.LoadInt64(&item.ExpireAt)
	s.RUnlock()

	if expireAt > 0 && time.Now().UnixNano() > expireAt {
		return -2
	}
	if expireAt == 0 {
		return -1
	}
	remaining := expireAt - time.Now().UnixNano()
	if remaining <= 0 {
		return -2
	}
	return remaining
}

// incrBy атомарно инкрементирует значение ключа. Возвращает новое значение.
// Если ключ не существует — создаёт с 0 + delta.
// Ошибка если значение не число.
func (s *shard) incrBy(key string, delta int64) (int64, bool, error) {
	s.Lock()
	defer s.Unlock()

	now := nowCached()
	item, exists := s.items[key]

	if exists && item.IsExpired() {
		delete(s.items, key)
		if item.HeapIndex >= 0 {
			heap.Remove(&s.pq, item.HeapIndex)
		}
		exists = false
	}

	var current int64
	isNew := false

	if exists {
		var err error
		current, err = strconv.ParseInt(item.Value, 10, 64)
		if err != nil {
			return 0, false, err
		}
		current += delta
		item.Value = strconv.FormatInt(current, 10)
		atomic.StoreInt64(&item.LastAccess, now)
	} else {
		current = delta
		isNew = true
		newItem := &Item{
			Key:        key,
			Value:      strconv.FormatInt(current, 10),
			LastAccess: now,
			HeapIndex:  -1,
		}
		s.items[key] = newItem
	}

	return current, isNew, nil
}

// appendVal дописывает к значению ключа. Возвращает новую длину.
func (s *shard) appendVal(key, suffix string) (int, bool) {
	s.Lock()
	defer s.Unlock()

	now := nowCached()
	item, exists := s.items[key]

	if exists && item.IsExpired() {
		delete(s.items, key)
		if item.HeapIndex >= 0 {
			heap.Remove(&s.pq, item.HeapIndex)
		}
		exists = false
	}

	if exists {
		item.Value += suffix
		atomic.StoreInt64(&item.LastAccess, now)
		return len(item.Value), false
	}

	newItem := &Item{
		Key:        key,
		Value:      suffix,
		LastAccess: now,
		HeapIndex:  -1,
	}
	s.items[key] = newItem
	return len(suffix), true
}

// strlen возвращает длину строки.
func (s *shard) strlen(key string) int {
	s.RLock()
	item, exists := s.items[key]
	if !exists {
		s.RUnlock()
		return 0
	}
	expireAt := atomic.LoadInt64(&item.ExpireAt)
	vlen := len(item.Value)
	s.RUnlock()

	if expireAt > 0 && time.Now().UnixNano() > expireAt {
		return 0
	}
	return vlen
}

// keys возвращает ключи в этом шарде, matching pattern.
func (s *shard) keys(pattern string) []string {
	s.RLock()
	defer s.RUnlock()

	var result []string
	for key, item := range s.items {
		if item.IsExpired() {
			continue
		}
		if pattern == "*" {
			result = append(result, key)
		} else {
			matched, _ := filepath.Match(pattern, key)
			if matched {
				result = append(result, key)
			}
		}
	}
	return result
}

// rename переименовывает ключ. Атомарно внутри одного шарда
// Для кросс-шардного rename используется Cache.Rename
func (s *shard) getItem(key string) (*Item, bool) {
	s.RLock()
	item, exists := s.items[key]
	s.RUnlock()

	if !exists || item.IsExpired() {
		return nil, false
	}
	return item, true
}
