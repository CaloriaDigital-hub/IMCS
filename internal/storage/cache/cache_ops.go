package storage

import (
	"math"
	"strconv"
	"sync/atomic"
	"time"
)

// === Redis-совместимые операции ===

// Exists возвращает число существующих ключей (повторы считаются, как в Redis).
func (c *Cache) Exists(keys ...string) int64 {
	now := time.Now().UnixNano()
	var n int64
	for _, key := range keys {
		s := c.shardFor(key)
		s.RLock()
		if s.peek(key, now) != nil {
			n++
		}
		s.RUnlock()
	}
	return n
}

// Expire ставит TTL существующему ключу. ttl <= 0 удаляет ключ (как в Redis).
func (c *Cache) Expire(key string, ttl time.Duration) (bool, error) {
	if err := c.writable(); err != nil {
		return false, err
	}
	now := time.Now().UnixNano()
	expireAt, err := expireAtFor(now, ttl)
	if err != nil {
		return false, err
	}

	s := c.shardFor(key)
	s.Lock()
	defer s.Unlock()

	it := s.live(key, now)
	if it == nil {
		return false, nil
	}
	if ttl <= 0 {
		s.remove(it)
		return true, c.log(logDel, key, "", 0)
	}
	s.setExpire(it, expireAt)
	return true, c.log(logExpireAt, key, "", expireAt)
}

// Persist убирает TTL. Возвращает false, если ключа нет или TTL не было.
func (c *Cache) Persist(key string) (bool, error) {
	if err := c.writable(); err != nil {
		return false, err
	}
	s := c.shardFor(key)
	s.Lock()
	defer s.Unlock()

	it := s.live(key, time.Now().UnixNano())
	if it == nil || it.ExpireAt == 0 {
		return false, nil
	}
	s.setExpire(it, 0)
	return true, c.log(logExpireAt, key, "", 0)
}

// remaining — оставшееся время жизни в ns; -1 = без TTL, -2 = нет ключа.
func (c *Cache) remaining(key string) int64 {
	now := time.Now().UnixNano()
	s := c.shardFor(key)
	s.RLock()
	defer s.RUnlock()

	it := s.peek(key, now)
	switch {
	case it == nil:
		return -2
	case it.ExpireAt == 0:
		return -1
	default:
		return it.ExpireAt - now
	}
}

// GetTTL — оставшееся время жизни в секундах (с округлением, как в Redis).
// -1 = без TTL, -2 = нет ключа.
func (c *Cache) GetTTL(key string) int64 {
	ns := c.remaining(key)
	if ns < 0 {
		return ns
	}
	ms := ns / int64(time.Millisecond)
	return (ms + 500) / 1000
}

// GetPTTL — оставшееся время жизни в миллисекундах. -1 / -2 как у GetTTL.
func (c *Cache) GetPTTL(key string) int64 {
	ns := c.remaining(key)
	if ns < 0 {
		return ns
	}
	return ns / int64(time.Millisecond)
}

// IncrBy атомарно прибавляет delta. Несуществующий ключ считается нулём.
func (c *Cache) IncrBy(key string, delta int64) (int64, error) {
	if err := c.writable(); err != nil {
		return 0, err
	}
	c.makeRoom(key)

	s := c.shardFor(key)
	s.Lock()
	defer s.Unlock()

	it := s.live(key, time.Now().UnixNano())
	var cur int64
	if it != nil {
		v, err := strconv.ParseInt(it.Value, 10, 64)
		if err != nil {
			return 0, ErrNotInteger
		}
		cur = v
	}
	if (delta > 0 && cur > math.MaxInt64-delta) || (delta < 0 && cur < math.MinInt64-delta) {
		return 0, ErrOverflow
	}
	cur += delta
	val := strconv.FormatInt(cur, 10)

	if it == nil {
		it = &Item{Key: key, Value: val, LastAccess: nowCached()}
		s.insert(it)
	} else {
		it.Value = val
		atomic.StoreInt64(&it.LastAccess, nowCached())
	}
	return cur, c.log(logSet, key, val, it.ExpireAt)
}

// Append дописывает suffix. Возвращает новую длину.
func (c *Cache) Append(key, suffix string) (int, error) {
	if err := c.writable(); err != nil {
		return 0, err
	}
	c.makeRoom(key)

	s := c.shardFor(key)
	s.Lock()
	defer s.Unlock()

	it := s.live(key, time.Now().UnixNano())
	if it == nil {
		it = &Item{Key: key, Value: suffix, LastAccess: nowCached()}
		s.insert(it)
	} else {
		it.Value += suffix
		atomic.StoreInt64(&it.LastAccess, nowCached())
	}
	return len(it.Value), c.log(logSet, key, it.Value, it.ExpireAt)
}

// Strlen — длина значения (0, если ключа нет).
func (c *Cache) Strlen(key string) int {
	s := c.shardFor(key)
	s.RLock()
	defer s.RUnlock()
	if it := s.peek(key, time.Now().UnixNano()); it != nil {
		return len(it.Value)
	}
	return 0
}

// MGet — пакетное чтение.
func (c *Cache) MGet(keys ...string) []GetResult {
	res := make([]GetResult, len(keys))
	for i, key := range keys {
		res[i].Value, res[i].Found = c.Get(key)
	}
	return res
}

// MSet атомарно записывает пары ключ-значение (TTL сбрасывается, как в Redis).
func (c *Cache) MSet(pairs ...string) error {
	if err := c.writable(); err != nil {
		return err
	}
	keys := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		keys = append(keys, pairs[i])
		c.makeRoom(pairs[i])
	}

	unlock := c.lockKeys(keys...)
	defer unlock()

	now := time.Now().UnixNano()
	var firstErr error
	for i := 0; i+1 < len(pairs); i += 2 {
		key, val := pairs[i], pairs[i+1]
		s := c.shardFor(key)
		if it := s.live(key, now); it != nil {
			it.Value = val
			s.setExpire(it, 0)
			atomic.StoreInt64(&it.LastAccess, nowCached())
		} else {
			s.insert(&Item{Key: key, Value: val, LastAccess: nowCached()})
		}
		if err := c.log(logSet, key, val, 0); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Keys возвращает ключи, подходящие под glob-паттерн Redis (*, ?, [...], \).
// O(N) и держит RLock каждого шарда на время обхода — как и KEYS в Redis,
// не для продакшн-трафика на больших базах.
func (c *Cache) Keys(pattern string) []string {
	now := time.Now().UnixNano()
	all := pattern == "*"
	var res []string
	for _, s := range c.shards {
		s.RLock()
		for key, it := range s.items {
			if !it.expired(now) && (all || globMatch(pattern, key)) {
				res = append(res, key)
			}
		}
		s.RUnlock()
	}
	return res
}

// FlushAll удаляет все ключи.
func (c *Cache) FlushAll() error {
	if err := c.writable(); err != nil {
		return err
	}
	c.lockAll()
	defer c.unlockAll()
	for _, s := range c.shards {
		s.clear()
	}
	// Под локами всех шардов: ни одна запись не встанет в журнал «вокруг» FLUSHALL.
	return c.log(logFlushAll, "", "", 0)
}

// Rename атомарно переименовывает ключ (TTL сохраняется, dst перезаписывается).
func (c *Cache) Rename(src, dst string) error {
	if err := c.writable(); err != nil {
		return err
	}
	unlock := c.lockKeys(src, dst)
	defer unlock()

	now := time.Now().UnixNano()
	ss := c.shardFor(src)
	it := ss.live(src, now)
	if it == nil {
		return ErrNoSuchKey
	}
	if src == dst {
		return nil
	}

	ds := c.shardFor(dst)
	if old := ds.live(dst, now); old != nil {
		ds.remove(old)
	}
	ss.remove(it)
	ds.insert(&Item{Key: dst, Value: it.Value, ExpireAt: it.ExpireAt, LastAccess: nowCached()})

	if err := c.log(logDel, src, "", 0); err != nil {
		return err
	}
	return c.log(logSet, dst, it.Value, it.ExpireAt)
}

// Type возвращает тип ключа: "string" или "none".
func (c *Cache) Type(key string) string {
	if c.Exists(key) > 0 {
		return "string"
	}
	return "none"
}
