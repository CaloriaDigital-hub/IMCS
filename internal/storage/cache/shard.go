package storage

import (
	"container/heap"
	"sort"
	"sync/atomic"
)

const shardCount = 64

// Константы FNV-1a (32-bit).
const (
	fnvOffset32 uint32 = 2166136261
	fnvPrime32  uint32 = 16777619
)

func newShard(count *atomic.Int64) *shard {
	return &shard{
		items: make(map[string]*Item),
		count: count,
	}
}

// shardIndex — FNV-1a без аллокаций (hash/fnv аллоцирует hasher и копию ключа).
func shardIndex(key string) int {
	hash := fnvOffset32
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= fnvPrime32
	}
	return int(hash & (shardCount - 1))
}

func (c *Cache) shardFor(key string) *shard {
	return c.shards[shardIndex(key)]
}

// lockKeys берёт Lock всех шардов, которым принадлежат keys, в порядке индекса
// (единый порядок исключает дедлок). Возвращает функцию разблокировки.
func (c *Cache) lockKeys(keys ...string) func() {
	idx := make([]int, 0, len(keys))
	for _, k := range keys {
		idx = append(idx, shardIndex(k))
	}
	sort.Ints(idx)
	uniq := idx[:0]
	for i, v := range idx {
		if i == 0 || v != idx[i-1] {
			uniq = append(uniq, v)
		}
	}
	for _, i := range uniq {
		c.shards[i].Lock()
	}
	return func() {
		for i := len(uniq) - 1; i >= 0; i-- {
			c.shards[uniq[i]].Unlock()
		}
	}
}

func (c *Cache) lockAll() {
	for _, s := range c.shards {
		s.Lock()
	}
}

func (c *Cache) unlockAll() {
	for i := shardCount - 1; i >= 0; i-- {
		c.shards[i].Unlock()
	}
}

// peek — живой элемент или nil. Нужен хотя бы RLock. Истёкшие не удаляет.
func (s *shard) peek(key string, now int64) *Item {
	it := s.items[key]
	if it == nil || it.expired(now) {
		return nil
	}
	return it
}

// live — живой элемент или nil. Нужен Lock. Истёкший удаляет.
func (s *shard) live(key string, now int64) *Item {
	it := s.items[key]
	if it == nil {
		return nil
	}
	if it.expired(now) {
		s.remove(it)
		return nil
	}
	return it
}

// insert добавляет новый элемент. Нужен Lock.
func (s *shard) insert(it *Item) {
	it.HeapIndex = -1
	s.items[it.Key] = it
	if it.ExpireAt > 0 {
		heap.Push(&s.pq, it)
	}
	s.count.Add(1)
}

// remove удаляет элемент. Нужен Lock.
func (s *shard) remove(it *Item) {
	delete(s.items, it.Key)
	if it.HeapIndex >= 0 {
		heap.Remove(&s.pq, it.HeapIndex)
	}
	s.count.Add(-1)
}

// setExpire меняет TTL и поддерживает heap. Нужен Lock.
func (s *shard) setExpire(it *Item, expireAt int64) {
	it.ExpireAt = expireAt
	switch {
	case expireAt > 0 && it.HeapIndex >= 0:
		heap.Fix(&s.pq, it.HeapIndex)
	case expireAt > 0:
		heap.Push(&s.pq, it)
	case it.HeapIndex >= 0:
		heap.Remove(&s.pq, it.HeapIndex)
	}
}

// clear удаляет всё. Нужен Lock.
func (s *shard) clear() {
	s.count.Add(-int64(len(s.items)))
	s.items = make(map[string]*Item)
	s.pq = nil
}
