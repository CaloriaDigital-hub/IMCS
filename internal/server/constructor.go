package server

import (
	"net"
	"time"

	storage "github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"
)

// New создаёт TCP-сервер. По умолчанию: protected mode включён,
// maxclients = DefaultMaxClients, idle timeout выключен (как в Redis).
func New(addr string, cache *storage.Cache, opts ...Option) *Server {
	s := &Server{
		addr:       addr,
		cache:      cache,
		protected:  true,
		maxClients: DefaultMaxClients,
		conns:      make(map[net.Conn]struct{}),
		startedAt:  time.Now(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithMaxClients ограничивает число одновременных соединений.
func WithMaxClients(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.maxClients = n
		}
	}
}

// WithIdleTimeout закрывает соединения, простаивающие дольше d (0 = никогда).
func WithIdleTimeout(d time.Duration) Option {
	return func(s *Server) { s.idleTimeout = d }
}

// WithProtectedMode: без пароля принимать команды только с loopback-адресов.
func WithProtectedMode(on bool) Option {
	return func(s *Server) { s.protected = on }
}

// WithAOF подключает журнал: INFO persistence и BGREWRITEAOF.
func WithAOF(status AOFStatus, rewrite func() error) Option {
	return func(s *Server) {
		s.aof = status
		s.rewrite = rewrite
	}
}
