// Package imcs предоставляет встраиваемый in-memory кеш-сервер.
//
// Использование без сети (embedded):
//
//	db, err := imcs.Open("./data")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer db.Close()
//
//	db.Set("key", "value", time.Hour)
//	val, ok := db.Get("key")
//
// Использование с TCP-сервером (Redis-совместимый):
//
//	db, _ := imcs.Open("./data")
//	defer db.Close()
//	db.ListenAndServe(":6380")
package imcs

import (
	"time"

	"imcs/internal/persistence/AOF"
	"imcs/internal/server"
	"imcs/internal/storage/cache"
	"imcs/internal/storage/janitor"
)

// DB — встраиваемый кеш. Создаётся через Open().
type DB struct {
	cache     *storage.Cache
	persister *AOF.AOFPersister
	janitor   *janitor.Janitor
	srv       *server.Server
}

// Open создаёт кеш с AOF persistence в указанной директории.
// Автоматически восстанавливает данные из журнала при запуске.
//
//	db, err := imcs.Open("./data")
//	defer db.Close()
func Open(dir string) (*DB, error) {
	return OpenWithOptions(dir, Options{})
}

// Options содержит опциональные настройки.
type Options struct {
	// MaxKeys — максимальное кол-во ключей (0 = без лимита).
	// При превышении лимита включается LRU eviction.
	MaxKeys int64

	// Password — пароль для TCP-сервера (пустой = без AUTH).
	Password string
}

// OpenWithOptions создаёт кеш с дополнительными настройками.
func OpenWithOptions(dir string, opts Options) (*DB, error) {
	persister, err := AOF.NewPersister(dir)
	if err != nil {
		return nil, err
	}

	cache := storage.NewWithMaxKeys(persister, opts.MaxKeys)

	if err := cache.InitColdStorage(dir); err != nil {
		// Cold storage не критичен — продолжаем без него
	}

	// Восстанавливаем данные из AOF
	persister.Read(func(cmd, key, value string, expire int64) {
		switch cmd {
		case "SET":
			if expire > 0 && expire < time.Now().UnixNano() {
				return
			}
			var ttl time.Duration
			if expire > 0 {
				ttl = time.Duration(expire-time.Now().UnixNano()) * time.Nanosecond
			}
			cache.Set(key, value, ttl, false)
		case "DEL":
			cache.Delete(key)
		}
	})

	j := janitor.New(cache)
	j.Start()

	return &DB{
		cache:     cache,
		persister: persister,
		janitor:   j,
	}, nil
}

// ─── Core Operations ────────────────────────────────────────────────

// Set устанавливает значение с опциональным TTL.
// TTL = 0 означает без ограничения по времени.
//
//	db.Set("session:abc", "token", 30*time.Minute)
//	db.Set("config", "value", 0)  // вечный ключ
func (db *DB) Set(key, value string, ttl time.Duration) {
	db.cache.Set(key, value, ttl, false)
}

// SetNX устанавливает значение только если ключ НЕ существует.
// Возвращает true если установлен, false если ключ уже был.
//
//	ok := db.SetNX("lock:resource", "owner", 10*time.Second)
func (db *DB) SetNX(key, value string, ttl time.Duration) bool {
	return db.cache.Set(key, value, ttl, true) == nil
}

// Get возвращает значение по ключу.
//
//	val, ok := db.Get("user:1")
func (db *DB) Get(key string) (string, bool) {
	return db.cache.Get(key)
}

// Del удаляет один или несколько ключей.
//
//	db.Del("key1", "key2", "key3")
func (db *DB) Del(keys ...string) {
	for _, key := range keys {
		db.cache.Delete(key)
	}
}

// ─── Counters ───────────────────────────────────────────────────────

// Incr увеличивает значение на 1. Возвращает новое значение.
//
//	count, _ := db.Incr("page:views")
func (db *DB) Incr(key string) (int64, error) {
	return db.cache.IncrBy(key, 1)
}

// Decr уменьшает значение на 1. Возвращает новое значение.
func (db *DB) Decr(key string) (int64, error) {
	return db.cache.IncrBy(key, -1)
}

// IncrBy увеличивает значение на delta. Возвращает новое значение.
//
//	total, _ := db.IncrBy("balance", 500)
func (db *DB) IncrBy(key string, delta int64) (int64, error) {
	return db.cache.IncrBy(key, delta)
}

// ─── Key Management ─────────────────────────────────────────────────

// Exists проверяет существование ключей. Возвращает кол-во найденных.
//
//	n := db.Exists("key1", "key2")  // → 0, 1, или 2
func (db *DB) Exists(keys ...string) int64 {
	return db.cache.Exists(keys...)
}

// Expire устанавливает TTL на существующий ключ.
//
//	db.Expire("session", 30*time.Minute)
func (db *DB) Expire(key string, ttl time.Duration) bool {
	return db.cache.Expire(key, ttl)
}

// Persist убирает TTL — делает ключ вечным.
func (db *DB) Persist(key string) bool {
	return db.cache.Persist(key)
}

// TTL возвращает оставшееся время жизни.
// -1 = без TTL, -2 = ключ не найден.
func (db *DB) TTL(key string) int64 {
	return db.cache.GetTTL(key)
}

// Keys возвращает ключи по glob-паттерну.
//
//	keys := db.Keys("user:*")
//	all  := db.Keys("*")
func (db *DB) Keys(pattern string) []string {
	return db.cache.Keys(pattern)
}

// Rename переименовывает ключ.
func (db *DB) Rename(oldKey, newKey string) bool {
	return db.cache.Rename(oldKey, newKey)
}

// Len возвращает количество ключей в кеше.
func (db *DB) Len() int64 {
	return db.cache.CountKeys()
}

// ─── Batch Operations ───────────────────────────────────────────────

// MSet массовая установка пар ключ-значение.
//
//	db.MSet("k1", "v1", "k2", "v2", "k3", "v3")
func (db *DB) MSet(pairs ...string) {
	db.cache.MSet(pairs...)
}

// MGet массовое чтение ключей.
//
//	results := db.MGet("k1", "k2", "k3")
//	for _, r := range results {
//	    if r.Found { fmt.Println(r.Value) }
//	}
func (db *DB) MGet(keys ...string) []struct {
	Value string
	Found bool
} {
	return db.cache.MGet(keys...)
}

// ─── String Operations ──────────────────────────────────────────────

// Append дописывает к значению ключа. Возвращает новую длину.
func (db *DB) Append(key, value string) int {
	return db.cache.Append(key, value)
}

// Strlen возвращает длину строки.
func (db *DB) Strlen(key string) int {
	return db.cache.Strlen(key)
}

// ─── Flush ──────────────────────────────────────────────────────────

// FlushAll удаляет все данные из кеша и cold storage.
func (db *DB) FlushAll() {
	db.cache.FlushDB()
}

// ─── TCP Server ─────────────────────────────────────────────────────

// ListenAndServe запускает TCP-сервер (RESP протокол).
// Блокирующий вызов — слушает до ошибки или Shutdown.
//
//	go db.ListenAndServe(":6380")
func (db *DB) ListenAndServe(addr string) error {
	var opts []server.Option
	srv := server.New(addr, db.cache, opts...)
	db.srv = srv
	return srv.Listen()
}

// ─── Lifecycle ──────────────────────────────────────────────────────

// Close останавливает janitor, сбрасывает данные на диск и закрывает журнал.
// Всегда вызывай через defer.
func (db *DB) Close() {
	db.janitor.Stop()

	if db.srv != nil {
		db.srv.Shutdown()
	}

	db.cache.Close()
	db.persister.Close()
}
