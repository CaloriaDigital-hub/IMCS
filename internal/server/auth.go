package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
)

// WithAuth включает AUTH с паролем.
func WithAuth(password string) Option {
	return func(s *Server) {
		s.password = password
	}
}

// checkPassword сравнивает за постоянное время (хеши выравнивают длину).
func (s *Server) checkPassword(candidate string) bool {
	a := sha256.Sum256([]byte(candidate))
	b := sha256.Sum256([]byte(s.password))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// deniedByProtectedMode — без пароля к серверу пускаются только локальные клиенты.
func (s *Server) deniedByProtectedMode(addr net.Addr) bool {
	if !s.protected || s.password != "" {
		return false
	}
	tcp, ok := addr.(*net.TCPAddr)
	return ok && !tcp.IP.IsLoopback()
}

const deniedMsg = "-DENIED IMCS is running in protected mode because no password is set " +
	"and it accepts connections from other hosts. Set a password with -auth " +
	"(or IMCS_PASSWORD), or disable protected mode with -protected-mode=false " +
	"if the network is trusted.\r\n"
