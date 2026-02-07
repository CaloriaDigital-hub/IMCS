package cache

import (
	"sync"
	"time"
)



type Item struct {
	Value string
	Created time.Time
	Expiration int64
}

type Cache struct {
	mu 		sync.RWMutex
	items 	map[string]Item

}

