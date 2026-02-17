package storage

import (
	"sync/atomic"
	"time"
)

const staleThreshold = 7 * 24 * time.Hour // 1 неделя

// IsExpired проверяет, истёк ли TTL элемента.
func (i *Item) IsExpired() bool {
	return i.ExpireAt > 0 && time.Now().UnixNano() > i.ExpireAt
}

// IsStale проверяет, не использовался ли элемент больше недели.
func (i *Item) IsStale() bool {
	return time.Now().UnixNano()-atomic.LoadInt64(&i.LastAccess) > int64(staleThreshold)
}
