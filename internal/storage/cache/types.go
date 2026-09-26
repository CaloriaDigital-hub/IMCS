package storage

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Persistence — журнал изменений.
//
// Write вызывается под локом шарда, поэтому порядок записей по каждому ключу
// совпадает с порядком изменений в памяти. Реализация не должна брать локи
// шардов. expireAt — абсолютное время (unix ns), 0 = без TTL.
type Persistence interface {
	Write(cmd, key, value string, expireAt int64) error
	Err() error
}

// Команды журнала (совпадают с AOF.Cmd*).
const (
	logSet      = "SET"
	logDel      = "DEL"
	logExpireAt = "EXPIREAT"
	logFlushAll = "FLUSHALL"
)

var (
	ErrNotInteger    = errors.New("value is not an integer or out of range")
	ErrOverflow      = errors.New("increment or decrement would overflow")
	ErrNoSuchKey     = errors.New("no such key")
	ErrInvalidExpire = errors.New("invalid expire time")
	// ErrPersistence — журнал не принимает записи; изменение не выполнено.
	ErrPersistence = errors.New("persistence failure")
)

// SetOptions — опции SET.
type SetOptions struct {
	TTL     time.Duration // > 0: ключ истечёт через TTL
	NX      bool          // только если ключа нет
	XX      bool          // только если ключ есть
	KeepTTL bool          // сохранить текущий TTL
}

// GetResult — результат MGet.
type GetResult struct {
	Value string
	Found bool
}

// priorityQueue — min-heap по ExpireAt (только ключи с TTL).
type priorityQueue []*Item

// shard — один шард кеша. Все поля под RWMutex.
type shard struct {
	sync.RWMutex
	items map[string]*Item
	pq    priorityQueue
	count *atomic.Int64 // общий счётчик ключей кеша
}

// Cache — шардированное in-memory хранилище.
type Cache struct {
	shards    [shardCount]*shard
	persister Persistence
	maxKeys   int64
	totalKeys atomic.Int64
}

type nopPersistence struct{}

func (nopPersistence) Write(string, string, string, int64) error { return nil }
func (nopPersistence) Err() error                                { return nil }
