package storage

import (
	"sync/atomic"
	"time"
)

const staleThreshold = 7 * 24 * time.Hour // 1 неделя

// IsExpired проверяет, истёк ли TTL элемента. Thread-safe (atomic).
func (i *Item) IsExpired() bool {
	expireAt := atomic.LoadInt64(&i.ExpireAt)
	return expireAt > 0 && time.Now().UnixNano() > expireAt
}

// IsStale проверяет, не использовался ли элемент больше недели.
func (i *Item) IsStale() bool {
	return time.Now().UnixNano()-atomic.LoadInt64(&i.LastAccess) > int64(staleThreshold)
}
