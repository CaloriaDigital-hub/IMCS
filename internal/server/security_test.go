package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"
)

func dial(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	return conn, bufio.NewReader(conn)
}

func send(t *testing.T, conn net.Conn, r *bufio.Reader, cmd string) string {
	t.Helper()
	if _, err := conn.Write([]byte(cmd)); err != nil {
		t.Fatalf("write %q: %v", cmd, err)
	}
	resp, err := readRESPReply(r)
	if err != nil && !errors.Is(err, ErrServerReply) {
		t.Fatalf("read reply to %q: %v", cmd, err)
	}
	return resp
}

func assertAlive(t *testing.T, addr string) {
	t.Helper()
	conn, r := dial(t, addr)
	// С паролем до AUTH ответ — NOAUTH; для проверки «жив» этого достаточно.
	if resp := send(t, conn, r, "PING\r\n"); resp != "PONG" && !strings.HasPrefix(resp, "-NOAUTH") {
		t.Fatalf("server not responding: %q", resp)
	}
}

// Любая команда без аргументов — ошибка арности, а не паника.
func TestNoPanicOnMissingArgs(t *testing.T) {
	addr, _ := startTestServer(t)
	conn, r := dial(t, addr)
	for name, cmd := range commands {
		if name == "QUIT" {
			continue
		}
		resp := send(t, conn, r, name+"\r\n")
		if cmd.arity != 1 && cmd.arity != -1 && !strings.HasPrefix(resp, "-ERR wrong number of arguments") {
			t.Errorf("%s without args: %q", name, resp)
		}
	}
	assertAlive(t, addr)
}

// Пакет, который раньше ронял процесс до AUTH.
func TestBulkLengthOverflowBeforeAuth(t *testing.T) {
	addr, _ := startTestServer(t, WithAuth("secret"))
	conn, r := dial(t, addr)
	resp := send(t, conn, r, "*1\r\n$9223372036854775807\r\n")
	if !strings.HasPrefix(resp, "-ERR Protocol error") {
		t.Fatalf("got %q", resp)
	}
	if _, err := r.ReadByte(); err != io.EOF {
		t.Fatalf("connection must be closed after a protocol error, got %v", err)
	}
	assertAlive(t, addr)
}

func TestUnauthenticatedLimits(t *testing.T) {
	addr, _ := startTestServer(t, WithAuth("secret"))

	conn, r := dial(t, addr)
	if resp := send(t, conn, r, "*1000000\r\n"); !strings.Contains(resp, "unauthenticated multibulk length") {
		t.Fatalf("got %q", resp)
	}
	conn, r = dial(t, addr)
	if resp := send(t, conn, r, "*1\r\n$100000\r\n"); !strings.Contains(resp, "unauthenticated bulk length") {
		t.Fatalf("got %q", resp)
	}
	conn, r = dial(t, addr)
	if resp := send(t, conn, r, strings.Repeat("A", 100*1024)+"\r\n"); !strings.Contains(resp, "too big inline request") {
		t.Fatalf("got %q", resp)
	}
}

// Заявленная длина не выделяется заранее: память растёт по мере прихода данных.
func TestHugeBulkClaimDoesNotAllocate(t *testing.T) {
	addr, _ := startTestServer(t)
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	for i := 0; i < 5; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$500000000\r\nabc"))
		defer conn.Close()
	}
	time.Sleep(200 * time.Millisecond)

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 64<<20 {
		t.Fatalf("5 connections claiming 500MB each allocated %d MB", grown>>20)
	}
	assertAlive(t, addr)
}

func TestAuthFlow(t *testing.T) {
	addr, _ := startTestServer(t, WithAuth("secret"))
	conn, r := dial(t, addr)

	if resp := send(t, conn, r, "GET k\r\n"); !strings.HasPrefix(resp, "-NOAUTH") {
		t.Fatalf("before AUTH: %q", resp)
	}
	if resp := send(t, conn, r, "AUTH wrong\r\n"); !strings.HasPrefix(resp, "-WRONGPASS") {
		t.Fatalf("wrong password: %q", resp)
	}
	if resp := send(t, conn, r, "AUTH default secret\r\n"); resp != "OK" {
		t.Fatalf("AUTH default secret: %q", resp)
	}
	if resp := send(t, conn, r, "SET k v\r\n"); resp != "OK" {
		t.Fatalf("after AUTH: %q", resp)
	}
}

func TestProtectedMode(t *testing.T) {
	s := New(":0", storage.New(nil))
	remote := &net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 5000}
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5000}
	if !s.deniedByProtectedMode(remote) || s.deniedByProtectedMode(local) {
		t.Fatal("protected mode must deny remote and allow loopback clients without a password")
	}
	WithAuth("x")(s)
	if s.deniedByProtectedMode(remote) {
		t.Fatal("with a password remote clients are allowed")
	}
	s2 := New(":0", storage.New(nil), WithProtectedMode(false))
	if s2.deniedByProtectedMode(remote) {
		t.Fatal("protected mode disabled")
	}
}

func TestMaxClients(t *testing.T) {
	addr, _ := startTestServer(t, WithMaxClients(2))
	c1, r1 := dial(t, addr)
	send(t, c1, r1, "PING\r\n")
	c2, r2 := dial(t, addr)
	send(t, c2, r2, "PING\r\n")

	c3, r3 := dial(t, addr)
	line, _ := r3.ReadString('\n')
	if !strings.Contains(line, "max number of clients") {
		t.Fatalf("third client: %q", line)
	}
	c3.Close()

	c1.Close()
	time.Sleep(50 * time.Millisecond)
	assertAlive(t, addr)
}

// Shutdown отвечает на уже отправленные команды и закрывает простаивающих.
func TestGracefulShutdown(t *testing.T) {
	c := storage.New(nil)
	srv := New("127.0.0.1:0", c)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()

	idle, _ := dial(t, ln.Addr().String())
	busy, br := dial(t, ln.Addr().String())
	send(t, idle, bufio.NewReader(idle), "PING\r\n")

	busy.Write([]byte(strings.Repeat("INCR n\r\n", 1000)))
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("shutdown took %v", d)
	}
	if err := <-served; !errors.Is(err, ErrServerClosed) {
		t.Fatalf("Serve returned %v", err)
	}
	for i := 1; i <= 1000; i++ {
		resp, err := readRESPReply(br)
		if err != nil {
			t.Fatalf("reply %d lost on shutdown: %v", i, err)
		}
		if i == 1000 && resp != "1000" {
			t.Fatalf("last reply %q", resp)
		}
	}
}

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		ok   bool
	}{
		{`SET k v`, []string{"SET", "k", "v"}, true},
		{`  SET   k  v  `, []string{"SET", "k", "v"}, true},
		{`SET k "hello world"`, []string{"SET", "k", "hello world"}, true},
		{`SET k "a\nb\x41"`, []string{"SET", "k", "a\nbA"}, true},
		{`SET k 'it\'s'`, []string{"SET", "k", "it's"}, true},
		{`SET k ""`, []string{"SET", "k", ""}, true},
		{`SET k "unterminated`, nil, false},
		{`SET k "a"b`, nil, false},
	}
	for _, tc := range cases {
		got, ok := splitArgs([]byte(tc.in))
		if ok != tc.ok || strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("splitArgs(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
