package server

import (
	"bufio"
	"io"
	"strconv"
	"unsafe"

	storage "github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"
)

// protocolError — клиент нарушил протокол; соединение закрывается.
type protocolError string

func (e protocolError) Error() string { return string(e) }

type limits struct {
	maxArgs int
	maxBulk int
}

// readCommand читает одну команду:
//   - multibulk: *3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n (любой Redis-клиент)
//   - inline:    SET key "value with spaces"\r\n (telnet, redis-cli в pipe-режиме)
func readCommand(r *bufio.Reader, lim limits) ([]string, error) {
	b, err := r.Peek(1)
	if err != nil {
		return nil, err
	}
	if b[0] == '*' {
		return readMultibulk(r, lim)
	}
	return readInline(r)
}

func readMultibulk(r *bufio.Reader, lim limits) ([]string, error) {
	line, err := readLine(r, maxInlineLen)
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(string(line[1:]))
	if err != nil || count > lim.maxArgs {
		if err == nil && lim.maxArgs == maxArgsNoAuth {
			return nil, protocolError("unauthenticated multibulk length")
		}
		return nil, protocolError("invalid multibulk length")
	}
	if count <= 0 {
		return nil, nil
	}

	// Ёмкость не берём из заголовка на веру — растёт по мере прихода данных.
	args := make([]string, 0, min(count, 16))
	for i := 0; i < count; i++ {
		line, err := readLine(r, maxInlineLen)
		if err != nil {
			return nil, err
		}
		if len(line) == 0 || line[0] != '$' {
			return nil, protocolError("expected '$'")
		}
		size, err := strconv.Atoi(string(line[1:]))
		if err != nil || size < 0 || size > lim.maxBulk {
			if err == nil && size > 0 && lim.maxBulk == maxBulkNoAuth {
				return nil, protocolError("unauthenticated bulk length")
			}
			return nil, protocolError("invalid bulk length")
		}
		arg, err := readBulk(r, size)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	return args, nil
}

// readBulk читает size байт + CRLF. Большие аргументы читаются кусками, так что
// память выделяется по мере фактического прихода данных, а не по заявленной длине.
func readBulk(r *bufio.Reader, size int) (string, error) {
	var buf []byte
	if size <= bigArgChunkLen {
		buf = make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
	} else {
		var chunks [][]byte
		for left := size + 2; left > 0; {
			chunk := make([]byte, min(left, bigArgChunkLen))
			if _, err := io.ReadFull(r, chunk); err != nil {
				return "", err
			}
			chunks = append(chunks, chunk)
			left -= len(chunk)
		}
		buf = make([]byte, 0, size+2)
		for _, ch := range chunks {
			buf = append(buf, ch...)
		}
	}
	if buf[size] != '\r' || buf[size+1] != '\n' {
		return "", protocolError("expected CRLF after bulk")
	}
	if size == 0 {
		return "", nil
	}
	// buf больше нигде не используется и не меняется — строка без второй копии.
	return unsafe.String(&buf[0], size), nil
}

func readInline(r *bufio.Reader) ([]string, error) {
	line, err := readLine(r, maxInlineLen)
	if err != nil {
		return nil, err
	}
	args, ok := splitArgs(line)
	if !ok {
		return nil, protocolError("unbalanced quotes in request")
	}
	return args, nil
}

// readLine читает строку до \n (без \r\n). Результат может ссылаться на буфер
// reader'а — использовать до следующего чтения.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		buf := append([]byte(nil), line...)
		for err == bufio.ErrBufferFull {
			if len(buf) > max {
				return nil, protocolError("too big inline request")
			}
			line, err = r.ReadSlice('\n')
			buf = append(buf, line...)
		}
		line = buf
	}
	if err != nil {
		return nil, err
	}
	if len(line) > max+2 {
		return nil, protocolError("too big inline request")
	}
	line = line[:len(line)-1]
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return line, nil
}

// splitArgs разбирает inline-команду как sdssplitargs в Redis:
// пробелы разделяют аргументы, "..." поддерживает \n \r \t \b \a \xHH \", '...' — \'.
func splitArgs(line []byte) ([]string, bool) {
	var args []string
	i := 0
	for {
		for i < len(line) && isSpace(line[i]) {
			i++
		}
		if i >= len(line) {
			return args, true
		}

		var cur []byte
		switch line[i] {
		case '"':
			i++
			for {
				if i >= len(line) {
					return nil, false
				}
				ch := line[i]
				if ch == '\\' && i+3 < len(line) && line[i+1] == 'x' && isHex(line[i+2]) && isHex(line[i+3]) {
					cur = append(cur, hexVal(line[i+2])<<4|hexVal(line[i+3]))
					i += 4
					continue
				}
				if ch == '\\' && i+1 < len(line) {
					cur = append(cur, unescape(line[i+1]))
					i += 2
					continue
				}
				if ch == '"' {
					i++
					if i < len(line) && !isSpace(line[i]) {
						return nil, false
					}
					break
				}
				cur = append(cur, ch)
				i++
			}
		case '\'':
			i++
			for {
				if i >= len(line) {
					return nil, false
				}
				ch := line[i]
				if ch == '\\' && i+1 < len(line) && line[i+1] == '\'' {
					cur = append(cur, '\'')
					i += 2
					continue
				}
				if ch == '\'' {
					i++
					if i < len(line) && !isSpace(line[i]) {
						return nil, false
					}
					break
				}
				cur = append(cur, ch)
				i++
			}
		default:
			for i < len(line) && !isSpace(line[i]) {
				cur = append(cur, line[i])
				i++
			}
		}
		args = append(args, string(cur))
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func unescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'b':
		return '\b'
	case 'a':
		return '\a'
	default:
		return c
	}
}

// === RESP ответы ===

var (
	replyOK      = []byte("+OK\r\n")
	replyPong    = []byte("+PONG\r\n")
	replyNilBulk = []byte("$-1\r\n")
	replyEmpty   = []byte("*0\r\n")
	replyZero    = []byte(":0\r\n")
	replyOne     = []byte(":1\r\n")
)

func respBulk(s string) []byte {
	buf := make([]byte, 0, len(s)+16)
	buf = append(buf, '$')
	buf = strconv.AppendInt(buf, int64(len(s)), 10)
	buf = append(buf, '\r', '\n')
	buf = append(buf, s...)
	return append(buf, '\r', '\n')
}

// respError — ошибка с кодом: respError("ERR", "...") → -ERR ...\r\n
func respError(code, msg string) []byte {
	buf := make([]byte, 0, len(code)+len(msg)+4)
	buf = append(buf, '-')
	buf = append(buf, code...)
	buf = append(buf, ' ')
	buf = append(buf, msg...)
	return append(buf, '\r', '\n')
}

func respErr(msg string) []byte { return respError("ERR", msg) }

func respInt(n int64) []byte {
	switch n {
	case 0:
		return replyZero
	case 1:
		return replyOne
	}
	buf := make([]byte, 0, 24)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, n, 10)
	return append(buf, '\r', '\n')
}

func respSimple(msg string) []byte {
	buf := make([]byte, 0, len(msg)+3)
	buf = append(buf, '+')
	buf = append(buf, msg...)
	return append(buf, '\r', '\n')
}

func appendBulk(buf []byte, s string) []byte {
	buf = append(buf, '$')
	buf = strconv.AppendInt(buf, int64(len(s)), 10)
	buf = append(buf, '\r', '\n')
	buf = append(buf, s...)
	return append(buf, '\r', '\n')
}

func respArrayResults(items []storage.GetResult) []byte {
	buf := make([]byte, 0, 16+len(items)*16)
	buf = append(buf, '*')
	buf = strconv.AppendInt(buf, int64(len(items)), 10)
	buf = append(buf, '\r', '\n')
	for _, it := range items {
		if !it.Found {
			buf = append(buf, replyNilBulk...)
		} else {
			buf = appendBulk(buf, it.Value)
		}
	}
	return buf
}

func respArrayStrings(items []string) []byte {
	buf := make([]byte, 0, 16+len(items)*16)
	buf = append(buf, '*')
	buf = strconv.AppendInt(buf, int64(len(items)), 10)
	buf = append(buf, '\r', '\n')
	for _, s := range items {
		buf = appendBulk(buf, s)
	}
	return buf
}
