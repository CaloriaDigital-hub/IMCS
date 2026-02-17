package cold

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"sync"
)

// entry — одна запись на диске.
type entry struct {
	Key      string
	Value    string
	ExpireAt int64
}

// Store — файловое хранилище для холодных (старых) данных.
// Данные хранятся в gob-файле c in-memory индексом.
type Store struct {
	mu    sync.RWMutex
	dir   string
	index map[string]entry // ключ → значение (в памяти, но дешево — только index)
}

// New создаёт cold store в указанной директории.
func New(dir string) (*Store, error) {
	dir = filepath.Join(dir, "cold")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	s := &Store{
		dir:   dir,
		index: make(map[string]entry),
	}

	// Загружаем существующие данные
	if err := s.load(); err != nil {
		// Не фатально — просто начинаем с пустого
	}

	return s, nil
}

// Put сохраняет ключ в cold storage.
func (s *Store) Put(key, value string, expireAt int64) {
	s.mu.Lock()
	s.index[key] = entry{Key: key, Value: value, ExpireAt: expireAt}
	s.mu.Unlock()
}

// PutBatch сохраняет пачку ключей атомарно.
func (s *Store) PutBatch(items []Item) {
	s.mu.Lock()
	for _, it := range items {
		s.index[it.Key] = entry{Key: it.Key, Value: it.Value, ExpireAt: it.ExpireAt}
	}
	s.mu.Unlock()
}

// Get ищет ключ в cold storage.
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	e, ok := s.index[key]
	s.mu.RUnlock()

	if !ok {
		return "", false
	}

	return e.Value, true
}

// Delete удаляет ключ из cold storage.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	delete(s.index, key)
	s.mu.Unlock()
}

// Len возвращает количество ключей в cold storage.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.index)
}

// Item — элемент для batch операций.
type Item struct {
	Key      string
	Value    string
	ExpireAt int64
}

// Flush сбрасывает index на диск (gob).
func (s *Store) Flush() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.dir, "cold.gob")
	tmp := path + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if err := gob.NewEncoder(f).Encode(s.index); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()

	return os.Rename(tmp, path)
}

// load загружает index с диска.
func (s *Store) load() error {
	path := filepath.Join(s.dir, "cold.gob")

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return gob.NewDecoder(f).Decode(&s.index)
}

// FlushAll очищает весь cold store (RAM + disk).
func (s *Store) FlushAll() {
	s.mu.Lock()
	s.index = make(map[string]entry)
	s.mu.Unlock()

	path := filepath.Join(s.dir, "cold.gob")
	os.Remove(path)
}
