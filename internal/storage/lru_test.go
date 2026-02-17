package storage

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestLRUEviction(t *testing.T) {
	const maxKeys = 1000

	c := NewWithMaxKeys(&mockPersistence{}, maxKeys)
	defer c.Close()

	// Заполняем до лимита
	for i := 0; i < maxKeys; i++ {
		c.Set("key:"+strconv.Itoa(i), "val", 0, false)
	}

	if count := c.CountKeys(); count != maxKeys {
		t.Fatalf("expected %d keys, got %d", maxKeys, count)
	}

	// «Прогреваем» первые 100 ключей — они должны выжить
	for i := 0; i < 100; i++ {
		c.Get("key:" + strconv.Itoa(i))
	}

	// Добавляем 500 новых — LRU должен вытеснить старые
	for i := maxKeys; i < maxKeys+500; i++ {
		c.Set("key:"+strconv.Itoa(i), "newval", 0, false)
	}

	count := c.CountKeys()
	if count > maxKeys+50 { // допуск ~5% из-за concurrent eviction
		t.Fatalf("expected ~%d keys, got %d (eviction not working)", maxKeys, count)
	}

	// Проверяем что прогретые ключи выжили
	survived := 0
	for i := 0; i < 100; i++ {
		if _, found := c.Get("key:" + strconv.Itoa(i)); found {
			survived++
		}
	}

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║          LRU EVICTION TEST               ║")
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Printf("║  Max keys:         %6d                 ║\n", maxKeys)
	fmt.Printf("║  Keys after evict: %6d                 ║\n", count)
	fmt.Printf("║  Hot keys survived: %4d/100              ║\n", survived)
	fmt.Println("╚══════════════════════════════════════════╝")

	if survived < 50 {
		t.Errorf("expected most hot keys to survive, got %d/100", survived)
	}
}

func TestLRUEvictionUnderStress(t *testing.T) {
	const maxKeys int64 = 10_000

	c := NewWithMaxKeys(&mockPersistence{}, maxKeys)
	defer c.Close()

	// Вставляем 50К ключей — должен держаться около лимита
	for i := 0; i < 50_000; i++ {
		c.Set("stress:"+strconv.Itoa(i), "val:"+strconv.Itoa(i), time.Minute, false)
	}

	count := c.CountKeys()

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║       LRU STRESS TEST                    ║")
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Printf("║  Max keys:         %6d                 ║\n", maxKeys)
	fmt.Printf("║  Inserted:         %6d                 ║\n", 50_000)
	fmt.Printf("║  Keys remaining:   %6d                 ║\n", count)
	fmt.Printf("║  Evicted:          %6d                 ║\n", 50_000-count)
	fmt.Println("╚══════════════════════════════════════════╝")

	if count > maxKeys+500 {
		t.Errorf("expected ~%d keys, got %d", maxKeys, count)
	}
}
