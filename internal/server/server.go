package server

import (
	"bufio"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"imcs/internal/storage/cache"
)

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

// Option — функциональная опция сервера.
type Option func(*Server)

// WithAuth устанавливает пароль для AUTH.
func WithAuth(password string) Option {
	return func(s *Server) {
		s.password = password
	}
}

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

// executeCommand выполняет RESP-команду, возвращает RESP-ответ.
func (s *Server) executeCommand(cmd string, args []string) []byte {
	switch cmd {
	// === String Commands ===
	case "SET":
		return s.cmdSET(args)
	case "GET":
		return s.cmdGET(args)
	case "DEL":
		return s.cmdDEL(args)
	case "SETNX":
		return s.cmdSETNX(args)
	case "SETEX":
		return s.cmdSETEX(args)
	case "MGET":
		return s.cmdMGET(args)
	case "MSET":
		return s.cmdMSET(args)
	case "INCR":
		return s.cmdINCR(args, 1)
	case "DECR":
		return s.cmdINCR(args, -1)
	case "INCRBY":
		return s.cmdINCRBY(args, false)
	case "DECRBY":
		return s.cmdINCRBY(args, true)
	case "APPEND":
		return s.cmdAPPEND(args)
	case "STRLEN":
		return s.cmdSTRLEN(args)

	// === Key Commands ===
	case "EXISTS":
		return s.cmdEXISTS(args)
	case "EXPIRE":
		return s.cmdEXPIRE(args, false)
	case "PEXPIRE":
		return s.cmdEXPIRE(args, true)
	case "TTL":
		return respInt(s.cache.GetTTL(args[0]))
	case "PTTL":
		return respInt(s.cache.GetPTTL(args[0]))
	case "PERSIST":
		return s.cmdPERSIST(args)
	case "TYPE":
		return s.cmdTYPE(args)
	case "RENAME":
		return s.cmdRENAME(args)
	case "KEYS":
		return s.cmdKEYS(args)

	// === Server Commands ===
	case "PING":
		return s.cmdPING(args)
	case "ECHO":
		return s.cmdECHO(args)
	case "DBSIZE":
		return respInt(s.cache.CountKeys())
	case "FLUSHDB", "FLUSHALL":
		s.cache.FlushDB()
		return respOK()
	case "INFO":
		return s.cmdINFO()
	case "SELECT":
		return respOK() // single DB — always OK
	case "COMMAND":
		return respOK()
	case "CONFIG":
		return s.cmdCONFIG(args)
	case "CLIENT":
		return respOK()

	default:
		return respErrorMsg("unknown command '" + cmd + "'")
	}
}

// === String Commands ===

func (s *Server) cmdSET(args []string) []byte {
	if len(args) < 2 {
		return respErrorMsg("wrong number of arguments for 'set' command")
	}

	key := args[0]
	value := args[1]
	var ttl time.Duration
	var nx, xx bool

	for i := 2; i < len(args); i++ {
		opt := strings.ToUpper(args[i])
		switch opt {
		case "NX":
			nx = true
		case "XX":
			xx = true
		case "EX":
			if i+1 >= len(args) {
				return respErrorMsg("syntax error")
			}
			i++
			secs, err := strconv.Atoi(args[i])
			if err != nil {
				return respErrorMsg("value is not an integer or out of range")
			}
			ttl = time.Duration(secs) * time.Second
		case "PX":
			if i+1 >= len(args) {
				return respErrorMsg("syntax error")
			}
			i++
			ms, err := strconv.Atoi(args[i])
			if err != nil {
				return respErrorMsg("value is not an integer or out of range")
			}
			ttl = time.Duration(ms) * time.Millisecond
		default:
			return respErrorMsg("syntax error")
		}
	}

	// XX = only set if key already exists
	if xx {
		if s.cache.Exists(key) == 0 {
			return respNilBulk()
		}
	}

	if nx {
		if err := s.cache.Set(key, value, ttl, true); err == storage.ErrKeyExist {
			return respNilBulk()
		}
	} else {
		s.cache.Set(key, value, ttl, false)
	}

	return respOK()
}

func (s *Server) cmdGET(args []string) []byte {
	if len(args) != 1 {
		return respErrorMsg("wrong number of arguments for 'get' command")
	}

	value, found := s.cache.Get(args[0])
	if !found {
		return respNilBulk()
	}

	return respBulk(value)
}

func (s *Server) cmdDEL(args []string) []byte {
	if len(args) < 1 {
		return respErrorMsg("wrong number of arguments for 'del' command")
	}

	deleted := 0
	for _, key := range args {
		if s.cache.Exists(key) > 0 {
			s.cache.Delete(key)
			deleted++
		}
	}

	return respInt(int64(deleted))
}

func (s *Server) cmdSETNX(args []string) []byte {
	if len(args) != 2 {
		return respErrorMsg("wrong number of arguments for 'setnx' command")
	}
	if err := s.cache.Set(args[0], args[1], 0, true); err == storage.ErrKeyExist {
		return respInt(0)
	}
	return respInt(1)
}

func (s *Server) cmdSETEX(args []string) []byte {
	if len(args) != 3 {
		return respErrorMsg("wrong number of arguments for 'setex' command")
	}
	secs, err := strconv.Atoi(args[1])
	if err != nil || secs <= 0 {
		return respErrorMsg("invalid expire time in 'setex' command")
	}
	s.cache.Set(args[0], args[2], time.Duration(secs)*time.Second, false)
	return respOK()
}

func (s *Server) cmdMGET(args []string) []byte {
	if len(args) < 1 {
		return respErrorMsg("wrong number of arguments for 'mget' command")
	}
	results := s.cache.MGet(args...)
	return respArrayBulks(results)
}

func (s *Server) cmdMSET(args []string) []byte {
	if len(args) < 2 || len(args)%2 != 0 {
		return respErrorMsg("wrong number of arguments for 'mset' command")
	}
	s.cache.MSet(args...)
	return respOK()
}

func (s *Server) cmdINCR(args []string, delta int64) []byte {
	if len(args) != 1 {
		return respErrorMsg("wrong number of arguments for 'incr' command")
	}
	result, err := s.cache.IncrBy(args[0], delta)
	if err != nil {
		return respErrorMsg("value is not an integer or out of range")
	}
	return respInt(result)
}

func (s *Server) cmdINCRBY(args []string, negate bool) []byte {
	if len(args) != 2 {
		return respErrorMsg("wrong number of arguments for 'incrby' command")
	}
	delta, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return respErrorMsg("value is not an integer or out of range")
	}
	if negate {
		delta = -delta
	}
	result, err := s.cache.IncrBy(args[0], delta)
	if err != nil {
		return respErrorMsg("value is not an integer or out of range")
	}
	return respInt(result)
}

func (s *Server) cmdAPPEND(args []string) []byte {
	if len(args) != 2 {
		return respErrorMsg("wrong number of arguments for 'append' command")
	}
	length := s.cache.Append(args[0], args[1])
	return respInt(int64(length))
}

func (s *Server) cmdSTRLEN(args []string) []byte {
	if len(args) != 1 {
		return respErrorMsg("wrong number of arguments for 'strlen' command")
	}
	return respInt(int64(s.cache.Strlen(args[0])))
}

// === Key Commands ===

func (s *Server) cmdEXISTS(args []string) []byte {
	if len(args) < 1 {
		return respErrorMsg("wrong number of arguments for 'exists' command")
	}
	return respInt(s.cache.Exists(args...))
}

func (s *Server) cmdEXPIRE(args []string, isMs bool) []byte {
	if len(args) != 2 {
		return respErrorMsg("wrong number of arguments for 'expire' command")
	}
	n, err := strconv.Atoi(args[1])
	if err != nil {
		return respErrorMsg("value is not an integer or out of range")
	}
	var ttl time.Duration
	if isMs {
		ttl = time.Duration(n) * time.Millisecond
	} else {
		ttl = time.Duration(n) * time.Second
	}
	if s.cache.Expire(args[0], ttl) {
		return respInt(1)
	}
	return respInt(0)
}

func (s *Server) cmdPERSIST(args []string) []byte {
	if len(args) != 1 {
		return respErrorMsg("wrong number of arguments for 'persist' command")
	}
	if s.cache.Persist(args[0]) {
		return respInt(1)
	}
	return respInt(0)
}

func (s *Server) cmdTYPE(args []string) []byte {
	if len(args) != 1 {
		return respErrorMsg("wrong number of arguments for 'type' command")
	}
	return respSimple(s.cache.Type(args[0]))
}

func (s *Server) cmdRENAME(args []string) []byte {
	if len(args) != 2 {
		return respErrorMsg("wrong number of arguments for 'rename' command")
	}
	if !s.cache.Rename(args[0], args[1]) {
		return respErrorMsg("no such key")
	}
	return respOK()
}

func (s *Server) cmdKEYS(args []string) []byte {
	if len(args) != 1 {
		return respErrorMsg("wrong number of arguments for 'keys' command")
	}
	keys := s.cache.Keys(args[0])
	return respArrayStrings(keys)
}

// === Server Commands ===

func (s *Server) cmdPING(args []string) []byte {
	if len(args) > 0 {
		return respBulk(args[0])
	}
	return respSimple("PONG")
}

func (s *Server) cmdECHO(args []string) []byte {
	if len(args) != 1 {
		return respErrorMsg("wrong number of arguments for 'echo' command")
	}
	return respBulk(args[0])
}

func (s *Server) cmdINFO() []byte {
	keys := s.cache.CountKeys()
	info := "# Server\r\n" +
		"imcs_version:1.0.0\r\n" +
		"resp_protocol:2\r\n" +
		"tcp_port:" + strings.TrimPrefix(s.addr, ":") + "\r\n" +
		"# Clients\r\n" +
		"# Keyspace\r\n" +
		"db0:keys=" + strconv.FormatInt(keys, 10) + ",expires=0\r\n"
	return respBulk(info)
}

func (s *Server) cmdCONFIG(args []string) []byte {
	if len(args) >= 1 && strings.ToUpper(args[0]) == "SET" {
		return respOK()
	}
	// CONFIG GET — вернём пустой array
	return respArrayStrings(nil)
}
