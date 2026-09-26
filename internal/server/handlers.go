package server

import (
	"errors"
	"log"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	storage "github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"
)

// command — описание команды.
// arity как в Redis (с учётом имени): N — ровно N аргументов, -N — не меньше N.
type command struct {
	arity  int
	noAuth bool // доступна до AUTH
	fn     func(s *Server, c *client, args []string) []byte
}

var commands map[string]command

func init() {
	commands = map[string]command{
		// Строки
		"SET":    {-3, false, cmdSet},
		"GET":    {2, false, cmdGet},
		"DEL":    {-2, false, cmdDel},
		"UNLINK": {-2, false, cmdDel},
		"SETNX":  {3, false, cmdSetNX},
		"SETEX":  {4, false, cmdSetEX},
		"PSETEX": {4, false, cmdSetEX},
		"MGET":   {-2, false, cmdMGet},
		"MSET":   {-3, false, cmdMSet},
		"INCR":   {2, false, cmdIncr},
		"DECR":   {2, false, cmdIncr},
		"INCRBY": {3, false, cmdIncr},
		"DECRBY": {3, false, cmdIncr},
		"APPEND": {3, false, cmdAppend},
		"STRLEN": {2, false, cmdStrlen},

		// Ключи
		"EXISTS":  {-2, false, cmdExists},
		"EXPIRE":  {3, false, cmdExpire},
		"PEXPIRE": {3, false, cmdExpire},
		"TTL":     {2, false, cmdTTL},
		"PTTL":    {2, false, cmdTTL},
		"PERSIST": {2, false, cmdPersist},
		"TYPE":    {2, false, cmdType},
		"RENAME":  {3, false, cmdRename},
		"KEYS":    {2, false, cmdKeys},

		// Сервер
		"AUTH":         {-2, true, cmdAuth},
		"QUIT":         {-1, true, cmdQuit},
		"PING":         {-1, false, cmdPing},
		"ECHO":         {2, false, cmdEcho},
		"DBSIZE":       {1, false, cmdDBSize},
		"FLUSHDB":      {-1, false, cmdFlush},
		"FLUSHALL":     {-1, false, cmdFlush},
		"INFO":         {-1, false, cmdInfo},
		"SELECT":       {2, false, cmdSelect},
		"COMMAND":      {-1, false, cmdCommand},
		"CONFIG":       {-2, false, cmdConfig},
		"CLIENT":       {-2, false, cmdClient},
		"BGREWRITEAOF": {1, false, cmdBgRewrite},
	}
}

func (s *Server) dispatch(c *client, args []string) []byte {
	s.totalCmds.Add(1)

	cmd, ok := commands[args[0]]
	if !ok {
		cmd, ok = commands[strings.ToUpper(args[0])]
	}
	if !c.authed && !(ok && cmd.noAuth) {
		return respError("NOAUTH", "Authentication required.")
	}
	if !ok {
		return respErr("unknown command '" + truncate(args[0], 64) + "'")
	}
	if (cmd.arity > 0 && len(args) != cmd.arity) || (cmd.arity < 0 && len(args) < -cmd.arity) {
		return respErr("wrong number of arguments for '" + strings.ToLower(args[0]) + "' command")
	}
	return cmd.fn(s, c, args)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// errReply переводит ошибку хранилища в ответ Redis.
func errReply(err error) []byte {
	switch {
	case errors.Is(err, storage.ErrPersistence):
		return respError("MISCONF", "Errors writing to the AOF file, write commands are disabled. Check the server logs.")
	case errors.Is(err, storage.ErrNotInteger):
		return respErr("value is not an integer or out of range")
	case errors.Is(err, storage.ErrOverflow):
		return respErr("increment or decrement would overflow")
	case errors.Is(err, storage.ErrNoSuchKey):
		return respErr("no such key")
	case errors.Is(err, storage.ErrInvalidExpire):
		return respErr("invalid expire time")
	default:
		return respErr(err.Error())
	}
}

var (
	errSyntax    = respErr("syntax error")
	errNotInt    = respErr("value is not an integer or out of range")
	errBadExpire = func(cmd string) []byte { return respErr("invalid expire time in '" + cmd + "' command") }
)

// parseTTL разбирает положительный TTL в единицах unit с проверкой переполнения.
func parseTTL(str string, unit time.Duration, cmd string) (time.Duration, []byte) {
	n, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return 0, errNotInt
	}
	if n <= 0 || n > math.MaxInt64/int64(unit) {
		return 0, errBadExpire(cmd)
	}
	return time.Duration(n) * unit, nil
}

// === Строки ===

// SET key value [NX|XX] [EX seconds|PX milliseconds|KEEPTTL]
func cmdSet(s *Server, c *client, args []string) []byte {
	var opt storage.SetOptions
	hasTTL := false
	for i := 3; i < len(args); i++ {
		switch strings.ToUpper(args[i]) {
		case "NX":
			if opt.XX {
				return errSyntax
			}
			opt.NX = true
		case "XX":
			if opt.NX {
				return errSyntax
			}
			opt.XX = true
		case "KEEPTTL":
			if hasTTL {
				return errSyntax
			}
			opt.KeepTTL = true
		case "EX", "PX":
			if hasTTL || opt.KeepTTL || i+1 >= len(args) {
				return errSyntax
			}
			unit := time.Second
			if strings.EqualFold(args[i], "PX") {
				unit = time.Millisecond
			}
			i++
			ttl, errResp := parseTTL(args[i], unit, "set")
			if errResp != nil {
				return errResp
			}
			opt.TTL, hasTTL = ttl, true
		default:
			return errSyntax
		}
	}

	ok, err := s.cache.Set(args[1], args[2], opt)
	if err != nil {
		return errReply(err)
	}
	if !ok {
		return replyNilBulk
	}
	return replyOK
}

func cmdGet(s *Server, c *client, args []string) []byte {
	v, ok := s.cache.Get(args[1])
	if !ok {
		return replyNilBulk
	}
	return respBulk(v)
}

func cmdDel(s *Server, c *client, args []string) []byte {
	n, err := s.cache.Delete(args[1:]...)
	if err != nil {
		return errReply(err)
	}
	return respInt(n)
}

func cmdSetNX(s *Server, c *client, args []string) []byte {
	ok, err := s.cache.Set(args[1], args[2], storage.SetOptions{NX: true})
	if err != nil {
		return errReply(err)
	}
	if ok {
		return replyOne
	}
	return replyZero
}

// SETEX key seconds value / PSETEX key milliseconds value
func cmdSetEX(s *Server, c *client, args []string) []byte {
	unit, name := time.Second, "setex"
	if strings.EqualFold(args[0], "PSETEX") {
		unit, name = time.Millisecond, "psetex"
	}
	ttl, errResp := parseTTL(args[2], unit, name)
	if errResp != nil {
		return errResp
	}
	if _, err := s.cache.Set(args[1], args[3], storage.SetOptions{TTL: ttl}); err != nil {
		return errReply(err)
	}
	return replyOK
}

func cmdMGet(s *Server, c *client, args []string) []byte {
	return respArrayResults(s.cache.MGet(args[1:]...))
}

func cmdMSet(s *Server, c *client, args []string) []byte {
	if len(args)%2 != 1 {
		return respErr("wrong number of arguments for 'mset' command")
	}
	if err := s.cache.MSet(args[1:]...); err != nil {
		return errReply(err)
	}
	return replyOK
}

// INCR / DECR / INCRBY / DECRBY
func cmdIncr(s *Server, c *client, args []string) []byte {
	name := strings.ToUpper(args[0])
	delta := int64(1)
	if len(args) == 3 {
		d, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil {
			return errNotInt
		}
		delta = d
	}
	if name == "DECR" || name == "DECRBY" {
		if delta == math.MinInt64 {
			return respErr("decrement would overflow")
		}
		delta = -delta
	}
	n, err := s.cache.IncrBy(args[1], delta)
	if err != nil {
		return errReply(err)
	}
	return respInt(n)
}

func cmdAppend(s *Server, c *client, args []string) []byte {
	n, err := s.cache.Append(args[1], args[2])
	if err != nil {
		return errReply(err)
	}
	return respInt(int64(n))
}

func cmdStrlen(s *Server, c *client, args []string) []byte {
	return respInt(int64(s.cache.Strlen(args[1])))
}

// === Ключи ===

func cmdExists(s *Server, c *client, args []string) []byte {
	return respInt(s.cache.Exists(args[1:]...))
}

// EXPIRE key seconds / PEXPIRE key milliseconds. Неположительный TTL удаляет ключ.
func cmdExpire(s *Server, c *client, args []string) []byte {
	unit := time.Second
	if strings.EqualFold(args[0], "PEXPIRE") {
		unit = time.Millisecond
	}
	n, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return errNotInt
	}
	if n > math.MaxInt64/int64(unit) {
		return errBadExpire(strings.ToLower(args[0]))
	}
	ok, err := s.cache.Expire(args[1], time.Duration(n)*unit)
	if err != nil {
		return errReply(err)
	}
	if ok {
		return replyOne
	}
	return replyZero
}

func cmdTTL(s *Server, c *client, args []string) []byte {
	if strings.EqualFold(args[0], "PTTL") {
		return respInt(s.cache.GetPTTL(args[1]))
	}
	return respInt(s.cache.GetTTL(args[1]))
}

func cmdPersist(s *Server, c *client, args []string) []byte {
	ok, err := s.cache.Persist(args[1])
	if err != nil {
		return errReply(err)
	}
	if ok {
		return replyOne
	}
	return replyZero
}

func cmdType(s *Server, c *client, args []string) []byte {
	return respSimple(s.cache.Type(args[1]))
}

func cmdRename(s *Server, c *client, args []string) []byte {
	if err := s.cache.Rename(args[1], args[2]); err != nil {
		return errReply(err)
	}
	return replyOK
}

func cmdKeys(s *Server, c *client, args []string) []byte {
	return respArrayStrings(s.cache.Keys(args[1]))
}

// === Сервер ===

// AUTH password | AUTH default password
func cmdAuth(s *Server, c *client, args []string) []byte {
	if s.password == "" {
		return respErr("AUTH <password> called without any password configured for the default user. Are you sure your configuration is correct?")
	}
	var pass string
	switch len(args) {
	case 2:
		pass = args[1]
	case 3:
		if args[1] != "default" {
			return respError("WRONGPASS", "invalid username-password pair or user is disabled.")
		}
		pass = args[2]
	default:
		return errSyntax
	}
	if !s.checkPassword(pass) {
		return respError("WRONGPASS", "invalid username-password pair or user is disabled.")
	}
	c.authed = true
	return replyOK
}

func cmdQuit(s *Server, c *client, args []string) []byte {
	c.quit = true
	return replyOK
}

func cmdPing(s *Server, c *client, args []string) []byte {
	switch len(args) {
	case 1:
		return replyPong
	case 2:
		return respBulk(args[1])
	default:
		return respErr("wrong number of arguments for 'ping' command")
	}
}

func cmdEcho(s *Server, c *client, args []string) []byte {
	return respBulk(args[1])
}

func cmdDBSize(s *Server, c *client, args []string) []byte {
	return respInt(s.cache.CountKeys())
}

// FLUSHDB / FLUSHALL [ASYNC|SYNC] — база одна, обе команды очищают всё.
func cmdFlush(s *Server, c *client, args []string) []byte {
	if len(args) > 2 {
		return errSyntax
	}
	if len(args) == 2 {
		if m := strings.ToUpper(args[1]); m != "ASYNC" && m != "SYNC" {
			return errSyntax
		}
	}
	if err := s.cache.FlushAll(); err != nil {
		return errReply(err)
	}
	return replyOK
}

// SELECT 0 — поддерживается только одна база. Остальные индексы — ошибка,
// а не молчаливая запись в ту же базу.
func cmdSelect(s *Server, c *client, args []string) []byte {
	n, err := strconv.Atoi(args[1])
	if err != nil {
		return errNotInt
	}
	if n != 0 {
		return respErr("DB index is out of range")
	}
	return replyOK
}

func cmdCommand(s *Server, c *client, args []string) []byte {
	if len(args) >= 2 && strings.EqualFold(args[1], "COUNT") {
		return respInt(int64(len(commands)))
	}
	return replyEmpty
}

func cmdConfig(s *Server, c *client, args []string) []byte {
	switch strings.ToUpper(args[1]) {
	case "GET":
		return replyEmpty
	case "RESETSTAT":
		return replyOK
	default:
		return respErr("CONFIG " + strings.ToUpper(args[1]) + " is not supported")
	}
}

func cmdClient(s *Server, c *client, args []string) []byte {
	switch strings.ToUpper(args[1]) {
	case "SETNAME", "SETINFO", "NO-EVICT", "NO-TOUCH":
		return replyOK
	case "GETNAME":
		return replyNilBulk
	case "ID":
		return respInt(0)
	default:
		return respErr("CLIENT " + strings.ToUpper(args[1]) + " is not supported")
	}
}

func cmdBgRewrite(s *Server, c *client, args []string) []byte {
	if s.rewrite == nil {
		return respErr("AOF is not enabled")
	}
	if s.aof != nil && s.aof.RewriteRunning() {
		return respErr("Background append only file rewriting already in progress")
	}
	go func() {
		if err := s.rewrite(); err != nil {
			log.Printf("BGREWRITEAOF failed: %v", err)
		}
	}()
	return respSimple("Background append only file rewriting started")
}

func cmdInfo(s *Server, c *client, args []string) []byte {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	var b strings.Builder
	line := func(k string, v any) {
		b.WriteString(k)
		b.WriteByte(':')
		switch x := v.(type) {
		case string:
			b.WriteString(x)
		case int64:
			b.WriteString(strconv.FormatInt(x, 10))
		case int:
			b.WriteString(strconv.Itoa(x))
		case uint64:
			b.WriteString(strconv.FormatUint(x, 10))
		}
		b.WriteString("\r\n")
	}

	b.WriteString("# Server\r\n")
	line("imcs_version", Version)
	line("process_id", os.Getpid())
	line("tcp_port", portOf(s.addr))
	line("uptime_in_seconds", int64(time.Since(s.startedAt).Seconds()))
	b.WriteString("\r\n# Clients\r\n")
	line("connected_clients", s.ConnectedClients())
	line("maxclients", s.maxClients)
	b.WriteString("\r\n# Memory\r\n")
	line("used_memory", ms.HeapAlloc)
	line("used_memory_rss", ms.Sys)
	b.WriteString("\r\n# Persistence\r\n")
	line("loading", 0)
	if s.aof != nil {
		status := "ok"
		if s.aof.Err() != nil {
			status = "err"
		}
		rewriting := 0
		if s.aof.RewriteRunning() {
			rewriting = 1
		}
		line("aof_enabled", 1)
		line("aof_rewrite_in_progress", rewriting)
		line("aof_last_write_status", status)
		line("aof_current_size", s.aof.Size())
	} else {
		line("aof_enabled", 0)
	}
	b.WriteString("\r\n# Stats\r\n")
	line("total_connections_received", s.totalConns.Load())
	line("total_commands_processed", s.totalCmds.Load())
	b.WriteString("\r\n# Keyspace\r\n")
	if keys := s.cache.CountKeys(); keys > 0 {
		b.WriteString("db0:keys=" + strconv.FormatInt(keys, 10) +
			",expires=" + strconv.FormatInt(s.cache.CountExpires(), 10) + ",avg_ttl=0\r\n")
	}
	return respBulk(b.String())
}

// Version — версия сервера (INFO imcs_version).
var Version = "1.1.0"

func portOf(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[i+1:]
	}
	return addr
}
