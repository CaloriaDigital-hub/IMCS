package server

import storage "imcs/internal/storage/cache"




// New создаёт новый TCP-сервер (RESP-совместимый).

func New(addr string, cache *storage.Cache, opts ...Option) *Server {
	s := &Server{
		addr:   addr,
		cache:  cache,
		stopCh: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}