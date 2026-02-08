package cache

import (
	"encoding/gob"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func (c *Cache) SaveToFile(filename string) error {
	path := filepath.Join(c.storageDir, filename)
	

	file, err := os.Create(path)
	if err != nil {
		log.Printf("Ошибка при создании файла: %v", err)
		return err
	}

	defer file.Close()

	encoder := gob.NewEncoder(file)
	
	c.mu.RLock()
	defer c.mu.RUnlock()

	if err := encoder.Encode(c.items); err != nil {
		fmt.Println(err)
		return err
	}

	return nil

	
}