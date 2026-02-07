package cache

import (
	"encoding/gob"
	"log"
	"fmt"
	"os"

)

func (c *Cache) SaveToFile(filename string) error {
	dir := "../cache-files/"
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("Не удалось создать директорию: %v", err)
	}

	file, err := os.Create(dir + filename)
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