package storage

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingPersistence запоминает журнал и умеет «ломаться».
type recordingPersistence struct {
	mu      sync.Mutex
	records []string
	last    map[string]string
	failErr error
}

func newRecording() *recordingPersistence {
	return &recordingPersistence{last: map[string]string{}}
}

func (p *recordingPersistence) Write(cmd, key, value string, expireAt int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = append(p.records, cmd+" "+key)
	if cmd == logSet {
		p.last[key] = value
	}
	return nil
}

func (p *recordingPersistence) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failErr
}

func realCount(c *Cache) int {
	n := 0
	for _, s := range c.shards {
		s.RLock()
		n += len(s.items)
		s.RUnlock()
	}
	return n
}

func TestSetNXIsAtomic(t *testing.T) {
	c := New(nil)
	for round := 0; round < 2000; round++ {
		key := "lock:" + strconv.Itoa(round)
		var wins atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if ok, _ := c.Set(key, "owner", SetOptions{NX: true, TTL: time.Minute}); ok {
					wins.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("round %d: %d owners acquired the lock", round, wins.Load())
		}
	}
}

func TestSetXX(t *testing.T) {
	c := New(nil)
	if ok, _ := c.Set("k", "v", SetOptions{XX: true}); ok {
		t.Fatal("XX on missing key must not set")
	}
	c.Set("k", "v1", SetOptions{})
	if ok, _ := c.Set("k", "v2", SetOptions{XX: true}); !ok {
		t.Fatal("XX on existing key must set")
	}
	if v, _ := c.Get("k"); v != "v2" {
		t.Fatalf("got %q", v)
	}
}

func TestKeepTTL(t *testing.T) {
	c := New(nil)
	c.Set("k", "v1", SetOptions{TTL: time.Hour})
	c.Set("k", "v2", SetOptions{KeepTTL: true})
	if ttl := c.GetTTL("k"); ttl < 3590 {
		t.Fatalf("KEEPTTL lost TTL: %d", ttl)
	}
	c.Set("k", "v3", SetOptions{})
	if ttl := c.GetTTL("k"); ttl != -1 {
		t.Fatalf("plain SET must clear TTL, got %d", ttl)
	}
}

func TestKeyCountStaysExact(t *testing.T) {
	c := New(nil)
	for i := 0; i < 1000; i++ {
		c.Set("t"+strconv.Itoa(i), "1", SetOptions{TTL: 20 * time.Millisecond})
	}
	time.Sleep(40 * time.Millisecond)
	for i := 0; i < 1000; i++ {
		k := "t" + strconv.Itoa(i)
		c.Get(k)
		switch i % 3 {
		case 0:
			c.IncrBy(k, 1)
		case 1:
			c.Append(k, "x")
		}
	}
	c.Rename("missing", "x")
	if got, want := c.CountKeys(), int64(realCount(c)); got != want {
		t.Fatalf("CountKeys=%d, real=%d", got, want)
	}
	c.ExpireByTTL(time.Second)
	if got, want := c.CountKeys(), int64(realCount(c)); got != want {
		t.Fatalf("after expire: CountKeys=%d, real=%d", got, want)
	}
}

func TestMaxKeysNoPhantomEvictions(t *testing.T) {
	c := NewWithMaxKeys(nil, 100)
	for i := 0; i < 100; i++ {
		c.Set("t"+strconv.Itoa(i), "v", SetOptions{TTL: 10 * time.Millisecond})
	}
	time.Sleep(20 * time.Millisecond)
	c.ExpireByTTL(time.Second)
	for i := 0; i < 50; i++ {
		c.Set("p"+strconv.Itoa(i), "v", SetOptions{})
	}
	for i := 0; i < 50; i++ {
		if _, ok := c.Get("p" + strconv.Itoa(i)); !ok {
			t.Fatalf("p%d evicted although the cache is far below the limit", i)
		}
	}
}

func TestMaxKeysBoundAndEvictionLogged(t *testing.T) {
	p := newRecording()
	c := NewWithMaxKeys(p, 1000)
	for i := 0; i < 5000; i++ {
		c.Set("k"+strconv.Itoa(i), "v", SetOptions{})
	}
	if n := c.CountKeys(); n > 1000 {
		t.Fatalf("limit exceeded: %d keys", n)
	}
	dels := 0
	for _, r := range p.records {
		if r[:3] == logDel {
			dels++
		}
	}
	if dels != 4000 {
		t.Fatalf("evictions must be journaled as DEL: got %d, want 4000", dels)
	}
}

func TestRenameConcurrentWithSet(t *testing.T) {
	c := New(nil)
	c.Set("a", "v", SetOptions{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			c.Set("a", "value-"+strconv.Itoa(i), SetOptions{})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			c.Rename("a", "b")
			c.Rename("b", "a")
		}
	}()
	wg.Wait()
	if got := c.CountKeys(); got != int64(realCount(c)) || got < 1 || got > 2 {
		t.Fatalf("CountKeys=%d real=%d", got, realCount(c))
	}
}

func TestRenameKeepsTTLAndOverwrites(t *testing.T) {
	c := New(nil)
	c.Set("src", "v", SetOptions{TTL: time.Hour})
	c.Set("dst", "old", SetOptions{})
	if err := c.Rename("src", "dst"); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get("dst"); v != "v" {
		t.Fatalf("dst=%q", v)
	}
	if c.Exists("src") != 0 || c.GetTTL("dst") < 3590 || c.CountKeys() != 1 {
		t.Fatalf("bad state: src exists=%d ttl=%d count=%d", c.Exists("src"), c.GetTTL("dst"), c.CountKeys())
	}
	if err := c.Rename("nope", "x"); !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("want ErrNoSuchKey, got %v", err)
	}
}

// Порядок записей в журнале по ключу должен совпадать с порядком в памяти.
func TestJournalOrderMatchesMemory(t *testing.T) {
	p := newRecording()
	c := New(p)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				c.IncrBy("counter", 1)
			}
		}()
	}
	wg.Wait()
	if v, _ := c.Get("counter"); v != "8000" || p.last["counter"] != "8000" {
		t.Fatalf("memory=%s journal=%s", v, p.last["counter"])
	}
}

func TestIncrSemantics(t *testing.T) {
	c := New(nil)
	c.Set("n", "9223372036854775806", SetOptions{TTL: time.Hour})
	if v, err := c.IncrBy("n", 1); err != nil || v != 9223372036854775807 {
		t.Fatalf("got %d %v", v, err)
	}
	if _, err := c.IncrBy("n", 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want overflow, got %v", err)
	}
	if c.GetTTL("n") < 3590 {
		t.Fatal("INCR must keep TTL")
	}
	c.Set("s", "abc", SetOptions{})
	if _, err := c.IncrBy("s", 1); !errors.Is(err, ErrNotInteger) {
		t.Fatalf("want ErrNotInteger, got %v", err)
	}
}

func TestExpireSemantics(t *testing.T) {
	c := New(nil)
	c.Set("k", "v", SetOptions{})
	if ok, _ := c.Expire("k", -time.Second); !ok || c.Exists("k") != 0 {
		t.Fatal("non-positive EXPIRE must delete the key")
	}
	c.Set("k", "v", SetOptions{TTL: time.Hour})
	if ok, _ := c.Persist("k"); !ok || c.GetTTL("k") != -1 {
		t.Fatal("PERSIST failed")
	}
	if ok, _ := c.Persist("k"); ok {
		t.Fatal("PERSIST on key without TTL must return false")
	}
}

func TestPersistenceFailureBlocksWrites(t *testing.T) {
	p := newRecording()
	c := New(p)
	c.Set("k", "v1", SetOptions{})
	p.failErr = errors.New("disk full")

	if _, err := c.Set("k", "v2", SetOptions{}); !errors.Is(err, ErrPersistence) {
		t.Fatalf("want ErrPersistence, got %v", err)
	}
	if v, _ := c.Get("k"); v != "v1" {
		t.Fatalf("memory must stay unchanged when the journal is down, got %q", v)
	}
	if _, err := c.Delete("k"); !errors.Is(err, ErrPersistence) {
		t.Fatal("DEL must fail too")
	}
	if err := c.FlushAll(); !errors.Is(err, ErrPersistence) {
		t.Fatal("FLUSHALL must fail too")
	}
}

func TestApplyReplay(t *testing.T) {
	c := New(nil)
	now := time.Now().UnixNano()
	c.Apply(logSet, "k", "old", 0)
	c.Apply(logSet, "k", "new", now-1) // истёкший SET перекрывает старое значение
	if c.Exists("k") != 0 {
		t.Fatal("expired SET in journal must delete the older value")
	}
	c.Apply(logSet, "a", "1", 0)
	c.Apply(logExpireAt, "a", "", now+int64(time.Hour))
	if c.GetTTL("a") < 3590 {
		t.Fatal("EXPIREAT not applied")
	}
	c.Apply(logExpireAt, "a", "", 0)
	if c.GetTTL("a") != -1 {
		t.Fatal("EXPIREAT 0 must persist")
	}
	c.Apply(logFlushAll, "", "", 0)
	if c.CountKeys() != 0 {
		t.Fatal("FLUSHALL not applied")
	}
}

func TestExpireByTTLKeepsUp(t *testing.T) {
	c := New(nil)
	for i := 0; i < 200_000; i++ {
		c.Set("e"+strconv.Itoa(i), "v", SetOptions{TTL: time.Millisecond})
	}
	time.Sleep(5 * time.Millisecond)
	if n := c.ExpireByTTL(10 * time.Second); n != 200_000 {
		t.Fatalf("expired %d of 200000 in one pass", n)
	}
	if c.CountKeys() != 0 {
		t.Fatalf("%d keys left", c.CountKeys())
	}
}

func TestSnapshotSkipsExpired(t *testing.T) {
	c := New(nil)
	c.Set("live", "1", SetOptions{TTL: time.Hour})
	c.Set("dead", "1", SetOptions{TTL: time.Millisecond})
	time.Sleep(3 * time.Millisecond)
	var got []string
	c.Snapshot(func(cmd, key, value string, expireAt int64) { got = append(got, key) })
	if len(got) != 1 || got[0] != "live" {
		t.Fatalf("snapshot=%v", got)
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, str string
		want         bool
	}{
		{"*", "", true},
		{"*", "a/b", true},
		{"user:*", "user:a/b", true},
		{"user:*", "users", false},
		{"h?llo", "hello", true},
		{"h?llo", "hllo", false},
		{"h[ae]llo", "hallo", true},
		{"h[ae]llo", "hillo", false},
		{"h[^e]llo", "hallo", true},
		{"h[^e]llo", "hello", false},
		{"h[a-b]llo", "hbllo", true},
		{"h[a-b]llo", "hcllo", false},
		{`h\*llo`, "h*llo", true},
		{`h\*llo`, "hello", false},
		{"*a*b*c", "xxaxxbxxc", true},
		{"*a*b*c", "xxaxxbxx", false},
		{"a*a*a*a*a*a*a*a*b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.str); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.str, got, tc.want)
		}
	}
}
