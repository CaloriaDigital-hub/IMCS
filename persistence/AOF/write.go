package AOF

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type WriteInput struct {
	Cmd string
	Key string
	Value string
	TTL time.Duration;
}

func NewAOF(dir string) (*AOF, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	filename := filepath.Join(dir, "journal.aof")
	
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	return &AOF{file: f}, nil
}

func (a *AOF) Close() error {
	
	return a.file.Close()
}

func (a *AOF) Write(input WriteInput) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	var expire int64

	if input.TTL > 0 {
		expire = time.Now().Add(input.TTL).UnixNano()

	}

	entry := fmt.Sprintf("%s|%s|%d|%s\n", input.Cmd, input.Key, expire, input.Value)
	
	_, err := a.file.WriteString(entry)

	return err

	
}