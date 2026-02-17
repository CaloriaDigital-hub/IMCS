package storage

import (
	"errors"
	"log"
	"time"

	"imcs/internal/storage/cold"
)

// ErrKeyExist возвращается при SET NX, если ключ уже существует.
var ErrKeyExist = errors.New("key already exists")

// New создаёт шардированный кеш без лимита ключей.
func New(p Persistence) *Cache {
	return NewWithMaxKeys(p, 0)
}

// NewWithMaxKeys создаёт кеш с лимитом ключей.
func NewWithMaxKeys(p Persistence, maxKeys int64) *Cache {
	c := &Cache{
		persister: p,
		maxKeys:   maxKeys,
		flushCh:   make(chan coldItem, flushChanSize),
		stopCh:    make(chan struct{}),
	}

	for i := 0; i < shardCount; i++ {
		c.shards[i] = newShard()
	}

	return c
}

// InitColdStorage инициализирует cold storage в указанной директории.
func (c *Cache) InitColdStorage(dir string) error {
	store, err := cold.New(dir)
	if err != nil {
		return err
	}
	c.cold = store

	for i := 0; i < flushWorkers; i++ {
		go c.flushWorker(i)
	}

	return nil
}

// Set устанавливает значение ключа.
func (c *Cache) Set(key, value string, duration time.Duration, nx bool) error {
	var expireAt int64
	if duration > 0 {
		expireAt = time.Now().Add(duration).UnixNano()
	}

	s := c.getShard(key)

	if nx {
		s.RLock()
		item, found := s.items[key]
		s.RUnlock()

		if found && !item.IsExpired() {
			return ErrKeyExist
		}
	}

	if c.maxKeys > 0 {
		s.RLock()
		_, exists := s.items[key]
		s.RUnlock()

		if !exists && c.totalKeys.Load() >= c.maxKeys {
			c.evictLRU()
		}
	}

	isNew := s.set(key, value, expireAt)
	if isNew {
		c.totalKeys.Add(1)
	}

	if c.cold != nil {
		c.cold.Delete(key)
	}

	c.persister.Write("SET", key, value, duration)

	return nil
}

// Get возвращает значение по ключу (RAM → cold storage).
func (c *Cache) Get(key string) (string, bool) {
	s := c.getShard(key)
	val, found := s.get(key)
	if found {
		return val, true
	}

	if c.cold != nil {
		val, found = c.cold.Get(key)
		if found {
			s.set(key, val, 0)
			c.totalKeys.Add(1)
			c.cold.Delete(key)
			return val, true
		}
	}

	return "", false
}

// Delete удаляет ключ из RAM и cold storage.
func (c *Cache) Delete(key string) {
	s := c.getShard(key)
	if s.del(key) {
		c.totalKeys.Add(-1)
	}

	if c.cold != nil {
		c.cold.Delete(key)
	}

	c.persister.Write("DEL", key, "", 0)
}

// CountKeys возвращает общее число ключей в RAM.
func (c *Cache) CountKeys() int64 {
	return c.totalKeys.Load()
}

// Snapshot вызывает fn для каждого живого ключа (для AOF Rewrite).
func (c *Cache) Snapshot(fn func(cmd, key, value string, expireAt int64)) {
	now := time.Now().UnixNano()

	for i := 0; i < shardCount; i++ {
		s := c.shards[i]
		s.RLock()

		for _, item := range s.items {
			if item.ExpireAt > 0 && item.ExpireAt <= now {
				continue
			}
			fn("SET", item.Key, item.Value, item.ExpireAt)
		}

		s.RUnlock()
	}
}

// Close останавливает workers, сбрасывает cold на диск.
func (c *Cache) Close() {
	close(c.stopCh)
	close(c.flushCh)

	if c.cold != nil {
		if err := c.cold.Flush(); err != nil {
			log.Println("cold flush error:", err)
		}
	}
}
