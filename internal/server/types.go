package server

import (
	"net"

	"imcs/internal/storage/cache"
)

// Server — TCP-сервер IMCS (RESP-совместимый).
type Server struct {
	addr     string
	cache    *storage.Cache
	password string // optional AUTH password
	listener net.Listener
	stopCh   chan struct{}
}
