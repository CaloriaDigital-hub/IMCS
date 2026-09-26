package AOF

import (
	"bufio"
	"hash/crc64"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unsafe"
)

const (
	writeBufSize  = 64 * 1024   // буфер bufio.Writer
	channelSize   = 4096        // размер очереди записей
	maxBatch      = 1024        // записей за один захват a.mu
	flushInterval = time.Second // fsync раз в секунду

	recordMarker = '@'
	maxHeaderLen = 128
	// MaxKeyLen / MaxValueLen — пределы, совпадающие с proto-max-bulk-len Redis.
	MaxKeyLen   = 512 << 20
	MaxValueLen = 512 << 20
)

// CRC64 таблица — ECMA стандарт.
var crcTable = crc64.MakeTable(crc64.ECMA)

// NewAOF открывает (или создаёт) журнал dir/journal.aof.
func NewAOF(dir string) (*AOF, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "journal.aof")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	a := &AOF{
		dir:     dir,
		path:    path,
		file:    f,
		writer:  bufio.NewWriterSize(f, writeBufSize),
		size:    st.Size(),
		writeCh: make(chan record, channelSize),
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
	a.lastRewriteSize.Store(st.Size())

	go a.backgroundWriter()
	return a, nil
}

// Close дописывает очередь, делает fsync и закрывает файл. Повторный вызов — no-op.
func (a *AOF) Close() error {
	a.closeMu.Lock()
	if a.closed.Load() {
		a.closeMu.Unlock()
		return nil
	}
	a.closed.Store(true)
	a.closeMu.Unlock()

	close(a.stopCh)
	<-a.done

	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.Close()
}

// Write ставит запись в очередь. Возвращает ошибку, если журнал закрыт или
// предыдущая запись на диск упала — вызывающий не должен считать операцию
// сохранённой.
func (a *AOF) Write(cmd, key, value string, expire int64) error {
	a.closeMu.RLock()
	defer a.closeMu.RUnlock()

	if a.closed.Load() {
		return ErrClosed
	}
	if err := a.Err(); err != nil {
		return err
	}
	a.writeCh <- record{cmd: cmd, key: key, value: value, expire: expire}
	return nil
}

// Err возвращает ошибку, из-за которой журнал перестал принимать записи.
func (a *AOF) Err() error {
	if p := a.failErr.Load(); p != nil {
		return *p
	}
	if a.closed.Load() {
		return ErrClosed
	}
	return nil
}

// Size — текущий размер журнала в байтах (включая ещё не сброшенный буфер).
func (a *AOF) Size() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.size
}

// LastRewriteSize — размер журнала после последнего rewrite (или при открытии).
func (a *AOF) LastRewriteSize() int64 {
	return a.lastRewriteSize.Load()
}

func (a *AOF) fail(err error) {
	if a.failErr.CompareAndSwap(nil, &err) {
		log.Printf("AOF: write error, write commands are disabled until restart: %v", err)
	}
}

// backgroundWriter — единственная горутина, которая пишет в файл.
func (a *AOF) backgroundWriter() {
	defer close(a.done)

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]record, 0, maxBatch)
	lastSync := time.Now()

	for {
		select {
		case r := <-a.writeCh:
			batch = a.drain(append(batch[:0], r))
			a.writeBatch(batch)
			// Под постоянной нагрузкой канал никогда не пустеет, и тикер может
			// не получить управление — поэтому проверяем время и здесь.
			if time.Since(lastSync) >= flushInterval {
				a.sync()
				lastSync = time.Now()
			}

		case <-ticker.C:
			a.sync()
			lastSync = time.Now()

		case <-a.stopCh:
			for {
				batch = a.drain(batch[:0])
				if len(batch) == 0 {
					break
				}
				a.writeBatch(batch)
			}
			a.sync()
			return
		}
	}
}

// drain неблокирующе добирает записи из очереди, пока batch не заполнен.
func (a *AOF) drain(batch []record) []record {
	for len(batch) < maxBatch {
		select {
		case r := <-a.writeCh:
			batch = append(batch, r)
		default:
			return batch
		}
	}
	return batch
}

func (a *AOF) writeBatch(batch []record) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.failErr.Load() != nil {
		return
	}
	for _, r := range batch {
		var n int
		var err error
		a.scratch, n, err = writeRecord(a.writer, a.scratch, r)
		if err != nil {
			a.fail(err)
			return
		}
		a.size += int64(n)
		if a.rewriting {
			a.rewriteBuf = append(a.rewriteBuf, r)
		}
	}
	a.dirty = true
}

func (a *AOF) sync() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.dirty || a.failErr.Load() != nil {
		return
	}
	if err := a.writer.Flush(); err != nil {
		a.fail(err)
		return
	}
	if err := a.file.Sync(); err != nil {
		a.fail(err)
		return
	}
	a.dirty = false
}

// writeRecord сериализует запись (бинарно-безопасный формат):
//
//	@<cmd> <expire> <keylen> <vallen> <crc64hex>\n<key><value>\n
//
// CRC64 считается по "<cmd> <expire> <keylen> <vallen>" + key + value.
// Возвращает переиспользуемый scratch-буфер и число записанных байт.
func writeRecord(w *bufio.Writer, scratch []byte, r record) ([]byte, int, error) {
	buf := append(scratch[:0], recordMarker)
	buf = append(buf, r.cmd...)
	buf = append(buf, ' ')
	buf = strconv.AppendInt(buf, r.expire, 10)
	buf = append(buf, ' ')
	buf = strconv.AppendInt(buf, int64(len(r.key)), 10)
	buf = append(buf, ' ')
	buf = strconv.AppendInt(buf, int64(len(r.value)), 10)

	crc := crc64.Update(0, crcTable, buf[1:])
	crc = crc64.Update(crc, crcTable, stringBytes(r.key))
	crc = crc64.Update(crc, crcTable, stringBytes(r.value))

	buf = append(buf, ' ')
	buf = strconv.AppendUint(buf, crc, 16)
	buf = append(buf, '\n')

	if _, err := w.Write(buf); err != nil {
		return buf, 0, err
	}
	if _, err := w.WriteString(r.key); err != nil {
		return buf, 0, err
	}
	if _, err := w.WriteString(r.value); err != nil {
		return buf, 0, err
	}
	if err := w.WriteByte('\n'); err != nil {
		return buf, 0, err
	}
	return buf, len(buf) + len(r.key) + len(r.value) + 1, nil
}

// stringBytes — представление строки как []byte без копирования.
// Только для чтения (CRC).
func stringBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
