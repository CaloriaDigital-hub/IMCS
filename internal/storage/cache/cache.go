package storage

import (
	"fmt"
	"sync/atomic"
	"time"
)

// New создаёт кеш без лимита ключей. p == nil — без персистенции.
func New(p Persistence) *Cache {
	return NewWithMaxKeys(p, 0)
}

// NewWithMaxKeys создаёт кеш с лимитом ключей (0 = без лимита).
// При достижении лимита вытесняются давно не использованные ключи (sampled LRU).
func NewWithMaxKeys(p Persistence, maxKeys int64) *Cache {
	if p == nil {
		p = nopPersistence{}
	}
	c := &Cache{persister: p, maxKeys: maxKeys}
	for i := range c.shards {
		c.shards[i] = newShard(&c.totalKeys)
	}
	return c
}

// writable проверяет, что журнал принимает записи. Вызывается до изменения
// памяти, чтобы при сбое диска не расходились память и журнал.
func (c *Cache) writable() error {
	if err := c.persister.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	return nil
}

// log пишет запись в журнал. Вызывать под локом шарда ключа.
func (c *Cache) log(cmd, key, value string, expireAt int64) error {
	if err := c.persister.Write(cmd, key, value, expireAt); err != nil {
		return fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	return nil
}

// expireAtFor переводит TTL в абсолютное время.
func expireAtFor(now int64, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return 0, nil
	}
	at := now + int64(ttl)
	if at < now {
		return 0, ErrInvalidExpire
	}
	return at, nil
}

// Set записывает значение. Возвращает false, если не выполнено условие NX/XX.
func (c *Cache) Set(key, value string, opt SetOptions) (bool, error) {
	if err := c.writable(); err != nil {
		return false, err
	}
	now := time.Now().UnixNano()
	expireAt, err := expireAtFor(now, opt.TTL)
	if err != nil {
		return false, err
	}
	if !opt.XX {
		c.makeRoom(key)
	}

	s := c.shardFor(key)
	s.Lock()
	defer s.Unlock()

	it := s.live(key, now)
	if (opt.NX && it != nil) || (opt.XX && it == nil) {
		return false, nil
	}
	if it == nil {
		it = &Item{Key: key, Value: value, ExpireAt: expireAt, LastAccess: nowCached()}
		s.insert(it)
	} else {
		it.Value = value
		if !opt.KeepTTL {
			s.setExpire(it, expireAt)
		}
		atomic.StoreInt64(&it.LastAccess, nowCached())
	}
	return true, c.log(logSet, key, value, it.ExpireAt)
}

// Get возвращает значение по ключу.
func (c *Cache) Get(key string) (string, bool) {
	s := c.shardFor(key)
	s.RLock()
	it := s.peek(key, time.Now().UnixNano())
	if it == nil {
		s.RUnlock()
		return "", false
	}
	val := it.Value
	touch(it)
	s.RUnlock()
	return val, true
}

// touch обновляет LastAccess, не трогая кеш-линию без необходимости.
func touch(it *Item) {
	now := nowCached()
	if atomic.LoadInt64(&it.LastAccess) != now {
		atomic.StoreInt64(&it.LastAccess, now)
	}
}

// Delete удаляет ключи. Возвращает число удалённых.
func (c *Cache) Delete(keys ...string) (int64, error) {
	if err := c.writable(); err != nil {
		return 0, err
	}
	now := time.Now().UnixNano()
	var n int64
	var firstErr error
	for _, key := range keys {
		s := c.shardFor(key)
		s.Lock()
		if it := s.live(key, now); it != nil {
			s.remove(it)
			n++
			if err := c.log(logDel, key, "", 0); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		s.Unlock()
	}
	return n, firstErr
}

// CountKeys — число ключей в памяти (включая истёкшие, но ещё не убранные janitor'ом).
func (c *Cache) CountKeys() int64 {
	return c.totalKeys.Load()
}

// CountExpires — число ключей с TTL.
func (c *Cache) CountExpires() int64 {
	var n int64
	for _, s := range c.shards {
		s.RLock()
		n += int64(len(s.pq))
		s.RUnlock()
	}
	return n
}

// Apply применяет запись журнала при загрузке: без записи в журнал и без вытеснения.
func (c *Cache) Apply(cmd, key, value string, expireAt int64) {
	now := time.Now().UnixNano()

	if cmd == logFlushAll {
		c.lockAll()
		for _, s := range c.shards {
			s.clear()
		}
		c.unlockAll()
		return
	}

	s := c.shardFor(key)
	s.Lock()
	defer s.Unlock()

	it := s.live(key, now)
	switch cmd {
	case logSet:
		if expireAt > 0 && expireAt <= now {
			// Уже истёк — но он перекрывает предыдущее значение.
			if it != nil {
				s.remove(it)
			}
			return
		}
		if it == nil {
			s.insert(&Item{Key: key, Value: value, ExpireAt: expireAt, LastAccess: nowCached()})
		} else {
			it.Value = value
			s.setExpire(it, expireAt)
		}
	case logDel:
		if it != nil {
			s.remove(it)
		}
	case logExpireAt:
		switch {
		case it == nil:
		case expireAt > 0 && expireAt <= now:
			s.remove(it)
		default:
			s.setExpire(it, expireAt)
		}
	}
	// Прочие команды старого формата (GET/INCR/...) старый код в журнал не писал.
}

// Snapshot вызывает emit("SET", ...) для каждого живого ключа (для AOF rewrite).
// Шард блокируется только на время копирования ссылок.
func (c *Cache) Snapshot(emit func(cmd, key, value string, expireAt int64)) {
	type entry struct {
		key, value string
		expireAt   int64
	}
	var buf []entry
	for _, s := range c.shards {
		now := time.Now().UnixNano()
		buf = buf[:0]
		s.RLock()
		for _, it := range s.items {
			if !it.expired(now) {
				buf = append(buf, entry{it.Key, it.Value, it.ExpireAt})
			}
		}
		s.RUnlock()
		for _, e := range buf {
			emit(logSet, e.key, e.value, e.expireAt)
		}
	}
}
