package storage

import (
	"strconv"
	"time"
)

// === Redis-совместимые операции ===

// Exists проверяет существование ключей. Возвращает количество найденных.
func (c *Cache) Exists(keys ...string) int64 {
	var count int64
	for _, key := range keys {
		s := c.getShard(key)
		if s.exists(key) {
			count++
		} else if c.cold != nil {
			if _, found := c.cold.Get(key); found {
				count++
			}
		}
	}
	return count
}

// Expire устанавливает TTL на существующий ключ.
func (c *Cache) Expire(key string, ttl time.Duration) bool {
	s := c.getShard(key)
	expireAt := time.Now().Add(ttl).UnixNano()
	return s.expire(key, expireAt)
}

// Persist убирает TTL с ключа (делает его вечным).
func (c *Cache) Persist(key string) bool {
	s := c.getShard(key)
	return s.expire(key, 0)
}

// GetTTL возвращает оставшееся время жизни в секундах.
// -1 = ключ без TTL, -2 = ключ не найден.
func (c *Cache) GetTTL(key string) int64 {
	s := c.getShard(key)
	ns := s.ttl(key)
	switch ns {
	case -1, -2:
		return ns
	default:
		return ns / int64(time.Second)
	}
}

// GetPTTL возвращает оставшееся время жизни в миллисекундах.
func (c *Cache) GetPTTL(key string) int64 {
	s := c.getShard(key)
	ns := s.ttl(key)
	switch ns {
	case -1, -2:
		return ns
	default:
		return ns / int64(time.Millisecond)
	}
}

// IncrBy атомарно добавляет delta к значению ключа. Возвращает новое значение.
func (c *Cache) IncrBy(key string, delta int64) (int64, error) {
	s := c.getShard(key)
	result, isNew, err := s.incrBy(key, delta)
	if err != nil {
		return 0, err
	}
	if isNew {
		c.totalKeys.Add(1)
	}
	c.persister.Write("SET", key, strconv.FormatInt(result, 10), 0)
	return result, nil
}

// Append дописывает к значению ключа. Возвращает новую длину.
func (c *Cache) Append(key, suffix string) int {
	s := c.getShard(key)
	length, isNew := s.appendVal(key, suffix)
	if isNew {
		c.totalKeys.Add(1)
		c.persister.Write("SET", key, suffix, 0)
	} else {
		val, _ := s.get(key)
		c.persister.Write("SET", key, val, 0)
	}
	return length
}

// Strlen возвращает длину строки.
func (c *Cache) Strlen(key string) int {
	s := c.getShard(key)
	return s.strlen(key)
}

// MGet пакетное чтение.
func (c *Cache) MGet(keys ...string) []struct {
	Value string
	Found bool
} {
	result := make([]struct {
		Value string
		Found bool
	}, len(keys))
	for i, key := range keys {
		val, found := c.Get(key)
		result[i].Value = val
		result[i].Found = found
	}
	return result
}

// MSet пакетная запись.
func (c *Cache) MSet(pairs ...string) {
	for i := 0; i+1 < len(pairs); i += 2 {
		c.Set(pairs[i], pairs[i+1], 0, false)
	}
}

// Keys возвращает все ключи, matching glob pattern.
func (c *Cache) Keys(pattern string) []string {
	var result []string
	for i := 0; i < shardCount; i++ {
		result = append(result, c.shards[i].keys(pattern)...)
	}
	return result
}

// FlushDB очищает все данные.
func (c *Cache) FlushDB() {
	for i := 0; i < shardCount; i++ {
		s := c.shards[i]
		s.Lock()
		s.items = make(map[string]*Item)
		s.pq = make(priorityQueue, 0)
		s.Unlock()
	}
	c.totalKeys.Store(0)

	if c.cold != nil {
		c.cold.FlushAll()
	}
}

// Rename переименовывает ключ. Thread-safe для кросс-шардного случая.
func (c *Cache) Rename(oldKey, newKey string) bool {
	sSrc := c.getShard(oldKey)
	item, found := sSrc.getItem(oldKey)
	if !found {
		return false
	}

	val := item.Value
	exp := item.ExpireAt

	sSrc.del(oldKey)
	c.totalKeys.Add(-1)

	sDst := c.getShard(newKey)
	isNew := sDst.set(newKey, val, exp)
	if isNew {
		c.totalKeys.Add(1)
	}

	c.persister.Write("DEL", oldKey, "", 0)

	var ttl time.Duration
	if exp > 0 {
		ttl = time.Duration(exp-time.Now().UnixNano()) * time.Nanosecond
	}
	c.persister.Write("SET", newKey, val, ttl)

	return true
}

// Type возвращает тип ключа.
func (c *Cache) Type(key string) string {
	s := c.getShard(key)
	if s.exists(key) {
		return "string"
	}
	return "none"
}
