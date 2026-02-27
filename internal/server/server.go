package server

import (

	"log"
	"net"

)





// Listen запускает TCP-сервер.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln

	if s.password != "" {
		log.Printf("IMCS server listening on %s (RESP, AUTH enabled)", s.addr)
	} else {
		log.Printf("IMCS server listening on %s (RESP protocol)", s.addr)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return nil
			default:
			}
			log.Println("accept error:", err)
			continue
		}
		go s.handleConnection(conn)
	}
}

// Shutdown graceful shutdown.
func (s *Server) Shutdown() {
	close(s.stopCh)
	if s.listener != nil {
		s.listener.Close()
	}
}


