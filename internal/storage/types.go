package storage

import (
	"sync"
	"sync/atomic"
	"time"

	"imcs/internal/storage/cold"
)

// Persistence — интерфейс для персистенции данных.
type Persistence interface {
	Write(cmd, key, value string, duration time.Duration) error
}

// Item — элемент кеша.
type Item struct {
	Key        string
	Value      string
	ExpireAt   int64
	LastAccess int64
	HeapIndex  int
}

// priorityQueue — очередь с приоритетом для TTL.
type priorityQueue []*Item

// shard — один шард кеша.
type shard struct {
	sync.RWMutex
	items map[string]*Item
	pq    priorityQueue
}

// coldItem — элемент для передачи в cold storage через канал.
type coldItem struct {
	key      string
	value    string
	expireAt int64
}

// Cache — шардированное in-memory хранилище.
type Cache struct {
	shards    [shardCount]*shard
	persister Persistence
	cold      *cold.Store   // холодное хранилище на диске
	flushCh   chan coldItem // канал для worker pool (выгрузка на диск)
	maxKeys   int64
	totalKeys atomic.Int64
	stopCh    chan struct{}
}
