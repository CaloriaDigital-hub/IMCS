package cache

import (
	"encoding/gob"
	"os"
)

func (c *Cache) LoadFromFile(filename string) error {
	file, err := os.Open("../cache-files/" + filename)

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

	return nil


}