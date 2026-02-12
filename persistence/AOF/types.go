package AOF

import (
	"os"
	"sync"
	"time"
)

// Типы для AOF
type AOF struct {
	file *os.File
	mu sync.Mutex
}


type WriteInput struct {
	Cmd   string
	Key   string
	Value string
	TTL   time.Duration
}
