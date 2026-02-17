package AOF

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func setupBenchAOF(b *testing.B) (*AOF, func()) {
	b.Helper()

	dir, err := os.MkdirTemp("", "aof-bench-*")
	if err != nil {
		b.Fatal(err)
	}

	a, err := NewAOF(dir)
	if err != nil {
		os.RemoveAll(dir)
		b.Fatal(err)
	}

	return a, func() {
		a.Close()
		os.RemoveAll(dir)
	}
}

func BenchmarkAOFWrite(b *testing.B) {
	a, cleanup := setupBenchAOF(b)
	defer cleanup()

	input := WriteInput{
		Cmd:   "SET",
		Key:   "bench-key",
		Value: "bench-value",
		TTL:   time.Minute,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Write(input)
	}
}

func BenchmarkAOFWriteUniqueKeys(b *testing.B) {
	a, cleanup := setupBenchAOF(b)
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Write(WriteInput{
			Cmd:   "SET",
			Key:   "key-" + strconv.Itoa(i),
			Value: "value-" + strconv.Itoa(i),
			TTL:   time.Minute,
		})
	}
}

func BenchmarkAOFRead(b *testing.B) {
	a, cleanup := setupBenchAOF(b)
	defer cleanup()

	// Заполняем AOF
	for i := 0; i < 10000; i++ {
		a.Write(WriteInput{
			Cmd:   "SET",
			Key:   "key-" + strconv.Itoa(i),
			Value: "value-" + strconv.Itoa(i),
			TTL:   time.Minute,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Read(func(cmd, key, value string, expire int64) {
			// noop
		})
	}
}

func BenchmarkAOFWriteParallel(b *testing.B) {
	a, cleanup := setupBenchAOF(b)
	defer cleanup()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			a.Write(WriteInput{
				Cmd:   "SET",
				Key:   "key-" + strconv.Itoa(i),
				Value: "value",
				TTL:   time.Minute,
			})
			i++
		}
	})
}

func BenchmarkPersisterWrite(b *testing.B) {
	dir, err := os.MkdirTemp("", "persister-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)

	p, err := NewPersister(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Write("SET", "key", "value", time.Minute)
	}
}
