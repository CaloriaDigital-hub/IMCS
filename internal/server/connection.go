package server

import (
	"bufio"
	"net"
	"time"
	"strings"
)

// handleConnection обрабатывает одно клиентское соединение (RESP).
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReaderSize(conn, 64*1024)
	writer := bufio.NewWriterSize(conn, 64*1024)

	authenticated := s.password == "" // если пароля нет — сразу авторизован

	for {
		// Idle timeout: 300 секунд
		conn.SetReadDeadline(time.Now().Add(300 * time.Second))

		args, err := readRESPCommand(reader)
		if err != nil {
			return
		}

		if len(args) == 0 {
			continue
		}

		cmd := strings.ToUpper(args[0])
		cmdArgs := args[1:]

		// AUTH и QUIT доступны до авторизации
		if cmd == "AUTH" {
			if s.password == "" {
				writer.Write(respErrorMsg("Client sent AUTH, but no password is set"))
			} else if len(cmdArgs) != 1 {
				writer.Write(respErrorMsg("wrong number of arguments for 'auth' command"))
			} else if cmdArgs[0] == s.password {
				authenticated = true
				writer.Write(respOK())
			} else {
				writer.Write(respErrorMsg("WRONGPASS invalid password"))
			}
			writer.Flush()
			continue
		}

		if cmd == "QUIT" {
			writer.Write(respOK())
			writer.Flush()
			return
		}

		// Проверяем авторизацию
		if !authenticated {
			writer.Write(respErrorMsg("NOAUTH Authentication required"))
			writer.Flush()
			continue
		}

		resp := s.executeCommand(cmd, cmdArgs)
		writer.Write(resp)
		writer.Flush()
	}
}
