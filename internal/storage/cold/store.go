package cold

import (
	"encoding/gob"
	"os"
	"path/filepath"
)

/*

	Тут методы:
		Put,
		Get,
		PutBatch,
		Delete,
		Len,
		Load
		Flush,
		FlushAll

*/


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
