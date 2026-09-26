package AOF

// AOFPersister — адаптер AOF для интерфейса storage.Persistence.
type AOFPersister struct {
	aof *AOF
}

// NewPersister открывает журнал в dir.
func NewPersister(dir string) (*AOFPersister, error) {
	a, err := NewAOF(dir)
	if err != nil {
		return nil, err
	}
	return &AOFPersister{aof: a}, nil
}

// Write ставит запись в журнал. expireAt — абсолютное время (unix ns), 0 = без TTL.
func (p *AOFPersister) Write(cmd, key, value string, expireAt int64) error {
	return p.aof.Write(cmd, key, value, expireAt)
}

// Err возвращает ошибку, из-за которой журнал не принимает записи.
func (p *AOFPersister) Err() error { return p.aof.Err() }

// Read загружает журнал. См. AOF.Read.
func (p *AOFPersister) Read(apply func(cmd, key, value string, expireAt int64), repair bool) (*ReadResult, error) {
	return p.aof.Read(apply, repair)
}

// Rewrite компактит журнал. См. AOF.Rewrite.
func (p *AOFPersister) Rewrite(snapshot func(emit func(cmd, key, value string, expireAt int64))) error {
	return p.aof.Rewrite(snapshot)
}

// RewriteRunning сообщает, идёт ли rewrite.
func (p *AOFPersister) RewriteRunning() bool { return p.aof.RewriteRunning() }

// Size — текущий размер журнала.
func (p *AOFPersister) Size() int64 { return p.aof.Size() }

// LastRewriteSize — размер после последнего rewrite (или при открытии).
func (p *AOFPersister) LastRewriteSize() int64 { return p.aof.LastRewriteSize() }

// Close сбрасывает очередь на диск и закрывает журнал.
func (p *AOFPersister) Close() error { return p.aof.Close() }
