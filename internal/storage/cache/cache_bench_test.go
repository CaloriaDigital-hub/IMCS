package storage

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"
	"time"
)

// mockPersistence — мок для бенчмарков, не пишет на диск.
type mockPersistence struct{}

func (m *mockPersistence) Write(cmd, key, value string, expireAt int64) error { return nil }
func (m *mockPersistence) Err() error                                         { return nil }

func newTestCache() *Cache {
	return New(&mockPersistence{})
}

// --- Базовые операции ---

func BenchmarkSet(b *testing.B) {
	c := newTestCache()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set("key"+strconv.Itoa(i), "value", SetOptions{})
	}
}

func BenchmarkSetWithTTL(b *testing.B) {
	c := newTestCache()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set("key"+strconv.Itoa(i), "value", SetOptions{TTL: 5 * time.Minute})
	}
}

func BenchmarkSetNX(b *testing.B) {
	c := newTestCache()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set("key"+strconv.Itoa(i), "value", SetOptions{NX: true})
	}
}

func BenchmarkGet(b *testing.B) {
	c := newTestCache()

	// Предзаполнение
	for i := 0; i < 10000; i++ {
		c.Set("key"+strconv.Itoa(i), "value"+strconv.Itoa(i), SetOptions{})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get("key" + strconv.Itoa(i%10000))
	}
}

func BenchmarkGetMiss(b *testing.B) {
	c := newTestCache()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get("nonexistent" + strconv.Itoa(i))
	}
}

func BenchmarkDelete(b *testing.B) {
	c := newTestCache()

	for i := 0; i < b.N; i++ {
		c.Set("key"+strconv.Itoa(i), "value", SetOptions{})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Delete("key" + strconv.Itoa(i))
	}
}

// --- Параллельные операции ---

func BenchmarkSetParallel(b *testing.B) {
	c := newTestCache()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Set("key"+strconv.Itoa(i), "value", SetOptions{})
			i++
		}
	})
}

func BenchmarkGetParallel(b *testing.B) {
	c := newTestCache()

	for i := 0; i < 10000; i++ {
		c.Set("key"+strconv.Itoa(i), "value", SetOptions{})
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Get("key" + strconv.Itoa(i%10000))
			i++
		}
	})
}

func BenchmarkMixedReadWrite(b *testing.B) {
	c := newTestCache()

	for i := 0; i < 10000; i++ {
		c.Set("key"+strconv.Itoa(i), "value", SetOptions{})
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "key" + strconv.Itoa(i%10000)
			if i%10 < 8 {
				c.Get(key) // 80% чтение
			} else {
				c.Set(key, "newval", SetOptions{}) // 20% запись
			}
			i++
		}
	})
}

// --- Масштабируемость шардов ---

func BenchmarkSetScaling(b *testing.B) {
	for _, goroutines := range []int{1, 4, 8, 16, 32, 64} {
		b.Run(fmt.Sprintf("goroutines-%d", goroutines), func(b *testing.B) {
			c := newTestCache()

			var wg sync.WaitGroup
			opsPerGoroutine := b.N / goroutines
			if opsPerGoroutine == 0 {
				opsPerGoroutine = 1
			}

			b.ResetTimer()
			for g := 0; g < goroutines; g++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					base := id * opsPerGoroutine
					for i := 0; i < opsPerGoroutine; i++ {
						c.Set("key"+strconv.Itoa(base+i), "value", SetOptions{})
					}
				}(g)
			}
			wg.Wait()
		})
	}
}

// --- Размер значений ---

func BenchmarkSetValueSizes(b *testing.B) {
	sizes := []int{10, 100, 1024, 10240}

	for _, size := range sizes {
		value := string(make([]byte, size))
		for i := range []byte(value) {
			value = value[:i] + "x" + value[i+1:]
		}

		b.Run(fmt.Sprintf("value-%dB", size), func(b *testing.B) {
			c := newTestCache()
			val := randString(size)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Set("key"+strconv.Itoa(i), val, SetOptions{})
			}
		})
	}
}

// --- Количество ключей ---

func BenchmarkGetWithLoad(b *testing.B) {
	for _, numKeys := range []int{100, 1000, 10000, 100000} {
		b.Run(fmt.Sprintf("keys-%d", numKeys), func(b *testing.B) {
			c := newTestCache()

			for i := 0; i < numKeys; i++ {
				c.Set("key"+strconv.Itoa(i), "value", SetOptions{})
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Get("key" + strconv.Itoa(i%numKeys))
			}
		})
	}
}

// --- Вспомогательные ---

func randString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
