package imcs

import (
	"bufio"
	"errors"
	"hash/crc64"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustOpen(t *testing.T, dir string, opts Options) *DB {
	t.Helper()
	db, err := OpenWithOptions(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func journalSize(t *testing.T, dir string) int64 {
	st, err := os.Stat(filepath.Join(dir, "journal.aof"))
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

// Раньше: replay писал в журнал, держа его лок → дедлок при >4096 записях.
func TestOpenLargeJournalDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})
	for i := 0; i < 20000; i++ {
		db.Set("k"+strconv.Itoa(i), "v", 0)
	}
	db.Close()

	done := make(chan *DB, 1)
	go func() {
		db, _ := Open(dir)
		done <- db
	}()
	select {
	case db := <-done:
		defer db.Close()
		if db.Len() != 20000 {
			t.Fatalf("loaded %d keys", db.Len())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open hangs on a journal with 20000 records")
	}
}

// Раньше: каждый рестарт дописывал весь журнал в самого себя.
func TestRestartDoesNotGrowJournal(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})
	for i := 0; i < 100; i++ {
		db.Set("k"+strconv.Itoa(i), "v", 0)
	}
	db.Close()
	size := journalSize(t, dir)
	for i := 0; i < 3; i++ {
		mustOpen(t, dir, Options{}).Close()
	}
	if got := journalSize(t, dir); got != size {
		t.Fatalf("journal grew on restart: %d -> %d", size, got)
	}
}

// Раньше: '\n' в значении обрезал журнал, '|' в ключе терял ключ, >16MB ломало загрузку.
func TestValuesSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	want := map[string]string{
		"json":   "{\n  \"a\": 1\n}",
		"user|1": "alice|admin",
		"crlf":   "a\r\nb",
		"bin":    "\x00\x01\xff",
		"big":    strings.Repeat("x", 17<<20),
	}
	db := mustOpen(t, dir, Options{})
	for k, v := range want {
		if err := db.Set(k, v, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 50; i++ {
		db.Set("after"+strconv.Itoa(i), "x", 0)
	}
	db.Close()

	db = mustOpen(t, dir, Options{})
	defer db.Close()
	for k, v := range want {
		if got, ok := db.Get(k); !ok || got != v {
			t.Errorf("%q lost or corrupted after restart (found=%v, len=%d)", k, ok, len(got))
		}
	}
	if n := db.Exists(func() []string {
		var ks []string
		for i := 0; i < 50; i++ {
			ks = append(ks, "after"+strconv.Itoa(i))
		}
		return ks
	}()...); n != 50 {
		t.Errorf("%d/50 keys written after special values survived", n)
	}
}

// Раньше: FLUSHALL / EXPIRE / PERSIST не попадали в журнал, INCR терял TTL.
func TestAllMutationsArePersisted(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})
	db.Set("flushed", "v", 0)
	db.FlushAll()
	db.Set("expiring", "v", 0)
	db.Expire("expiring", time.Hour)
	db.Set("persisted", "v", time.Hour)
	db.Persist("persisted")
	db.Set("cnt", "0", time.Hour)
	db.Incr("cnt")
	db.Set("app", "a", time.Hour)
	db.Append("app", "b")
	db.Set("gone", "v", 0)
	db.Expire("gone", -1)
	db.Set("old", "v", time.Hour)
	db.Rename("old", "new")
	db.Close()

	db = mustOpen(t, dir, Options{})
	defer db.Close()
	if db.Exists("flushed", "gone", "old") != 0 {
		t.Error("FLUSHALL / EXPIRE -1 / RENAME source came back after restart")
	}
	if db.TTL("expiring") < 3590 {
		t.Errorf("EXPIRE lost: ttl=%d", db.TTL("expiring"))
	}
	if db.TTL("persisted") != -1 {
		t.Errorf("PERSIST lost: ttl=%d", db.TTL("persisted"))
	}
	if v, _ := db.Get("cnt"); v != "1" || db.TTL("cnt") < 3590 {
		t.Errorf("INCR: value=%q ttl=%d", v, db.TTL("cnt"))
	}
	if v, _ := db.Get("app"); v != "ab" || db.TTL("app") < 3590 {
		t.Errorf("APPEND: value=%q ttl=%d", v, db.TTL("app"))
	}
	if v, _ := db.Get("new"); v != "v" || db.TTL("new") < 3590 {
		t.Errorf("RENAME: value=%q ttl=%d", v, db.TTL("new"))
	}
}

// Ключ, перезаписанный значением с TTL, не воскресает после истечения и рестарта.
func TestExpiredOverwriteDoesNotResurrect(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})
	db.Set("k", "permanent", 0)
	db.Set("k", "temporary", 50*time.Millisecond)
	db.Close()
	time.Sleep(100 * time.Millisecond)

	db = mustOpen(t, dir, Options{})
	defer db.Close()
	if v, ok := db.Get("k"); ok {
		t.Fatalf("old value resurrected: %q", v)
	}
}

// Журнал старого формата (как в ./cache-files) загружается и мигрирует.
func TestLegacyJournalMigration(t *testing.T) {
	dir := t.TempDir()
	tab := crc64.MakeTable(crc64.ECMA)
	var b strings.Builder
	for _, p := range []string{"SET|a|0|1", "SET|b|0|2", "DEL|a|0|", "SET|b|0|3"} {
		b.WriteString(strconv.FormatUint(crc64.Checksum([]byte(p), tab), 16) + "|" + p + "\n")
	}
	os.WriteFile(filepath.Join(dir, "journal.aof"), []byte(b.String()), 0644)

	db := mustOpen(t, dir, Options{})
	if v, _ := db.Get("b"); v != "3" || db.Exists("a") != 0 {
		t.Fatalf("legacy replay wrong: b=%q a=%d", v, db.Exists("a"))
	}
	db.Close()

	data, _ := os.ReadFile(filepath.Join(dir, "journal.aof"))
	if len(data) == 0 || data[0] != '@' {
		t.Fatalf("journal was not migrated to the new format: %q", data)
	}
	db = mustOpen(t, dir, Options{})
	defer db.Close()
	if v, _ := db.Get("b"); v != "3" {
		t.Fatalf("after migration b=%q", v)
	}
}

func TestCorruptJournalRefusesToOpen(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, Options{})
	for i := 0; i < 10; i++ {
		db.Set("k"+strconv.Itoa(i), "value-"+strconv.Itoa(i), 0)
	}
	db.Close()

	path := filepath.Join(dir, "journal.aof")
	data, _ := os.ReadFile(path)
	data[strings.Index(string(data), "value-5")] = 'X'
	os.WriteFile(path, data, 0644)

	if _, err := Open(dir); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	db = mustOpen(t, dir, Options{RepairAOF: true})
	defer db.Close()
	if db.Len() != 5 {
		t.Fatalf("repair kept %d keys, want 5", db.Len())
	}
}

func TestAutoRewrite(t *testing.T) {
	old := autoRewriteCheckEvery
	autoRewriteCheckEvery = 20 * time.Millisecond
	defer func() { autoRewriteCheckEvery = old }()

	dir := t.TempDir()
	db := mustOpen(t, dir, Options{AutoRewriteMinSize: 64 << 10})
	defer db.Close()
	for i := 0; i < 20000; i++ {
		db.Set("k"+strconv.Itoa(i%10), strconv.Itoa(i), 0)
	}
	deadline := time.Now().Add(3 * time.Second)
	for db.aof.Size() > 64<<10 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if size := db.aof.Size(); size > 64<<10 {
		t.Fatalf("journal not compacted: %d bytes for 10 keys", size)
	}
	if v, _ := db.Get("k9"); v != "19999" {
		t.Fatalf("k9=%q", v)
	}
}

// Раньше: Options.Password молча игнорировался в ListenAndServe.
func TestListenAndServeUsesPassword(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()

	db := mustOpen(t, t.TempDir(), Options{Password: "secret"})
	served := make(chan error, 1)
	go func() { served <- db.ListenAndServe(addr) }()

	var conn net.Conn
	var err error
	for i := 0; i < 100; i++ {
		if conn, err = net.Dial("tcp", addr); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("GET k\r\n"))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	conn.Close()
	if !strings.HasPrefix(line, "-NOAUTH") {
		t.Fatalf("password not enforced: %q", line)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatalf("ListenAndServe after Close: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal("second Close must be a no-op")
	}
}
