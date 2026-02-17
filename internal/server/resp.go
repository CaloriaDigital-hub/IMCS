package server

import (
	"bufio"
	"io"
	"strconv"
)

// RESP types
const (
	respSimpleString = '+'
	respError        = '-'
	respInteger      = ':'
	respBulkString   = '$'
	respArray        = '*'
)

// readRESPCommand читает одну RESP-команду из reader.
// Поддерживает:
//   - Inline: SET key value\r\n (для telnet)
//   - Multibulk: *3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n (redis-cli)
func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	// Читаем первый байт чтобы определить формат
	b, err := reader.Peek(1)
	if err != nil {
		return nil, err
	}

	if b[0] == '*' {
		return readMultibulk(reader)
	}
	return readInline(reader)
}

// readMultibulk парсит RESP multibulk: *N\r\n($len\r\ndata\r\n)*N
func readMultibulk(reader *bufio.Reader) ([]string, error) {
	line, err := readLine(reader)
	if err != nil {
		return nil, err
	}

	if len(line) < 1 || line[0] != '*' {
		return nil, io.ErrUnexpectedEOF
	}

	count, err := strconv.Atoi(string(line[1:]))
	if err != nil || count < 0 {
		return nil, io.ErrUnexpectedEOF
	}

	if count == 0 {
		return nil, nil
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		line, err := readLine(reader)
		if err != nil {
			return nil, err
		}

		if len(line) < 1 || line[0] != '$' {
			return nil, io.ErrUnexpectedEOF
		}

		size, err := strconv.Atoi(string(line[1:]))
		if err != nil || size < 0 {
			return nil, io.ErrUnexpectedEOF
		}

		buf := make([]byte, size+2) // +2 для \r\n
		_, err = io.ReadFull(reader, buf)
		if err != nil {
			return nil, err
		}

		args = append(args, string(buf[:size]))
	}

	return args, nil
}

// readInline парсит inline команду: SET key value\r\n или SET key value\n
func readInline(reader *bufio.Reader) ([]string, error) {
	line, err := readLine(reader)
	if err != nil {
		return nil, err
	}

	if len(line) == 0 {
		return nil, nil
	}

	// Парсим слова из строки
	var args []string
	i := 0
	for i < len(line) {
		// Пропуск пробелов
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}

		j := i
		for j < len(line) && line[j] != ' ' {
			j++
		}
		args = append(args, string(line[i:j]))
		i = j
	}

	return args, nil
}

// readLine читает строку до \r\n или \n, возвращает без терминатора.
func readLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	// Убираем \r\n или \n
	n := len(line)
	if n >= 2 && line[n-2] == '\r' {
		line = line[:n-2]
	} else if n >= 1 {
		line = line[:n-1]
	}

	return line, nil
}

// === RESP Response Builders ===

// respOK возвращает +OK\r\n
func respOK() []byte {
	return []byte("+OK\r\n")
}

// respNilBulk возвращает $-1\r\n (nil bulk string)
func respNilBulk() []byte {
	return []byte("$-1\r\n")
}

// respBulk возвращает $len\r\ndata\r\n
func respBulk(s string) []byte {
	lenStr := strconv.Itoa(len(s))
	buf := make([]byte, 0, 1+len(lenStr)+2+len(s)+2)
	buf = append(buf, '$')
	buf = append(buf, lenStr...)
	buf = append(buf, '\r', '\n')
	buf = append(buf, s...)
	buf = append(buf, '\r', '\n')
	return buf
}

// respErrorMsg возвращает -ERR msg\r\n
func respErrorMsg(msg string) []byte {
	buf := make([]byte, 0, 5+len(msg)+2)
	buf = append(buf, "-ERR "...)
	buf = append(buf, msg...)
	buf = append(buf, '\r', '\n')
	return buf
}

// respInt возвращает :N\r\n
func respInt(n int64) []byte {
	s := strconv.FormatInt(n, 10)
	buf := make([]byte, 0, 1+len(s)+2)
	buf = append(buf, ':')
	buf = append(buf, s...)
	buf = append(buf, '\r', '\n')
	return buf
}

// respSimple возвращает +msg\r\n
func respSimple(msg string) []byte {
	buf := make([]byte, 0, 1+len(msg)+2)
	buf = append(buf, '+')
	buf = append(buf, msg...)
	buf = append(buf, '\r', '\n')
	return buf
}

// respArrayBulks возвращает RESP array из bulk strings (для MGET).
func respArrayBulks(items []struct {
	Value string
	Found bool
}) []byte {
	header := "*" + strconv.Itoa(len(items)) + "\r\n"
	buf := make([]byte, 0, len(header)+len(items)*32)
	buf = append(buf, header...)
	for _, item := range items {
		if !item.Found {
			buf = append(buf, "$-1\r\n"...)
		} else {
			buf = append(buf, '$')
			buf = append(buf, strconv.Itoa(len(item.Value))...)
			buf = append(buf, '\r', '\n')
			buf = append(buf, item.Value...)
			buf = append(buf, '\r', '\n')
		}
	}
	return buf
}

// respArrayStrings возвращает RESP array из строк (для KEYS).
func respArrayStrings(items []string) []byte {
	header := "*" + strconv.Itoa(len(items)) + "\r\n"
	buf := make([]byte, 0, len(header)+len(items)*32)
	buf = append(buf, header...)
	for _, s := range items {
		buf = append(buf, '$')
		buf = append(buf, strconv.Itoa(len(s))...)
		buf = append(buf, '\r', '\n')
		buf = append(buf, s...)
		buf = append(buf, '\r', '\n')
	}
	return buf
}
