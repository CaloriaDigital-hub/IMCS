package AOF

import (
	"strconv"
	"testing"
	"time"
)

func setupBenchAOF(b *testing.B) *AOF {
	b.Helper()
	a, err := NewAOF(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { a.Close() })
	return a
}

func BenchmarkAOFWrite(b *testing.B) {
	a := setupBenchAOF(b)
	exp := time.Now().Add(time.Minute).UnixNano()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Write(CmdSet, "bench-key", "bench-value", exp)
	}
}

func BenchmarkAOFWriteUniqueKeys(b *testing.B) {
	a := setupBenchAOF(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Write(CmdSet, "key-"+strconv.Itoa(i), "value-"+strconv.Itoa(i), 0)
	}
}

func BenchmarkAOFRead(b *testing.B) {
	dir := b.TempDir()
	a, _ := NewAOF(dir)
	for i := 0; i < 10000; i++ {
		a.Write(CmdSet, "key-"+strconv.Itoa(i), "value-"+strconv.Itoa(i), 0)
	}
	a.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, _ := NewAOF(dir)
		r.Read(func(cmd, key, value string, expire int64) {}, false)
		r.Close()
	}
}

func BenchmarkAOFWriteParallel(b *testing.B) {
	a := setupBenchAOF(b)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			a.Write(CmdSet, "key-"+strconv.Itoa(i), "value", 0)
			i++
		}
	})
}
