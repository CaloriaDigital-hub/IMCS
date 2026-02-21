package AOF

import (
	"bufio"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

/*

 	AOF — append-only file для персистенции команд.
 	Запись через буферизованный канал — одна горутина-writer.

*/

type AOF struct {
	file    *os.File
	dir     string
	writer  *bufio.Writer
	mu      sync.Mutex
	writeCh chan writeEntry // буфер записей
	stopCh  chan struct{}
	done    chan struct{}

	// Rewrite buffer: пока идёт rewrite, новые записи дублируются сюда
	rewriting  atomic.Bool
	rewriteMu  sync.Mutex
	rewriteBuf [][]byte // буфер записей, пришедших во время rewrite
}

// writeEntry — запись в очередь AOF.
type writeEntry struct {
	data []byte
}

// WriteInput — входные данные для записи в AOF.
type WriteInput struct {
	Cmd   string
	Key   string
	Value string
	TTL   time.Duration
}

// ReadResult — результат чтения AOF.
type ReadResult struct {
	ValidEntries   int   // число корректных записей
	CorruptEntries int   // число записей с битым CRC
	Truncated      bool  // был ли файл обрезан
	TruncatedAt    int64 // позиция обрезки (байт)
}
