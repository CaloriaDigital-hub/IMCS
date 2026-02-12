
package AOF

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)


// Package AOF provides functionality for writing commands to an Append-Only File (AOF) for persistence.
// It includes methods for creating and managing AOF files, writing entries with optional TTL (time-to-live),
// and safely closing the file.
//
// WriteInput represents the input for an AOF write operation, including the command, key, value, and TTL.
//
// NewAOF initializes a new AOF file in the specified directory, creating it if necessary.
// It returns an AOF instance or an error if the file cannot be created.
//
// Close safely closes the underlying AOF file.
//
// Write appends a new entry to the AOF file, locking for thread safety. If TTL is specified, the expiration
// timestamp is recorded; otherwise, it is set to zero.


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
