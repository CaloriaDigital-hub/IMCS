package storage

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStress50K — жёсткий нагрузочный тест: 50 000 параллельных юзеров.
// Каждый юзер выполняет набор операций SET/GET/DEL.
// Выводит статистику: кол-во операций, скорость, задержки.
func TestStress50K(t *testing.T) {
	const (
		users        = 50_000
		opsPerUser   = 100
		keySpace     = 100_000
		readPercent  = 70
		writePercent = 20
		delPercent   = 10
	)

	c := New(&mockPersistence{})
	defer c.Close()

	// Предзаполняем кеш 100К ключей
	for i := 0; i < keySpace; i++ {
		c.Set("key:"+strconv.Itoa(i), "val:"+strconv.Itoa(i), 0, false)
	}

	var (
		totalSets    atomic.Int64
		totalGets    atomic.Int64
		totalDels    atomic.Int64
		totalHits    atomic.Int64
		totalMisses  atomic.Int64
		totalSetErrs atomic.Int64
	)

	var wg sync.WaitGroup
	wg.Add(users)

	start := time.Now()

	for u := 0; u < users; u++ {
		go func(userID int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(userID)))

			for op := 0; op < opsPerUser; op++ {
				key := "key:" + strconv.Itoa(rng.Intn(keySpace))
				roll := rng.Intn(100)

				switch {
				case roll < readPercent: // 70% GET
					_, found := c.Get(key)
					totalGets.Add(1)
					if found {
						totalHits.Add(1)
					} else {
						totalMisses.Add(1)
					}

				case roll < readPercent+writePercent: // 20% SET
					ttl := time.Duration(rng.Intn(300)+1) * time.Second
					err := c.Set(key, "updated:"+strconv.Itoa(userID), ttl, false)
					totalSets.Add(1)
					if err != nil {
						totalSetErrs.Add(1)
					}

				default: // 10% DEL
					c.Delete(key)
					totalDels.Add(1)
				}
			}
		}(u)
	}

	wg.Wait()
	elapsed := time.Since(start)

	sets := totalSets.Load()
	gets := totalGets.Load()
	dels := totalDels.Load()
	hits := totalHits.Load()
	misses := totalMisses.Load()
	setErrs := totalSetErrs.Load()
	totalOps := sets + gets + dels

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║          STRESS TEST: 50K USERS                 ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Users:           %6d concurrent               ║\n", users)
	fmt.Printf("║  Ops/user:        %6d                          ║\n", opsPerUser)
	fmt.Printf("║  Key space:       %6d keys                    ║\n", keySpace)
	fmt.Printf("║  Duration:        %v                    ║\n", elapsed.Round(time.Millisecond))
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Total ops:       %10d                      ║\n", totalOps)
	fmt.Printf("║  Throughput:      %10d ops/sec               ║\n", int64(float64(totalOps)/elapsed.Seconds()))
	fmt.Printf("║  Avg latency:     %10s/op                   ║\n", (elapsed / time.Duration(totalOps)).String())
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Println("║  WRITES (SET)                                   ║")
	fmt.Printf("║    Total:         %10d                      ║\n", sets)
	fmt.Printf("║    Errors:        %10d                      ║\n", setErrs)
	fmt.Printf("║    Throughput:    %10d ops/sec               ║\n", int64(float64(sets)/elapsed.Seconds()))
	fmt.Println("║  READS (GET)                                    ║")
	fmt.Printf("║    Total:         %10d                      ║\n", gets)
	fmt.Printf("║    Hits:          %10d                      ║\n", hits)
	fmt.Printf("║    Misses:        %10d                      ║\n", misses)
	fmt.Printf("║    Hit rate:         %5.1f%%                      ║\n", float64(hits)/float64(gets)*100)
	fmt.Printf("║    Throughput:    %10d ops/sec               ║\n", int64(float64(gets)/elapsed.Seconds()))
	fmt.Println("║  DELETES (DEL)                                  ║")
	fmt.Printf("║    Total:         %10d                      ║\n", dels)
	fmt.Printf("║    Throughput:    %10d ops/sec               ║\n", int64(float64(dels)/elapsed.Seconds()))
	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// TestStressBurst — тест на пиковую нагрузку.
// Все 50К юзеров стартуют одновременно (через sync.WaitGroup).
func TestStressBurst(t *testing.T) {
	const (
		users    = 50_000
		keySpace = 50_000
	)

	c := New(&mockPersistence{})
	defer c.Close()

	var (
		ready    sync.WaitGroup
		go_bang  sync.WaitGroup
		done     sync.WaitGroup
		setCount atomic.Int64
		getCount atomic.Int64
		delCount atomic.Int64
	)

	ready.Add(users)
	go_bang.Add(1)
	done.Add(users)

	for u := 0; u < users; u++ {
		go func(id int) {
			defer done.Done()
			key := "burst:" + strconv.Itoa(id%keySpace)
			val := "data:" + strconv.Itoa(id)

			ready.Done()
			go_bang.Wait() // все ждут сигнала

			// SET
			c.Set(key, val, 30*time.Second, false)
			setCount.Add(1)

			// GET
			c.Get(key)
			getCount.Add(1)

			// DEL каждый 5-й
			if id%5 == 0 {
				c.Delete(key)
				delCount.Add(1)
			}
		}(u)
	}

	ready.Wait() // ждём пока все горутины будут готовы
	start := time.Now()
	go_bang.Done() // ОГОНЬ!
	done.Wait()
	elapsed := time.Since(start)

	sets := setCount.Load()
	gets := getCount.Load()
	dels := delCount.Load()
	total := sets + gets + dels

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║          BURST TEST: 50K SIMULTANEOUS           ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Duration:        %v                    ║\n", elapsed.Round(time.Millisecond))
	fmt.Printf("║  Total ops:       %10d                      ║\n", total)
	fmt.Printf("║  Throughput:      %10d ops/sec               ║\n", int64(float64(total)/elapsed.Seconds()))
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  SET:             %10d                      ║\n", sets)
	fmt.Printf("║  GET:             %10d                      ║\n", gets)
	fmt.Printf("║  DEL:             %10d                      ║\n", dels)
	fmt.Println("╚══════════════════════════════════════════════════╝")
}
