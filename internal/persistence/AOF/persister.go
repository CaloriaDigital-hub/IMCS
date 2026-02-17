package AOF

import "time"

// AOFPersister — адаптер AOF для интерфейса storage.Persistence.
type AOFPersister struct {
	aof *AOF
}

// NewPersister создаёт адаптер для AOF.
func NewPersister(dir string) (*AOFPersister, error) {
	a, err := NewAOF(dir)
	if err != nil {
		return nil, err
	}
	return &AOFPersister{aof: a}, nil
}

// Write записывает команду через AOF.
func (p *AOFPersister) Write(cmd, key, value string, duration time.Duration) error {
	return p.aof.Write(WriteInput{
		Cmd:   cmd,
		Key:   key,
		Value: value,
		TTL:   duration,
	})
}

// Read делегирует чтение AOF с CRC64 проверкой и truncate recovery.
func (p *AOFPersister) Read(rf func(cmd, key, value string, expire int64)) (*ReadResult, error) {
	return p.aof.Read(rf)
}

// Close закрывает AOF.
func (p *AOFPersister) Close() error {
	return p.aof.Close()
}

// Rewrite выполняет компактность AOF — оставляет только последнее состояние.
func (p *AOFPersister) Rewrite(snapshot func(fn func(cmd, key, value string, expireAt int64))) error {
	return p.aof.Rewrite(snapshot)
}
