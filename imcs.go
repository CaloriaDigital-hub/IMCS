// Package imcs предоставляет встраиваемый in-memory кеш с AOF-персистенцией
// и опциональным Redis-совместимым (RESP2) TCP-сервером.
//
// Без сети (embedded):
//
//	db, err := imcs.Open("./data")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer db.Close()
//
//	err = db.Set("key", "value", time.Hour)
//	val, ok := db.Get("key")
//
// С TCP-сервером:
//
//	db, _ := imcs.OpenWithOptions("./data", imcs.Options{Password: "secret"})
//	defer db.Close()
//	go db.ListenAndServe(":6380")
package imcs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/CaloriaDigital-hub/IMCS/internal/persistence/AOF"
	"github.com/CaloriaDigital-hub/IMCS/internal/server"
	"github.com/CaloriaDigital-hub/IMCS/internal/storage/cache"
	"github.com/CaloriaDigital-hub/IMCS/internal/storage/janitor"
)

// Ошибки, которые возвращают операции записи.
var (
	// ErrPersistence — журнал не принимает записи (например, диск полон).
	// Изменение не выполнено; нужна диагностика и перезапуск.
	ErrPersistence = storage.ErrPersistence
	ErrNotInteger  = storage.ErrNotInteger
	ErrOverflow    = storage.ErrOverflow
	ErrNoSuchKey   = storage.ErrNoSuchKey
	// ErrCorrupt — журнал повреждён в середине. См. Options.RepairAOF.
	ErrCorrupt = AOF.ErrCorrupt
	// ErrClosed — DB уже закрыта.
	ErrClosed = errors.New("imcs: database is closed")
)

const (
	defaultAutoRewriteMinSize = 64 << 20
	shutdownTimeout           = 5 * time.Second
)

// autoRewriteCheckEvery — var, чтобы тесты не ждали по 5 секунд.
var autoRewriteCheckEvery = 5 * time.Second

// Options — настройки DB. Нулевое значение — разумные значения по умолчанию.
type Options struct {
	// MaxKeys — лимит ключей (0 = без лимита). При превышении вытесняются
	// давно не использованные ключи (sampled LRU), вытеснение пишется в журнал.
	MaxKeys int64

	// Password — пароль TCP-сервера (пусто = без AUTH).
	Password string

	// MaxClients — лимит одновременных TCP-соединений (0 = 10000).
	MaxClients int

	// IdleTimeout — закрывать TCP-соединения, простаивающие дольше (0 = никогда).
	IdleTimeout time.Duration

	// DisableProtectedMode — принимать соединения без пароля не только с
	// loopback. По умолчанию без пароля пускаются только локальные клиенты.
	DisableProtectedMode bool

	// RepairAOF — при повреждении в середине журнала обрезать его по месту
	// повреждения (всё, что после, теряется). По умолчанию Open возвращает ErrCorrupt.
	// Оборванная последняя запись (краш во время записи) отрезается всегда.
	RepairAOF bool

	// AutoRewriteMinSize — журнал переписывается (компактится), когда он вырос
	// вдвое с прошлого rewrite и не меньше этого размера. 0 = 64 МБ, < 0 = выключено.
	AutoRewriteMinSize int64
}

// DB — встраиваемый кеш. Создаётся через Open/OpenWithOptions.
type DB struct {
	cache   *storage.Cache
	aof     *AOF.AOFPersister
	janitor *janitor.Janitor
	opts    Options

	mu     sync.Mutex
	srv    *server.Server
	closed bool

	stopBg chan struct{}
	bgDone chan struct{}
}

// Open открывает DB в dir с настройками по умолчанию.
func Open(dir string) (*DB, error) {
	return OpenWithOptions(dir, Options{})
}

// OpenWithOptions открывает DB в dir и загружает журнал.
func OpenWithOptions(dir string, opts Options) (*DB, error) {
	aof, err := AOF.NewPersister(dir)
	if err != nil {
		return nil, err
	}

	c := storage.NewWithMaxKeys(aof, opts.MaxKeys)

	start := time.Now()
	res, err := aof.Read(c.Apply, opts.RepairAOF)
	if err != nil {
		_ = aof.Close()
		return nil, fmt.Errorf("imcs: load journal: %w", err)
	}
	log.Printf("AOF: loaded %d records (%d keys) in %v",
		res.ValidEntries, c.CountKeys(), time.Since(start).Round(time.Millisecond))

	db := &DB{
		cache:   c,
		aof:     aof,
		janitor: janitor.New(c),
		opts:    opts,
		stopBg:  make(chan struct{}),
		bgDone:  make(chan struct{}),
	}

	// Старый строковый формат не переносит '\n' и '|' — сразу переписываем
	// журнал в новый формат (заодно компактим).
	if res.LegacyEntries > 0 {
		log.Printf("AOF: %d records in legacy format, migrating journal", res.LegacyEntries)
		if err := db.Rewrite(); err != nil {
			_ = aof.Close()
			return nil, fmt.Errorf("imcs: migrate journal: %w", err)
		}
	}

	db.janitor.Start()
	go db.background()
	return db, nil
}

// background — автоматический rewrite журнала.
func (db *DB) background() {
	defer close(db.bgDone)

	minSize := db.opts.AutoRewriteMinSize
	if minSize < 0 {
		<-db.stopBg
		return
	}
	if minSize == 0 {
		minSize = defaultAutoRewriteMinSize
	}

	t := time.NewTicker(autoRewriteCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			size, base := db.aof.Size(), db.aof.LastRewriteSize()
			if size >= minSize && size >= 2*base {
				if err := db.Rewrite(); err != nil && !errors.Is(err, AOF.ErrRewriteInProgress) {
					log.Printf("AOF: auto rewrite failed: %v", err)
				}
			}
		case <-db.stopBg:
			return
		}
	}
}

// Rewrite компактит журнал: оставляет только текущее состояние.
func (db *DB) Rewrite() error {
	return db.aof.Rewrite(db.cache.Snapshot)
}

// ─── Строки ─────────────────────────────────────────────────────────

// Set записывает значение. ttl <= 0 — без TTL.
//
//	db.Set("session:abc", "token", 30*time.Minute)
func (db *DB) Set(key, value string, ttl time.Duration) error {
	_, err := db.cache.Set(key, value, storage.SetOptions{TTL: ttl})
	return err
}

// SetNX записывает значение, только если ключа нет. Атомарно.
//
//	ok, err := db.SetNX("lock:resource", "owner", 10*time.Second)
func (db *DB) SetNX(key, value string, ttl time.Duration) (bool, error) {
	return db.cache.Set(key, value, storage.SetOptions{TTL: ttl, NX: true})
}

// SetXX записывает значение, только если ключ есть. Атомарно.
func (db *DB) SetXX(key, value string, ttl time.Duration) (bool, error) {
	return db.cache.Set(key, value, storage.SetOptions{TTL: ttl, XX: true})
}

// Get возвращает значение по ключу.
func (db *DB) Get(key string) (string, bool) {
	return db.cache.Get(key)
}

// Del удаляет ключи. Возвращает число удалённых.
func (db *DB) Del(keys ...string) (int64, error) {
	return db.cache.Delete(keys...)
}

// Incr увеличивает число на 1.
func (db *DB) Incr(key string) (int64, error) { return db.cache.IncrBy(key, 1) }

// Decr уменьшает число на 1.
func (db *DB) Decr(key string) (int64, error) { return db.cache.IncrBy(key, -1) }

// IncrBy прибавляет delta. TTL ключа сохраняется.
func (db *DB) IncrBy(key string, delta int64) (int64, error) {
	return db.cache.IncrBy(key, delta)
}

// Append дописывает к значению. Возвращает новую длину.
func (db *DB) Append(key, value string) (int, error) { return db.cache.Append(key, value) }

// Strlen — длина значения.
func (db *DB) Strlen(key string) int { return db.cache.Strlen(key) }

// MSet атомарно записывает пары ключ-значение: MSet("k1", "v1", "k2", "v2").
func (db *DB) MSet(pairs ...string) error {
	if len(pairs)%2 != 0 {
		return errors.New("imcs: MSet needs key-value pairs")
	}
	return db.cache.MSet(pairs...)
}

// Value — результат MGet.
type Value = storage.GetResult

// MGet — пакетное чтение.
func (db *DB) MGet(keys ...string) []Value { return db.cache.MGet(keys...) }

// ─── Ключи ──────────────────────────────────────────────────────────

// Exists — число существующих ключей.
func (db *DB) Exists(keys ...string) int64 { return db.cache.Exists(keys...) }

// Expire ставит TTL. ttl <= 0 удаляет ключ. false — ключа нет.
func (db *DB) Expire(key string, ttl time.Duration) (bool, error) {
	return db.cache.Expire(key, ttl)
}

// Persist убирает TTL. false — ключа нет или TTL не было.
func (db *DB) Persist(key string) (bool, error) { return db.cache.Persist(key) }

// TTL — оставшееся время жизни в секундах. -1 = без TTL, -2 = ключа нет.
func (db *DB) TTL(key string) int64 { return db.cache.GetTTL(key) }

// Keys — ключи по glob-паттерну Redis (*, ?, [...]). O(N), не для горячего пути.
func (db *DB) Keys(pattern string) []string { return db.cache.Keys(pattern) }

// Rename атомарно переименовывает ключ. ErrNoSuchKey, если ключа нет.
func (db *DB) Rename(oldKey, newKey string) error { return db.cache.Rename(oldKey, newKey) }

// Len — число ключей.
func (db *DB) Len() int64 { return db.cache.CountKeys() }

// FlushAll удаляет все ключи.
func (db *DB) FlushAll() error { return db.cache.FlushAll() }

// ─── TCP ────────────────────────────────────────────────────────────

// ListenAndServe запускает RESP-сервер на addr с настройками из Options.
// Блокирует до Close (тогда возвращает nil) или ошибки.
func (db *DB) ListenAndServe(addr string) error {
	opts := []server.Option{
		server.WithMaxClients(db.opts.MaxClients),
		server.WithIdleTimeout(db.opts.IdleTimeout),
		server.WithProtectedMode(!db.opts.DisableProtectedMode),
		server.WithAOF(db.aof, db.Rewrite),
	}
	if db.opts.Password != "" {
		opts = append(opts, server.WithAuth(db.opts.Password))
	}
	srv := server.New(addr, db.cache, opts...)

	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return ErrClosed
	}
	if db.srv != nil {
		db.mu.Unlock()
		return errors.New("imcs: server is already running")
	}
	db.srv = srv
	db.mu.Unlock()

	if err := srv.Listen(); err != nil && !errors.Is(err, server.ErrServerClosed) {
		return err
	}
	return nil
}

// ─── Lifecycle ──────────────────────────────────────────────────────

// Close останавливает сервер (дожидаясь текущих команд), janitor и фоновые
// задачи, затем сбрасывает журнал на диск. Повторный вызов — no-op.
func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	srv := db.srv
	db.mu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		_ = srv.Shutdown(ctx) // по таймауту соединения закрываются принудительно
		cancel()
	}
	close(db.stopBg)
	<-db.bgDone
	db.janitor.Stop()
	return db.aof.Close()
}
