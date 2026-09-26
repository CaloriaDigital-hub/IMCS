package server

import (
	"bufio"
	"errors"
	"log"
	"net"
	"runtime/debug"
	"time"
)

// handleConnection обслуживает одно клиентское соединение.
func (s *Server) handleConnection(conn net.Conn) {
	defer s.untrack(conn)
	defer func() {
		// Паника в одной команде не должна ронять весь сервер.
		if r := recover(); r != nil {
			log.Printf("panic serving %s: %v\n%s", conn.RemoteAddr(), r, debug.Stack())
		}
	}()

	if s.deniedByProtectedMode(conn.RemoteAddr()) {
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = conn.Write([]byte(deniedMsg))
		return
	}

	c := &client{
		conn:   conn,
		r:      bufio.NewReaderSize(conn, readBufSize),
		w:      bufio.NewWriterSize(conn, writeBufSize),
		authed: s.password == "",
	}

	for !c.quit {
		// При Shutdown дорабатываем уже полученные команды и выходим.
		if s.closing.Load() && c.r.Buffered() == 0 {
			break
		}
		if s.idleTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.idleTimeout))
		}

		lim := limits{maxArgs: maxArgs, maxBulk: maxBulkLen}
		if !c.authed {
			lim = limits{maxArgs: maxArgsNoAuth, maxBulk: maxBulkNoAuth}
		}

		args, err := readCommand(c.r, lim)
		if err != nil {
			var pe protocolError
			if errors.As(err, &pe) {
				_, _ = c.w.WriteString("-ERR Protocol error: " + string(pe) + "\r\n")
				_ = s.flush(c)
			}
			return
		}
		if len(args) == 0 {
			continue
		}

		// Ошибка bufio.Writer «липкая» и вернётся из flush ниже.
		_, _ = c.w.Write(s.dispatch(c, args))

		// Pipelining: пока в буфере есть следующие команды, ответы копятся
		// и уходят одним syscall.
		if c.r.Buffered() == 0 || c.quit {
			if s.flush(c) != nil {
				return
			}
		}
	}
	_ = s.flush(c)
}

func (s *Server) flush(c *client) error {
	if c.w.Buffered() == 0 {
		return nil
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.w.Flush()
}
