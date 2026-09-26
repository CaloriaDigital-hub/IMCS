package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"
)

// ErrServerReply — сервер вернул "-ERR ...". Соединение живое, вызывающая
// сторона может продолжать работу на том же коннекте.
// IO/parse ошибки возвращаются отдельно (без этого wrap'а) — на них коннект мёртв.
var ErrServerReply = errors.New("server reply error")

func startTestServer(t *testing.T, opts ...Option) (string, *storage.Cache) {
	t.Helper()
	cache := storage.New(nil)
	addr, _ := startServerWith(t, cache, opts...)
	return addr, cache
}

func startServerWith(t *testing.T, cache *storage.Cache, opts ...Option) (string, *Server) {
	t.Helper()

	srv := New("127.0.0.1:0", cache, opts...)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	return ln.Addr().String(), srv
}

// readRESPReply читает один RESP-ответ.
func readRESPReply(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")

	if len(line) == 0 {
		return "", nil
	}

	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return line, fmt.Errorf("%w: %s", ErrServerReply, line[1:])
	case ':':
		return line[1:], nil
	case '$':
		size, _ := strconv.Atoi(line[1:])
		if size == -1 {
			return "(nil)", nil
		}
		buf := make([]byte, size+2) // +2 для \r\n
		n := 0
		for n < len(buf) {
			nn, err := reader.Read(buf[n:])
			if err != nil {
				return "", err
			}
			n += nn
		}
		return string(buf[:size]), nil
	case '*':
		count, _ := strconv.Atoi(line[1:])
		if count <= 0 {
			return "[]", nil
		}
		parts := make([]string, count)
		for i := 0; i < count; i++ {
			val, err := readRESPReply(reader)
			if err != nil {
				return "", err
			}
			parts[i] = val
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	default:
		return line, nil
	}
}

// ====================================================================
// TEST 1: RESP Protocol — все 22+ команд
// ====================================================================

func TestRESPProtocol(t *testing.T) {
	addr, _ := startTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	tests := []struct {
		cmd      string
		expected string
	}{
		{"PING\r\n", "PONG"},
		{"PING hello\r\n", "hello"},
		{"ECHO world\r\n", "world"},
		{"SET mykey myval\r\n", "OK"},
		{"GET mykey\r\n", "myval"},
		{"SET ttlkey val EX 100\r\n", "OK"},
		{"GET ttlkey\r\n", "val"},
		{"SET nxkey val1 NX\r\n", "OK"},
		{"SET nxkey val2 NX\r\n", "(nil)"},
		{"GET nxkey\r\n", "val1"},
		{"SET xxmissing val XX\r\n", "(nil)"},
		{"SET mykey updated XX\r\n", "OK"},
		{"GET mykey\r\n", "updated"},
		{"SETNX setnxkey hello\r\n", "1"},
		{"SETNX setnxkey world\r\n", "0"},
		{"GET setnxkey\r\n", "hello"},
		{"SETEX sxkey 100 sxval\r\n", "OK"},
		{"GET sxkey\r\n", "sxval"},
		{"EXISTS mykey\r\n", "1"},
		{"EXISTS nosuchkey\r\n", "0"},
		{"SET delme yes\r\n", "OK"},
		{"DEL delme\r\n", "1"},
		{"GET delme\r\n", "(nil)"},
		{"SET counter 10\r\n", "OK"},
		{"INCR counter\r\n", "11"},
		{"DECR counter\r\n", "10"},
		{"INCRBY counter 5\r\n", "15"},
		{"DECRBY counter 3\r\n", "12"},
		{"INCR newcounter\r\n", "1"},
		{"SET greeting hello\r\n", "OK"},
		{"APPEND greeting _world\r\n", "11"},
		{"STRLEN greeting\r\n", "11"},
		{"EXPIRE mykey 300\r\n", "1"},
		{"EXPIRE nosuchkey 300\r\n", "0"},
		{"SET noexpkey val\r\n", "OK"},
		{"TTL noexpkey\r\n", "-1"},
		{"TTL nosuchkey\r\n", "-2"},
		{"SET persistme val EX 100\r\n", "OK"},
		{"PERSIST persistme\r\n", "1"},
		{"TTL persistme\r\n", "-1"},
		{"TYPE mykey\r\n", "string"},
		{"TYPE nosuchkey\r\n", "none"},
		{"SET renameold value\r\n", "OK"},
		{"RENAME renameold renamenew\r\n", "OK"},
		{"GET renamenew\r\n", "value"},
		{"GET renameold\r\n", "(nil)"},
		{"MSET mk1 mv1 mk2 mv2 mk3 mv3\r\n", "OK"},
		{"MGET mk1 mk2 mk3 mkX\r\n", "[mv1, mv2, mv3, (nil)]"},
		{"SELECT 0\r\n", "OK"},
		{"SELECT 1\r\n", "-ERR DB index is out of range"},
		{"TTL\r\n", "-ERR wrong number of arguments for 'ttl' command"},
		{"SET q \"hello world\"\r\n", "OK"},
		{"GET q\r\n", "hello world"},
	}

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    RESP PROTOCOL: FULL COMMAND TEST (22+)       ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")

	passed := 0
	for _, tt := range tests {
		conn.Write([]byte(tt.cmd))
		resp, err := readRESPReply(reader)
		if err != nil && !errors.Is(err, ErrServerReply) {
			t.Fatalf("cmd=%q err=%v", tt.cmd, err)
		}

		cmdName := strings.TrimRight(tt.cmd, "\r\n")
		if resp == tt.expected {
			fmt.Printf("║  ✅ %-35s → %s\n", cmdName, resp)
			passed++
		} else {
			fmt.Printf("║  ❌ %-35s → %q (want %q)\n", cmdName, resp, tt.expected)
			t.Errorf("cmd=%q got=%q want=%q", cmdName, resp, tt.expected)
		}
	}

	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Result: %d/%d passed                              ║\n", passed, len(tests))
	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// ====================================================================
// TEST 2: 1M ops стресс тест (200 клиентов × 5000 ops)
// ====================================================================

func TestTCPStress50K(t *testing.T) {
	addr, _ := startTestServer(t)

	const (
		clients      = 200 // ограничено лимитом одновременных TCP-соединений Windows (~200)
		opsPerClient = 5000
		keySpace     = 50000
		readPct      = 70
		writePct     = 20
	)

	var (
		totalSets   atomic.Int64
		totalGets   atomic.Int64
		totalDels   atomic.Int64
		totalHits   atomic.Int64
		totalMisses atomic.Int64
		totalErrs   atomic.Int64
	)

	var wg sync.WaitGroup
	wg.Add(clients)

	start := time.Now()

	for c := 0; c < clients; c++ {
		go func(clientID int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", addr)
			if err != nil {
				totalErrs.Add(1)
				return
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			rng := rand.New(rand.NewSource(int64(clientID)))

			for op := 0; op < opsPerClient; op++ {
				key := "k:" + strconv.Itoa(rng.Intn(keySpace))
				roll := rng.Intn(100)

				var cmd string
				switch {
				case roll < readPct:
					cmd = "GET " + key + "\r\n"
					totalGets.Add(1)
				case roll < readPct+writePct:
					cmd = "SET " + key + " val" + strconv.Itoa(op) + " EX 300\r\n"
					totalSets.Add(1)
				default:
					cmd = "DEL " + key + "\r\n"
					totalDels.Add(1)
				}

				_, err := conn.Write([]byte(cmd))
				if err != nil {
					totalErrs.Add(1)
					return
				}

				resp, err := readRESPReply(reader)
				if err != nil {
					totalErrs.Add(1)
					return
				}

				if roll < readPct {
					if resp == "(nil)" {
						totalMisses.Add(1)
					} else {
						totalHits.Add(1)
					}
				}
			}
		}(c)
	}

	wg.Wait()
	elapsed := time.Since(start)

	sets := totalSets.Load()
	gets := totalGets.Load()
	dels := totalDels.Load()
	errs := totalErrs.Load()
	hits := totalHits.Load()
	misses := totalMisses.Load()
	total := sets + gets + dels

	throughput := int64(float64(total) / elapsed.Seconds())
	avgLatency := elapsed / time.Duration(total)

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║     TCP STRESS TEST: RESP PROTOCOL                  ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  TCP connections:   %6d                            ║\n", clients)
	fmt.Printf("║  Ops/connection:    %6d                            ║\n", opsPerClient)
	fmt.Printf("║  Total ops:     %10d                            ║\n", total)
	fmt.Printf("║  Duration:      %10v                            ║\n", elapsed.Round(time.Millisecond))
	fmt.Printf("║  Errors:        %10d                            ║\n", errs)
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  THROUGHPUT:    %10d ops/sec                    ║\n", throughput)
	fmt.Printf("║  AVG LATENCY:   %10s/op                        ║\n", avgLatency.String())
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Println("║  WRITES (SET)                                       ║")
	fmt.Printf("║    Count:       %10d                            ║\n", sets)
	fmt.Printf("║    Throughput:  %10d ops/sec                    ║\n", int64(float64(sets)/elapsed.Seconds()))
	fmt.Println("║  READS (GET)                                        ║")
	fmt.Printf("║    Count:       %10d                            ║\n", gets)
	fmt.Printf("║    Hits:        %10d                            ║\n", hits)
	fmt.Printf("║    Misses:      %10d                            ║\n", misses)
	if gets > 0 {
		fmt.Printf("║    Hit rate:       %5.1f%%                           ║\n", float64(hits)/float64(gets)*100)
	}
	fmt.Printf("║    Throughput:  %10d ops/sec                    ║\n", int64(float64(gets)/elapsed.Seconds()))
	fmt.Println("║  DELETES (DEL)                                      ║")
	fmt.Printf("║    Count:       %10d                            ║\n", dels)
	fmt.Printf("║    Throughput:  %10d ops/sec                    ║\n", int64(float64(dels)/elapsed.Seconds()))
	fmt.Println("╚══════════════════════════════════════════════════════╝")

	if errs > 0 {
		t.Errorf("TCP errors: %d", errs)
	}

	if throughput > 1_000_000 {
		fmt.Println("\n🏆 > 1M ops/sec ЧЕРЕЗ СЕТЬ (RESP) — ПОБЕДА!")
	}
}

// ====================================================================
// TEST 3: HARD — Concurrent INCR (атомарность счётчика)
// ====================================================================

func TestHardAtomicIncr(t *testing.T) {
	addr, cache := startTestServer(t)

	const (
		goroutines  = 100
		increments  = 1000
		expectedSum = goroutines * increments
	)

	// Инициализируем счётчик
	conn0, _ := net.Dial("tcp", addr)
	conn0.Write([]byte("SET atomic_counter 0\r\n"))
	readRESPReply(bufio.NewReader(conn0))
	conn0.Close()

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var errs atomic.Int64

	start := time.Now()

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				errs.Add(1)
				return
			}
			defer conn.Close()
			reader := bufio.NewReader(conn)

			for i := 0; i < increments; i++ {
				conn.Write([]byte("INCR atomic_counter\r\n"))
				_, err := readRESPReply(reader)
				if err != nil {
					errs.Add(1)
					return
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Проверяем финальное значение
	conn1, _ := net.Dial("tcp", addr)
	conn1.Write([]byte("GET atomic_counter\r\n"))
	val, _ := readRESPReply(bufio.NewReader(conn1))
	conn1.Close()

	finalVal, _ := strconv.Atoi(val)

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    HARD TEST: ATOMIC INCR                       ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Goroutines:    %10d                          ║\n", goroutines)
	fmt.Printf("║  INCR/goroutine:%10d                          ║\n", increments)
	fmt.Printf("║  Total INCRs:   %10d                          ║\n", expectedSum)
	fmt.Printf("║  Final value:   %10d                          ║\n", finalVal)
	fmt.Printf("║  Duration:      %10v                          ║\n", elapsed.Round(time.Millisecond))
	fmt.Printf("║  Throughput:    %10d INCR/sec                 ║\n", int64(float64(expectedSum)/elapsed.Seconds()))
	fmt.Printf("║  Errors:        %10d                          ║\n", errs.Load())

	if finalVal == expectedSum {
		fmt.Println("║  ✅ ATOMICITY: PERFECT — no race conditions      ║")
	} else {
		fmt.Printf("║  ❌ ATOMICITY: BROKEN — lost %d increments       ║\n", expectedSum-finalVal)
		t.Errorf("INCR atomicity broken: expected %d, got %d", expectedSum, finalVal)
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
	_ = cache
}

// ====================================================================
// TEST 4: HARD — Pipeline burst (100 команд за один Write)
// ====================================================================

func TestHardPipelineBurst(t *testing.T) {
	addr, _ := startTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	const pipelineSize = 1000

	// Готовим pipeline: 1000 SET + 1000 GET
	var buf strings.Builder
	for i := 0; i < pipelineSize; i++ {
		buf.WriteString(fmt.Sprintf("SET pipe:%d val%d\r\n", i, i))
	}
	for i := 0; i < pipelineSize; i++ {
		buf.WriteString(fmt.Sprintf("GET pipe:%d\r\n", i))
	}

	start := time.Now()

	// Один большой write
	conn.Write([]byte(buf.String()))

	reader := bufio.NewReader(conn)

	// Читаем 1000 OK + 1000 значений
	setOK := 0
	for i := 0; i < pipelineSize; i++ {
		resp, err := readRESPReply(reader)
		if err != nil {
			t.Fatal(err)
		}
		if resp == "OK" {
			setOK++
		}
	}

	getOK := 0
	for i := 0; i < pipelineSize; i++ {
		resp, err := readRESPReply(reader)
		if err != nil {
			t.Fatal(err)
		}
		expected := fmt.Sprintf("val%d", i)
		if resp == expected {
			getOK++
		}
	}

	elapsed := time.Since(start)
	totalOps := pipelineSize * 2

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    HARD TEST: PIPELINE BURST                    ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Pipeline size: %10d cmds                   ║\n", totalOps)
	fmt.Printf("║  SET OK:        %10d/%d                     ║\n", setOK, pipelineSize)
	fmt.Printf("║  GET correct:   %10d/%d                     ║\n", getOK, pipelineSize)
	fmt.Printf("║  Duration:      %10v                          ║\n", elapsed.Round(time.Microsecond))
	fmt.Printf("║  Throughput:    %10d ops/sec                 ║\n", int64(float64(totalOps)/elapsed.Seconds()))

	if setOK == pipelineSize && getOK == pipelineSize {
		fmt.Println("║  ✅ PIPELINE: PERFECT                            ║")
	} else {
		fmt.Println("║  ❌ PIPELINE: FAILED                             ║")
		t.Errorf("Pipeline failed: SET=%d/%d GET=%d/%d", setOK, pipelineSize, getOK, pipelineSize)
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// ====================================================================
// TEST 5: HARD — TTL expiry correctness
// ====================================================================

func TestHardTTLExpiry(t *testing.T) {
	addr, _ := startTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// SET с TTL = 500ms
	conn.Write([]byte("SET ttl_test expire_me PX 500\r\n"))
	resp, _ := readRESPReply(reader)
	if resp != "OK" {
		t.Fatal("SET failed")
	}

	// Проверяем что ключ живой
	conn.Write([]byte("GET ttl_test\r\n"))
	resp, _ = readRESPReply(reader)
	if resp != "expire_me" {
		t.Fatalf("expected expire_me, got %q", resp)
	}

	// PTTL должен быть > 0
	conn.Write([]byte("PTTL ttl_test\r\n"))
	pttlStr, _ := readRESPReply(reader)
	pttl, _ := strconv.Atoi(pttlStr)

	// Через 200ms ключ должен быть ЖИВ — иначе TTL слишком ранний.
	// Без этой проверки сломанный TTL=50ms тоже "проходил" бы тест.
	time.Sleep(200 * time.Millisecond)
	conn.Write([]byte("GET ttl_test\r\n"))
	respMid, _ := readRESPReply(reader)
	if respMid != "expire_me" {
		t.Errorf("TTL expired too early: at 200ms expected %q, got %q", "expire_me", respMid)
	}

	// Ждём ещё 400ms (всего ~600ms с момента SET) — ключ должен быть мёртв
	time.Sleep(400 * time.Millisecond)
	conn.Write([]byte("GET ttl_test\r\n"))
	resp, _ = readRESPReply(reader)

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    HARD TEST: TTL EXPIRY                        ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  SET ttl_test PX 500                             ║\n")
	fmt.Printf("║  Before: PTTL = %dms                            ║\n", pttl)
	fmt.Printf("║  At 200ms: GET = %-10q                       ║\n", respMid)
	fmt.Printf("║  After 600ms sleep:                              ║\n")

	if resp == "(nil)" {
		fmt.Println("║  ✅ GET = (nil) — ключ корректно истёк            ║")
	} else {
		fmt.Printf("║  ❌ GET = %q — ключ НЕ истёк!                 ║\n", resp)
		t.Errorf("TTL expiry broken: key still alive after 600ms, got %q", resp)
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// ====================================================================
// TEST 6: HARD — Big values (1MB per key)
// ====================================================================

func TestHardBigValues(t *testing.T) {
	addr, _ := startTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	// Генерируем 1MB значение
	bigVal := strings.Repeat("x", 1024*1024)

	const numKeys = 10

	start := time.Now()

	// SET 10 ключей по 1MB через multibulk (для бинарной безопасности)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("big:%d", i)
		// multibulk: *3\r\n$3\r\nSET\r\n$keylen\r\nkey\r\n$valuelen\r\nvalue\r\n
		cmd := fmt.Sprintf("*3\r\n$3\r\nSET\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n",
			len(key), key, len(bigVal), bigVal)
		conn.Write([]byte(cmd))
		resp, _ := readRESPReply(reader)
		if resp != "OK" {
			t.Fatalf("SET big:%d failed: %q", i, resp)
		}
	}

	setElapsed := time.Since(start)

	// GET и проверяем
	start = time.Now()
	verified := 0
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("big:%d", i)
		cmd := fmt.Sprintf("*2\r\n$3\r\nGET\r\n$%d\r\n%s\r\n", len(key), key)
		conn.Write([]byte(cmd))
		resp, err := readRESPReply(reader)
		if err != nil {
			t.Fatalf("GET big:%d read error: %v", i, err)
		}
		if resp == bigVal {
			verified++
		}
	}

	getElapsed := time.Since(start)

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    HARD TEST: BIG VALUES (1MB/key)              ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Keys:          %10d                          ║\n", numKeys)
	fmt.Printf("║  Value size:    %10s                          ║\n", "1MB")
	fmt.Printf("║  Total data:    %10s                          ║\n", fmt.Sprintf("%dMB", numKeys))
	fmt.Printf("║  SET duration:  %10v                          ║\n", setElapsed.Round(time.Millisecond))
	fmt.Printf("║  GET duration:  %10v                          ║\n", getElapsed.Round(time.Millisecond))
	fmt.Printf("║  SET throughput:%10s/sec                     ║\n", formatMB(float64(numKeys)*1.0/setElapsed.Seconds()))
	fmt.Printf("║  GET throughput:%10s/sec                     ║\n", formatMB(float64(numKeys)*1.0/getElapsed.Seconds()))
	fmt.Printf("║  Verified:      %10d/%d                       ║\n", verified, numKeys)

	if verified == numKeys {
		fmt.Println("║  ✅ BIG VALUES: PERFECT                          ║")
	} else {
		fmt.Println("║  ❌ BIG VALUES: DATA CORRUPTION                  ║")
		t.Errorf("Big values corrupted: %d/%d verified", verified, numKeys)
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}

func formatMB(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fGB", mb/1024)
	}
	return fmt.Sprintf("%.0fMB", mb)
}

// ====================================================================
// TEST 7: HARD — Max connections (2000 simultaneous)
// ====================================================================

func TestHardMaxConnections(t *testing.T) {
	addr, _ := startTestServer(t)

	const maxConns = 2000

	conns := make([]net.Conn, 0, maxConns)
	var connected atomic.Int64
	var failed atomic.Int64

	start := time.Now()

	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(maxConns)

	for i := 0; i < maxConns; i++ {
		go func(id int) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err != nil {
				failed.Add(1)
				return
			}

			// Считаем "подключён" только если сервер реально ответил на PING.
			// До этого мы знаем только что TCP handshake прошёл.
			conn.Write([]byte("PING\r\n"))
			reader := bufio.NewReader(conn)
			resp, err := readRESPReply(reader)
			if err != nil || resp != "PONG" {
				failed.Add(1)
				conn.Close()
				return
			}
			connected.Add(1)

			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	connectTime := time.Since(start)

	// Все соединения делают операции
	start = time.Now()
	var opsWg sync.WaitGroup
	var totalOps atomic.Int64

	for i, conn := range conns {
		opsWg.Add(1)
		go func(id int, c net.Conn) {
			defer opsWg.Done()
			reader := bufio.NewReader(c)
			key := fmt.Sprintf("conn:%d", id)

			c.Write([]byte(fmt.Sprintf("SET %s alive\r\n", key)))
			if _, err := readRESPReply(reader); err != nil {
				return
			}
			totalOps.Add(1)

			c.Write([]byte(fmt.Sprintf("GET %s\r\n", key)))
			resp, err := readRESPReply(reader)
			if err != nil || resp != "alive" {
				return
			}
			totalOps.Add(1)
		}(i, conn)
	}

	opsWg.Wait()
	opsTime := time.Since(start)

	// Cleanup
	for _, conn := range conns {
		conn.Close()
	}

	ok := connected.Load()
	fail := failed.Load()

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    HARD TEST: MAX CONNECTIONS                   ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Target:        %10d connections             ║\n", maxConns)
	fmt.Printf("║  Connected:     %10d                          ║\n", ok)
	fmt.Printf("║  Failed:        %10d                          ║\n", fail)
	fmt.Printf("║  Connect time:  %10v                          ║\n", connectTime.Round(time.Millisecond))
	fmt.Printf("║  Ops performed: %10d                          ║\n", totalOps.Load())
	fmt.Printf("║  Ops time:      %10v                          ║\n", opsTime.Round(time.Millisecond))

	if ok >= int64(maxConns*95/100) {
		fmt.Println("║  ✅ CONNECTIONS: PASS (≥95%)                     ║")
	} else {
		fmt.Printf("║  ⚠️  CONNECTIONS: %.0f%% success                   ║\n", float64(ok)/float64(maxConns)*100)
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// ====================================================================
// TEST 8: HARD — Mixed workload chaos (all commands)
// ====================================================================

func TestHardMixedChaos(t *testing.T) {
	addr, _ := startTestServer(t)

	const (
		clients = 200
		ops     = 2000
	)

	commands := []string{
		"SET", "GET", "DEL", "EXISTS",
		"APPEND", "STRLEN", "SETNX", "EXPIRE", "TTL",
		"MSET", "MGET", "TYPE", "DBSIZE", "PING",
		"INCR", // отдельный namespace, не пересекается со строковыми
	}

	var (
		totalOps    atomic.Int64
		totalSrvErr atomic.Int64 // -ERR от сервера (логические — type mismatch и т.п.)
		totalIOErr  atomic.Int64 // I/O / parse — соединение мёртвое
	)
	cmdCounts := make([]atomic.Int64, len(commands))

	var wg sync.WaitGroup
	wg.Add(clients)

	start := time.Now()

	for c := 0; c < clients; c++ {
		go func(clientID int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", addr)
			if err != nil {
				totalIOErr.Add(1)
				return
			}
			defer conn.Close()

			reader := bufio.NewReader(conn)
			rng := rand.New(rand.NewSource(int64(clientID)))

			for op := 0; op < ops; op++ {
				cmdIdx := rng.Intn(len(commands))
				// Строки и числа в разных namespace'ах:
				// без этого INCR попадал бы по str-ключам и сервер легитимно
				// возвращал бы -ERR (не число) — счётчик ошибок раздувался бы
				// от ожидаемого поведения, а не от настоящих багов.
				strKey := fmt.Sprintf("str:%d", rng.Intn(1000))
				numKey := fmt.Sprintf("num:%d", rng.Intn(1000))

				var cmd string
				switch commands[cmdIdx] {
				case "SET":
					cmd = fmt.Sprintf("SET %s v%d\r\n", strKey, op)
				case "GET":
					cmd = fmt.Sprintf("GET %s\r\n", strKey)
				case "DEL":
					cmd = fmt.Sprintf("DEL %s\r\n", strKey)
				case "INCR":
					cmd = fmt.Sprintf("INCR %s\r\n", numKey)
				case "EXISTS":
					cmd = fmt.Sprintf("EXISTS %s\r\n", strKey)
				case "APPEND":
					cmd = fmt.Sprintf("APPEND %s x\r\n", strKey)
				case "STRLEN":
					cmd = fmt.Sprintf("STRLEN %s\r\n", strKey)
				case "SETNX":
					cmd = fmt.Sprintf("SETNX %s v%d\r\n", strKey, op)
				case "EXPIRE":
					cmd = fmt.Sprintf("EXPIRE %s 300\r\n", strKey)
				case "TTL":
					cmd = fmt.Sprintf("TTL %s\r\n", strKey)
				case "MSET":
					cmd = fmt.Sprintf("MSET %s v1 %s:b v2\r\n", strKey, strKey)
				case "MGET":
					cmd = fmt.Sprintf("MGET %s %s:b\r\n", strKey, strKey)
				case "TYPE":
					cmd = fmt.Sprintf("TYPE %s\r\n", strKey)
				case "DBSIZE":
					cmd = "DBSIZE\r\n"
				case "PING":
					cmd = "PING\r\n"
				}

				conn.Write([]byte(cmd))
				_, err := readRESPReply(reader)
				if err != nil {
					if errors.Is(err, ErrServerReply) {
						// Логическая ошибка: считаем и продолжаем — коннект жив.
						totalSrvErr.Add(1)
						cmdCounts[cmdIdx].Add(1)
						totalOps.Add(1)
						continue
					}
					// I/O — соединение мёртвое, эту горутину гасим.
					totalIOErr.Add(1)
					return
				}

				cmdCounts[cmdIdx].Add(1)
				totalOps.Add(1)
			}
		}(c)
	}

	wg.Wait()
	elapsed := time.Since(start)

	total := totalOps.Load()
	srvErrs := totalSrvErr.Load()
	ioErrs := totalIOErr.Load()

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    HARD TEST: MIXED CHAOS (all commands)        ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Clients:       %10d                          ║\n", clients)
	fmt.Printf("║  Total ops:     %10d                          ║\n", total)
	fmt.Printf("║  Duration:      %10v                          ║\n", elapsed.Round(time.Millisecond))
	fmt.Printf("║  Throughput:    %10d ops/sec                 ║\n", int64(float64(total)/elapsed.Seconds()))
	fmt.Printf("║  -ERR replies:  %10d                          ║\n", srvErrs)
	fmt.Printf("║  I/O errors:    %10d                          ║\n", ioErrs)
	fmt.Println("╠══════════════════════════════════════════════════╣")

	for i, name := range commands {
		cnt := cmdCounts[i].Load()
		fmt.Printf("║    %-12s  %10d                          ║\n", name, cnt)
	}

	fmt.Println("╠══════════════════════════════════════════════════╣")
	if srvErrs == 0 && ioErrs == 0 {
		fmt.Println("║  ✅ CHAOS: NO ERRORS — server is correct+stable  ║")
	} else {
		fmt.Printf("║  ❌ CHAOS: %d -ERR, %d I/O                      ║\n", srvErrs, ioErrs)
		// При корректно изолированных namespace'ах -ERR быть не должно.
		// Если есть — это реальный баг сервера (или регрессия в неймспейсах).
		t.Errorf("chaos: %d -ERR replies, %d I/O errors", srvErrs, ioErrs)
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}
