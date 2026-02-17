package AOF

import (
	"bufio"
	"hash/crc64"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	writeBufSize  = 64 * 1024   // 64KB буфер bufio.Writer
	channelSize   = 4096        // размер канала записей
	flushInterval = time.Second // fsync каждую секунду
)

// CRC64 таблица — ECMA стандарт.
var crcTable = crc64.MakeTable(crc64.ECMA)

// NewAOF создаёт новый AOF с буферизованной записью.
func NewAOF(dir string) (*AOF, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	filename := filepath.Join(dir, "journal.aof")

	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	a := &AOF{
		file:    f,
		dir:     dir,
		writer:  bufio.NewWriterSize(f, writeBufSize),
		writeCh: make(chan writeEntry, channelSize),
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}

	go a.backgroundWriter()

	return a, nil
}

// Close останавливает writer, сбрасывает буфер и закрывает файл.
func (a *AOF) Close() error {
	close(a.stopCh)
	<-a.done
	return a.file.Close()
}

// buildEntry собирает запись с CRC64.
// Формат: crc64hex|cmd|key|expire|value\n
func buildEntry(input WriteInput) []byte {
	var expire int64
	if input.TTL > 0 {
		expire = time.Now().Add(input.TTL).UnixNano()
	}

	payload := make([]byte, 0, len(input.Cmd)+len(input.Key)+len(input.Value)+32)
	payload = append(payload, input.Cmd...)
	payload = append(payload, '|')
	payload = append(payload, input.Key...)
	payload = append(payload, '|')
	payload = strconv.AppendInt(payload, expire, 10)
	payload = append(payload, '|')
	payload = append(payload, input.Value...)

	checksum := crc64.Checksum(payload, crcTable)
	crcHex := strconv.FormatUint(checksum, 16)

	entry := make([]byte, 0, len(crcHex)+1+len(payload)+1)
	entry = append(entry, crcHex...)
	entry = append(entry, '|')
	entry = append(entry, payload...)
	entry = append(entry, '\n')

	return entry
}

// Write формирует запись с CRC64 и отправляет в канал.
func (a *AOF) Write(input WriteInput) error {
	entry := buildEntry(input)

	select {
	case a.writeCh <- writeEntry{data: entry}:
		return nil
	case <-a.stopCh:
		return nil
	}
}

// processEntry пишет запись в основной файл и дублирует в rewrite buffer.
func (a *AOF) processEntry(data []byte) {
	a.writer.Write(data)

	// Если идёт rewrite — дублируем в буфер докатки
	if a.rewriting.Load() {
		a.rewriteMu.Lock()
		cp := make([]byte, len(data))
		copy(cp, data)
		a.rewriteBuf = append(a.rewriteBuf, cp)
		a.rewriteMu.Unlock()
	}
}

// backgroundWriter — единственная горутина, пишет в файл.
func (a *AOF) backgroundWriter() {
	defer close(a.done)

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case entry := <-a.writeCh:
			a.processEntry(entry.data)

			// Drain
			drained := true
			for drained {
				select {
				case e := <-a.writeCh:
					a.processEntry(e.data)
				default:
					drained = false
				}
			}

		case <-ticker.C:
			a.mu.Lock()
			a.writer.Flush()
			a.file.Sync()
			a.mu.Unlock()

		case <-a.stopCh:
			for {
				select {
				case e := <-a.writeCh:
					a.processEntry(e.data)
				default:
					a.writer.Flush()
					a.file.Sync()
					return
				}
			}
		}
	}
}
