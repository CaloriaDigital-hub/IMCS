package cache

import (
	"imcs/persistence/AOF"
	"sync"
	"time"
)



type Item struct {
	Value string
	Created time.Time
	Expiration int64
}

type Cache struct {
	mu 			sync.RWMutex
	items 		map[string]Item
	storageDir	string
	persister *AOF.AOF


}

