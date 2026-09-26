package server

import (
	"context"
	"errors"
	"log"
	"net"
	"time"
)

// Listen открывает addr и обслуживает соединения до Shutdown.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Serve обслуживает соединения на ln до Shutdown. Возвращает ErrServerClosed.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closing.Load() {
		s.mu.Unlock()
		_ = ln.Close()
		return ErrServerClosed
	}
	s.listener = ln
	s.mu.Unlock()

	auth := "no AUTH"
	if s.password != "" {
		auth = "AUTH enabled"
	} else if s.protected {
		auth = "no AUTH, protected mode: loopback clients only"
	}
	log.Printf("IMCS listening on %s (%s, maxclients %d)", ln.Addr(), auth, s.maxClients)

	var backoff time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.closing.Load() {
				return ErrServerClosed
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Например, EMFILE: не крутимся в горячем цикле.
			if backoff == 0 {
				backoff = 5 * time.Millisecond
			} else if backoff *= 2; backoff > time.Second {
				backoff = time.Second
			}
			log.Printf("accept error: %v; retrying in %v", err, backoff)
			time.Sleep(backoff)
			continue
		}
		backoff = 0

		if !s.track(conn) {
			continue
		}
		go s.handleConnection(conn)
	}
}

// track регистрирует соединение. false — соединение отклонено и закрыто.
func (s *Server) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closing.Load() {
		_ = conn.Close()
		return false
	}
	if len(s.conns) >= s.maxClients {
		// Вежливое сообщение перед закрытием: не дошло — не страшно.
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = conn.Write([]byte("-ERR max number of clients reached\r\n"))
		_ = conn.Close()
		return false
	}
	s.conns[conn] = struct{}{}
	s.wg.Add(1)
	s.totalConns.Add(1)
	return true
}

func (s *Server) untrack(conn net.Conn) {
	// Ошибку не проверяем: при Shutdown соединение уже закрыто, и повторный
	// Close всегда возвращает ошибку. Выход здесь пропустил бы wg.Done(),
	// и Shutdown ждал бы вечно.
	_ = conn.Close()
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
	s.wg.Done()
}

// ConnectedClients — число открытых соединений.
func (s *Server) ConnectedClients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Shutdown перестаёт принимать соединения, даёт клиентам дочитать уже
// полученные команды и ждёт их завершения. Когда ctx истекает, оставшиеся
// соединения закрываются принудительно. Повторный вызов безопасен.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing.Store(true)
	ln := s.listener
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}
	// Будим тех, кто ждёт новую команду; команды из буфера ещё будут выполнены.
	// Ошибка значит, что соединение уже закрыто — будить некого.
	for _, c := range conns {
		_ = c.SetReadDeadline(time.Now())
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		for c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
		<-done
		return ctx.Err()
	}
}
