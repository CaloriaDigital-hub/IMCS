package AOF

import (
	"os"
	"sync"
)

type AOF struct {
	file *os.File
	mu sync.Mutex
}