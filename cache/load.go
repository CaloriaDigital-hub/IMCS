package cache

import (
	"encoding/gob"
	"os"
	"path/filepath"
)

func (c *Cache) LoadFromFile(filename string) error {
	path := filepath.Join(c.storageDir, filename)

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	defer file.Close()

	decoder :=  gob.NewDecoder(file)

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := decoder.Decode(&c.items); err != nil {
		return err
	}

	return decoder.Decode(&c.items)


}