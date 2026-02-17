package storage

import (
	"sync/atomic"
	"time"
)

// clock — кешированные часы, обновляются раз в секунду.
// Чтение через nowCached() стоит ~1ns вместо ~25ns у time.Now().
var cachedNow int64

func init() {
	atomic.StoreInt64(&cachedNow, time.Now().UnixNano())

	go func() {
		ticker := time.NewTicker(time.Second)
		for t := range ticker.C {
			atomic.StoreInt64(&cachedNow, t.UnixNano())
		}
	}()
}

// nowCached возвращает кешированное время (точность ~1с).
func nowCached() int64 {
	return atomic.LoadInt64(&cachedNow)
}
