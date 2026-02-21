package storage

import (
	"runtime"

	"sync/atomic"
	"time"

	"imcs/internal/storage/cold"
)

/*
	Кастомная легковесная блокировка
	0: свободен, >0: кол-во readers, -1: писатель захватил
*/
type SpinRWMutex int32

func (m *SpinRWMutex) RLock () {
	for i := 0; ; i++ {
		if i > 30 {
			runtime.Gosched()
		}

		v:=atomic.LoadInt32((*int32)(m))

		if v >= 0 && atomic.CompareAndSwapInt32((*int32)(m), v, v+1) {
			break
		}
	}
}
func (m *SpinRWMutex) RUnlock() {
	atomic.AddInt32((*int32)(m), -1)
}



func (m *SpinRWMutex) Lock() {
	for i := 0; !atomic.CompareAndSwapInt32((*int32)(m), 0, -1); i++ {
		if i > 30 {
			runtime.Gosched()
		}
	}
}

func (m *SpinRWMutex) Unlock() {
	atomic.StoreInt32((*int32)(m), 0)
}


// Persistence — интерфейс для персистенции данных
type Persistence interface {
	Write(cmd, key, value string, duration time.Duration) error
}




// Item — элемент кеша
type Item struct {
	Key        string
	Value      string
	ExpireAt   int64
	LastAccess int64
	HeapIndex  int
}

// priorityQueue — очередь с приоритетом для TTL
type priorityQueue []*Item





// shard — один шард кеша.
type shard struct {
	SpinRWMutex
	items map[string]*Item
	pq    priorityQueue
}

type ItemSnapshot struct {
	Value 		string
	ExpireAt 	int64
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
	cold      *cold.Store  
	flushCh   chan coldItem
	maxKeys   int64
	totalKeys atomic.Int64
	stopCh    chan struct{}
}
