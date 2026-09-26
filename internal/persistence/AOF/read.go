package AOF

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/crc64"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"unsafe"
)

const maxLegacyLine = 16 * 1024 * 1024 // лимит строки старого формата

// errIncomplete — запись оборвана концом файла (типичный хвост после краша).
var errIncomplete = errors.New("incomplete record")

// Read применяет все записи журнала к apply.
//
// Поддерживает новый бинарный формат и старый строковый (crc|cmd|key|expire|value),
// так что существующие журналы загружаются без конвертации.
//
// Оборванная или битая последняя запись (хвост после краша) отрезается всегда.
// Повреждение в середине файла — это ошибка ErrCorrupt: молча выкидывать всё,
// что идёт после, нельзя. С repair=true журнал обрезается по месту повреждения.
//
// Вызывать до первого Write.
func (a *AOF) Read(apply func(cmd, key, value string, expire int64), repair bool) (*ReadResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	f, err := os.Open(a.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := st.Size()

	res := &ReadResult{}
	br := bufio.NewReaderSize(f, 1<<20)
	var off int64

	for {
		b, err := br.Peek(1)
		if err == io.EOF {
			break
		}
		if err != nil {
			return res, err
		}

		var (
			r      record
			n      int64
			perr   error
			legacy = b[0] != recordMarker
		)
		if legacy {
			r, n, perr = readLegacy(br)
		} else {
			r, n, perr = readRecord(br, fileSize-off)
		}

		if perr != nil {
			_, peekErr := br.Peek(1)
			tail := errors.Is(perr, errIncomplete) || peekErr == io.EOF
			if !tail && !repair {
				return res, fmt.Errorf("%w at offset %d of %s: %v "+
					"(start with repair enabled to truncate the journal at this offset; "+
					"everything after it will be lost)", ErrCorrupt, off, a.path, perr)
			}
			res.CorruptEntries++
			res.Truncated = true
			res.TruncatedAt = off
			log.Printf("AOF: %v at offset %d, truncating journal (%d bytes discarded)",
				perr, off, fileSize-off)
			break
		}

		res.ValidEntries++
		if legacy {
			res.LegacyEntries++
		}
		off += n
		apply(r.cmd, r.key, r.value, r.expire)
	}

	if res.Truncated {
		// Windows: Truncate падает на хэндле с O_APPEND — открываем отдельный.
		tf, err := os.OpenFile(a.path, os.O_RDWR, 0644)
		if err != nil {
			return res, err
		}
		truncErr := tf.Truncate(res.TruncatedAt)
		if truncErr == nil {
			truncErr = tf.Sync()
		}
		_ = tf.Close()
		if truncErr != nil {
			return res, truncErr
		}
		a.size = res.TruncatedAt
		a.lastRewriteSize.Store(res.TruncatedAt)
	}

	return res, nil
}

// readRecord читает запись нового формата. remaining — байт до конца файла.
func readRecord(br *bufio.Reader, remaining int64) (record, int64, error) {
	line, err := br.ReadSlice('\n')
	switch {
	case err == io.EOF:
		return record{}, 0, errIncomplete
	case err == bufio.ErrBufferFull || len(line) > maxHeaderLen:
		return record{}, 0, errors.New("record header too long")
	case err != nil:
		return record{}, 0, err
	}

	hdr := line[1 : len(line)-1] // без '@' и '\n'
	parts := bytes.Split(hdr, []byte{' '})
	if len(parts) != 5 {
		return record{}, 0, errors.New("malformed record header")
	}
	cmd := string(parts[0])
	switch cmd {
	case CmdSet, CmdDel, CmdExpireAt, CmdFlushAll:
	default:
		return record{}, 0, fmt.Errorf("unknown command %q", cmd)
	}
	expire, err1 := strconv.ParseInt(string(parts[1]), 10, 64)
	klen, err2 := strconv.ParseInt(string(parts[2]), 10, 64)
	vlen, err3 := strconv.ParseInt(string(parts[3]), 10, 64)
	stored, err4 := strconv.ParseUint(string(parts[4]), 16, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil ||
		klen < 0 || klen > MaxKeyLen || vlen < 0 || vlen > MaxValueLen {
		return record{}, 0, errors.New("malformed record header")
	}

	// CRC заголовка — до следующего чтения, ReadSlice переиспользует буфер.
	crc := crc64.Update(0, crcTable, hdr[:len(hdr)-len(parts[4])-1])

	headerLen := int64(len(line))
	bodyLen := klen + vlen + 1
	if headerLen+bodyLen > remaining {
		return record{}, 0, errIncomplete
	}

	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(br, body); err != nil {
		return record{}, 0, errIncomplete
	}
	if body[bodyLen-1] != '\n' {
		return record{}, 0, errors.New("missing record terminator")
	}
	crc = crc64.Update(crc, crcTable, body[:klen+vlen])
	if crc != stored {
		return record{}, 0, fmt.Errorf("CRC mismatch (stored=%x computed=%x)", stored, crc)
	}

	r := record{cmd: cmd, key: string(body[:klen]), expire: expire}
	if vlen > 0 {
		// body больше не меняется — значение ссылается на него без копии.
		r.value = unsafe.String(&body[klen], int(vlen))
	}
	return r, headerLen + bodyLen, nil
}

// readLegacy читает строку старого формата: crc64hex|cmd|key|expire|value
// (или совсем старого, без CRC: cmd|key|expire|value).
func readLegacy(br *bufio.Reader) (record, int64, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxLegacyLine {
			return record{}, 0, errors.New("legacy line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF {
			return record{}, 0, errIncomplete
		}
		if err != nil {
			return record{}, 0, err
		}
		break
	}
	n := int64(len(buf))
	line := strings.TrimRight(string(buf), "\r\n")

	sep := strings.IndexByte(line, '|')
	if sep < 1 {
		return record{}, 0, errors.New("legacy line without separator")
	}
	payload := line[sep+1:]
	stored, err := strconv.ParseUint(line[:sep], 16, 64)
	if err != nil {
		// Совсем старый формат без CRC.
		r, ok := parseLegacyPayload(line)
		if !ok {
			return record{}, 0, errors.New("unrecognized legacy line")
		}
		return r, n, nil
	}
	if computed := crc64.Checksum([]byte(payload), crcTable); computed != stored {
		return record{}, 0, fmt.Errorf("legacy CRC mismatch (stored=%x computed=%x)", stored, computed)
	}
	r, ok := parseLegacyPayload(payload)
	if !ok {
		return record{}, 0, errors.New("malformed legacy payload")
	}
	return r, n, nil
}

func parseLegacyPayload(payload string) (record, bool) {
	parts := strings.SplitN(payload, "|", 4)
	if len(parts) < 4 {
		return record{}, false
	}
	switch parts[0] {
	case "SET", "DEL", "GET", "INCR", "EXPIRE", "APPEND":
	default:
		return record{}, false
	}
	expire, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return record{}, false
	}
	return record{cmd: parts[0], key: parts[1], value: parts[3], expire: expire}, true
}
