package AOF

import (
	"crypto/rand"
	"errors"
	"fmt"
	"hash/crc64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type entry struct {
	value  string
	expire int64
}

// load открывает журнал и воспроизводит его в map (SET/DEL/EXPIREAT/FLUSHALL).
func load(t *testing.T, dir string, repair bool) (map[string]entry, *ReadResult, error) {
	t.Helper()
	a, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	state := map[string]entry{}
	res, err := a.Read(func(cmd, key, value string, expire int64) {
		switch cmd {
		case CmdSet:
			state[key] = entry{value, expire}
		case CmdDel:
			delete(state, key)
		case CmdExpireAt:
			if e, ok := state[key]; ok {
				e.expire = expire
				state[key] = e
			}
		case CmdFlushAll:
			clear(state)
		}
	}, repair)
	return state, res, err
}

func mustOpen(t *testing.T, dir string) *AOF {
	t.Helper()
	a, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func journal(dir string) string { return filepath.Join(dir, "journal.aof") }

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return string(b)
}

// Бинарные данные, '\n', '|', пустые значения — всё должно пережить рестарт.
func TestRoundTripBinarySafe(t *testing.T) {
	dir := t.TempDir()
	want := map[string]entry{
		"plain":           {"value", 0},
		"json":            {"{\n  \"a\": 1\n}\n", 0},
		"user|1":          {"a|b|c", 123456789},
		"key with spaces": {"\r\n\r\n", 0},
		"empty":           {"", 0},
		"binary":          {randomString(4096), 0},
		"\x00\xff":        {"\x00", 0},
		"big":             {strings.Repeat("x", 17<<20), 0}, // больше старого лимита 16 МБ
	}

	a := mustOpen(t, dir)
	for k, e := range want {
		if err := a.Write(CmdSet, k, e.value, e.expire); err != nil {
			t.Fatal(err)
		}
	}
	a.Write(CmdSet, "after", "ok", 0)
	a.Close()
	want["after"] = entry{"ok", 0}

	got, res, err := load(t, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated || res.CorruptEntries != 0 {
		t.Fatalf("unexpected corruption: %+v", res)
	}
	for k, e := range want {
		if got[k] != e {
			t.Errorf("key %q: got len=%d expire=%d, want len=%d expire=%d",
				k, len(got[k].value), got[k].expire, len(e.value), e.expire)
		}
	}
}

// Оборванная последняя запись (краш посреди записи) отрезается, остальное цело.
func TestTailTruncation(t *testing.T) {
	dir := t.TempDir()
	a := mustOpen(t, dir)
	for i := 0; i < 100; i++ {
		a.Write(CmdSet, "k"+strconv.Itoa(i), strings.Repeat("v", 1000), 0)
	}
	a.Close()

	data, _ := os.ReadFile(journal(dir))
	os.WriteFile(journal(dir), data[:len(data)-500], 0644)

	got, res, err := load(t, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.ValidEntries != 99 || len(got) != 99 {
		t.Fatalf("want 99 entries and truncation, got %+v (keys=%d)", res, len(got))
	}
	if st, _ := os.Stat(journal(dir)); st.Size() != res.TruncatedAt {
		t.Fatalf("file size %d, want %d", st.Size(), res.TruncatedAt)
	}

	// После обрезки журнал снова пригоден для дозаписи.
	a = mustOpen(t, dir)
	a.Write(CmdSet, "new", "1", 0)
	a.Close()
	got, res, err = load(t, dir, false)
	if err != nil || res.Truncated || len(got) != 100 || got["new"].value != "1" {
		t.Fatalf("append after truncation broken: err=%v res=%+v keys=%d", err, res, len(got))
	}
}

// Битая последняя запись с полной длиной — тоже хвост.
func TestCorruptLastRecordIsTail(t *testing.T) {
	dir := t.TempDir()
	a := mustOpen(t, dir)
	for i := 0; i < 10; i++ {
		a.Write(CmdSet, "k"+strconv.Itoa(i), "value", 0)
	}
	a.Close()

	data, _ := os.ReadFile(journal(dir))
	data[len(data)-3] ^= 0xff
	os.WriteFile(journal(dir), data, 0644)

	got, res, err := load(t, dir, false)
	if err != nil || !res.Truncated || len(got) != 9 {
		t.Fatalf("err=%v res=%+v keys=%d", err, res, len(got))
	}
}

// Повреждение в середине — ошибка, файл не трогается. С repair — обрезка.
func TestMidFileCorruption(t *testing.T) {
	dir := t.TempDir()
	a := mustOpen(t, dir)
	for i := 0; i < 100; i++ {
		a.Write(CmdSet, "k"+strconv.Itoa(i), "value-"+strconv.Itoa(i), 0)
	}
	a.Close()

	data, _ := os.ReadFile(journal(dir))
	idx := strings.Index(string(data), "value-50")
	data[idx] = 'X'
	os.WriteFile(journal(dir), data, 0644)

	_, _, err := load(t, dir, false)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if st, _ := os.Stat(journal(dir)); st.Size() != int64(len(data)) {
		t.Fatal("journal must not be modified without repair")
	}

	got, res, err := load(t, dir, true)
	if err != nil || !res.Truncated || len(got) != 50 {
		t.Fatalf("repair: err=%v res=%+v keys=%d", err, res, len(got))
	}
}

// Журналы старого строкового формата загружаются.
func TestLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	legacy := func(payload string) string {
		return strconv.FormatUint(crc64.Checksum([]byte(payload), crcTable), 16) + "|" + payload + "\n"
	}
	content := legacy("SET|a|0|1") +
		legacy("SET|b|1774745877566160400|2") +
		legacy("DEL|a|0|") +
		"SET|c|0|3\n" // совсем старый формат без CRC
	os.WriteFile(journal(dir), []byte(content), 0644)

	// Новые записи дописываются после старых.
	a := mustOpen(t, dir)
	a.Write(CmdSet, "d", "4", 0)
	a.Close()

	got, res, err := load(t, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.LegacyEntries != 4 || res.ValidEntries != 5 {
		t.Fatalf("res=%+v", res)
	}
	if _, ok := got["a"]; ok || got["b"].value != "2" || got["b"].expire != 1774745877566160400 ||
		got["c"].value != "3" || got["d"].value != "4" {
		t.Fatalf("state=%v", got)
	}
}

// Rewrite под параллельной записью не теряет и не искажает данные.
// Модель повторяет контракт кеша: изменение и Write — под одним локом ключа.
func TestRewriteUnderConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	a := mustOpen(t, dir)

	// Каждый writer пишет свои уникальные ключи (потерянная запись = пропавший
	// ключ) и иногда удаляет/меняет TTL у своих недавних ключей.
	const writers = 8
	type shardModel struct {
		sync.Mutex
		m map[string]entry
	}
	var (
		models [writers]shardModel
		stop   atomic.Bool
		wg     sync.WaitGroup
	)
	for g := 0; g < writers; g++ {
		models[g].m = map[string]entry{}
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			m := &models[g]
			for i := 0; !stop.Load(); i++ {
				key := fmt.Sprintf("g%d:%d", g, i)
				old := fmt.Sprintf("g%d:%d", g, i-3)
				m.Lock()
				switch i % 5 {
				case 0:
					delete(m.m, old)
					a.Write(CmdDel, old, "", 0)
				case 1:
					if e, ok := m.m[old]; ok {
						e.expire = int64(i)
						m.m[old] = e
						a.Write(CmdExpireAt, old, "", int64(i))
					}
				}
				m.m[key] = entry{"v" + key, 0}
				a.Write(CmdSet, key, "v"+key, 0)
				m.Unlock()
			}
		}(g)
	}

	snapshot := func(emit func(cmd, key, value string, expire int64)) {
		for g := range models {
			m := &models[g]
			m.Lock()
			cp := make(map[string]entry, len(m.m))
			for k, e := range m.m {
				cp[k] = e
			}
			m.Unlock()
			for k, e := range cp {
				emit(CmdSet, k, e.value, e.expire)
			}
		}
	}
	for r := 0; r < 5; r++ {
		time.Sleep(20 * time.Millisecond)
		if err := a.Rewrite(snapshot); err != nil {
			t.Fatal(err)
		}
	}
	// Останавливаемся сразу после rewrite: хвост журнала — это в основном
	// записи, пришедшие во время последнего rewrite.
	stop.Store(true)
	wg.Wait()
	a.Close()

	got, _, err := load(t, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	total, bad := 0, 0
	for g := range models {
		for k, e := range models[g].m {
			total++
			if got[k] != e {
				bad++
			}
		}
	}
	if bad > 0 || len(got) != total {
		t.Fatalf("journal diverged from memory: %d of %d keys differ, journal has %d keys", bad, total, len(got))
	}
}

func TestCloseRejectsWrites(t *testing.T) {
	a := mustOpen(t, t.TempDir())
	a.Close()
	if err := a.Write(CmdSet, "k", "v", 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// Ошибка записи на диск не глотается: журнал перестаёт принимать записи.
func TestWriteErrorIsSticky(t *testing.T) {
	a := mustOpen(t, t.TempDir())
	a.mu.Lock()
	a.file.Close() // имитация отвалившегося диска
	a.mu.Unlock()

	a.Write(CmdSet, "k", "v", 0)
	deadline := time.Now().Add(3 * flushInterval)
	for a.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.Err() == nil {
		t.Fatal("write error was swallowed")
	}
	if err := a.Write(CmdSet, "k2", "v", 0); err == nil {
		t.Fatal("writes must be rejected after a disk error")
	}
	close(a.stopCh)
	<-a.done
}
