package storage

import (
	"sync/atomic"
	"time"
)

// clockResolution — точность LRU-часов. Чтение через nowCached() стоит ~1ns
// вместо ~25ns у time.Now(); 10 пробуждений в секунду незаметны даже на 2 vCPU.
const clockResolution = 100 * time.Millisecond

var cachedNow int64

func init() {
	atomic.StoreInt64(&cachedNow, time.Now().UnixNano())

	go func() {
		ticker := time.NewTicker(clockResolution)
		for t := range ticker.C {
			atomic.StoreInt64(&cachedNow, t.UnixNano())
		}
	}()
}

// nowCached возвращает кешированное время (точность clockResolution).
// Только для LastAccess; для TTL используется time.Now().
func nowCached() int64 {
	return atomic.LoadInt64(&cachedNow)
}
