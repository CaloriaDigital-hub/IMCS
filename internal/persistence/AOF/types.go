package AOF

import (
	"bufio"
	"errors"
	"os"
	"sync"
	"sync/atomic"
)

/*

	AOF — append-only журнал.

	Все записи — абсолютные присваивания состояния (SET со значением и
	абсолютным expire, DEL, EXPIREAT, FLUSHALL). Повторное применение любой
	записи поверх более нового состояния даёт тот же результат, на этом
	держится корректность rewrite-буфера.

	Запись идёт через канал в одну горутину-writer, fsync раз в секунду.

*/

// Команды журнала.
const (
	CmdSet      = "SET" // value + абсолютный expire (unix ns, 0 = без TTL)
	CmdDel      = "DEL"
	CmdExpireAt = "EXPIREAT" // абсолютный expire (unix ns), 0 = PERSIST
	CmdFlushAll = "FLUSHALL"
)

var (
	// ErrClosed возвращается при записи в закрытый журнал.
	ErrClosed = errors.New("aof: journal is closed")
	// ErrCorrupt — повреждение в середине журнала (не хвост).
	ErrCorrupt = errors.New("aof: journal is corrupt")
	// ErrRewriteInProgress — rewrite уже идёт.
	ErrRewriteInProgress = errors.New("aof: rewrite already in progress")
)

type record struct {
	cmd    string
	key    string
	value  string
	expire int64
}

type AOF struct {
	dir  string
	path string

	// mu защищает файл, writer и состояние rewrite.
	mu         sync.Mutex
	file       *os.File
	writer     *bufio.Writer
	scratch    []byte
	size       int64
	dirty      bool
	rewriting  bool
	rewriteBuf []record

	// closeMu гарантирует, что после Close в канал никто не пишет.
	closeMu sync.RWMutex
	closed  atomic.Bool

	writeCh chan record
	stopCh  chan struct{}
	done    chan struct{}

	failErr         atomic.Pointer[error] // первая ошибка записи на диск (sticky)
	rewriteRunning  atomic.Bool
	lastRewriteSize atomic.Int64
}

// ReadResult — результат загрузки журнала.
type ReadResult struct {
	ValidEntries   int   // применённые записи
	LegacyEntries  int   // из них в старом строковом формате
	CorruptEntries int   // отброшенные записи
	Truncated      bool  // файл был обрезан
	TruncatedAt    int64 // позиция обрезки (байт)
}
