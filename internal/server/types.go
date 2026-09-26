package server

import (
	"bufio"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"
)

// ErrServerClosed возвращается из Listen/Serve после Shutdown.
var ErrServerClosed = errors.New("server: closed")

const (
	DefaultMaxClients = 10000

	readBufSize  = 16 * 1024
	writeBufSize = 16 * 1024
	writeTimeout = 60 * time.Second

	// Пределы протокола — как в Redis.
	maxInlineLen   = 64 * 1024
	maxArgs        = 1024 * 1024
	maxBulkLen     = 512 * 1024 * 1024
	maxArgsNoAuth  = 10
	maxBulkNoAuth  = 16 * 1024
	bigArgChunkLen = 1024 * 1024
)

// AOFStatus — сведения о журнале для INFO и BGREWRITEAOF.
type AOFStatus interface {
	Size() int64
	Err() error
	RewriteRunning() bool
}

// Server — TCP-сервер IMCS (RESP2).
type Server struct {
	addr        string
	cache       *storage.Cache
	password    string
	protected   bool
	maxClients  int
	idleTimeout time.Duration
	rewrite     func() error
	aof         AOFStatus

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
	closing  atomic.Bool
	wg       sync.WaitGroup

	startedAt  time.Time
	totalConns atomic.Int64
	totalCmds  atomic.Int64
}

// Option — функциональная опция сервера.
type Option func(*Server)

// client — состояние одного соединения.
type client struct {
	conn   net.Conn
	r      *bufio.Reader
	w      *bufio.Writer
	authed bool
	quit   bool
}
