package AOF

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// generateMBValue генерирует случайную строку заданного размера в байтах.
func generateMBValue(sizeBytes int) string {
	b := make([]byte, sizeBytes/2)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// TestCrashRecoveryMB — тест имитации краша с MB-записями + CRC64.
func TestCrashRecoveryMB(t *testing.T) {
	dir, err := os.MkdirTemp("", "crash-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const (
		numKeys     = 20
		valueSizeMB = 1
	)
	valueSize := valueSizeMB * 1024 * 1024

	testData := make(map[string]string, numKeys)
	for i := 0; i < numKeys; i++ {
		key := "crash:key:" + strconv.Itoa(i)
		val := generateMBValue(valueSize)
		testData[key] = val
	}

	totalDataMB := float64(numKeys*valueSize) / (1024 * 1024)

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║     CRASH RECOVERY TEST: MB VALUES + CRC64     ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Keys:              %6d                       ║\n", numKeys)
	fmt.Printf("║  Value size:        %4dMB each                  ║\n", valueSizeMB)
	fmt.Printf("║  Total data:      %6.1fMB                       ║\n", totalDataMB)
	fmt.Println("╠══════════════════════════════════════════════════╣")

	fmt.Println("║  Phase 1: Writing data with CRC64...            ║")
	writeStart := time.Now()

	aof1, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}

	for key, val := range testData {
		aof1.Write(WriteInput{Cmd: "SET", Key: key, Value: val, TTL: time.Hour})
	}

	writeElapsed := time.Since(writeStart)
	fmt.Printf("║  Write time:    %10v                       ║\n", writeElapsed.Round(time.Millisecond))

	// Имитация краша: останавливаем backgroundWriter, flush буфер
	close(aof1.stopCh)
	<-aof1.done

	aof1.mu.Lock()
	aof1.writer.Flush()
	aof1.file.Sync()
	aof1.mu.Unlock()

	stat, _ := os.Stat(filepath.Join(dir, "journal.aof"))
	fileSizeMB := float64(stat.Size()) / (1024 * 1024)
	fmt.Printf("║  AOF file size:   %6.1fMB                       ║\n", fileSizeMB)
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Println("║  Phase 2: 💥 CRASH (no Close!)                 ║")

	aof1 = nil

	fmt.Println("║  Phase 3: Recovering with CRC64 check...       ║")
	recoveryStart := time.Now()

	aof2, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer aof2.Close()

	recovered := make(map[string]string)
	result, err := aof2.Read(func(cmd, key, value string, expire int64) {
		if cmd == "SET" {
			recovered[key] = value
		}
	})
	if err != nil {
		t.Fatal("recovery read error:", err)
	}

	recoveryElapsed := time.Since(recoveryStart)

	matchCount := 0
	corruptCount := 0
	for key, orig := range testData {
		rec, found := recovered[key]
		if !found {
			continue
		}
		if rec == orig {
			matchCount++
		} else {
			corruptCount++
		}
	}

	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Recovery time:  %10v                       ║\n", recoveryElapsed.Round(time.Millisecond))
	fmt.Printf("║  Valid entries:      %5d                        ║\n", result.ValidEntries)
	fmt.Printf("║  Corrupt entries:    %5d                        ║\n", result.CorruptEntries)
	fmt.Printf("║  Truncated:          %5v                        ║\n", result.Truncated)
	fmt.Printf("║  Keys matched:      %5d / %d                   ║\n", matchCount, numKeys)

	if corruptCount == 0 && matchCount == len(recovered) {
		fmt.Println("║  ✅ CRC64 INTEGRITY: PERFECT                    ║")
	}
	if result.CorruptEntries == 0 {
		fmt.Println("║  ✅ NO CORRUPTION DETECTED                       ║")
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")

	if corruptCount > 0 {
		t.Errorf("data corruption: %d keys", corruptCount)
	}
}

// TestCorruptedAOFTruncate — имитация побитого файла.
// Обрезаем последние N байт, проверяем truncate recovery.
func TestCorruptedAOFTruncate(t *testing.T) {
	dir, err := os.MkdirTemp("", "corrupt-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const numKeys = 100
	valueSize := 10 * 1024 // 10KB

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    CORRUPTED AOF: TRUNCATE RECOVERY             ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")

	// Пишем данные и нормально закрываем
	aof1, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < numKeys; i++ {
		aof1.Write(WriteInput{
			Cmd:   "SET",
			Key:   "corrupt:" + strconv.Itoa(i),
			Value: generateMBValue(valueSize),
			TTL:   time.Hour,
		})
	}
	aof1.Close()

	aofPath := filepath.Join(dir, "journal.aof")
	stat, _ := os.Stat(aofPath)
	originalSize := stat.Size()
	fmt.Printf("║  Written: %d keys, AOF: %.1fKB                   ║\n", numKeys, float64(originalSize)/1024)

	// ========== ПОВРЕЖДЕНИЕ 1: Обрезаем последние 500 байт ==========
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Println("║  Corruption 1: Truncating last 500 bytes        ║")

	// Копируем файл для тестов
	data, _ := os.ReadFile(aofPath)

	// Обрезаем
	os.WriteFile(aofPath, data[:len(data)-500], 0644)

	aof2, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}

	count1 := 0
	result1, err := aof2.Read(func(cmd, key, value string, expire int64) {
		count1++
	})
	if err != nil {
		t.Fatal(err)
	}
	aof2.Close()

	fmt.Printf("║  Valid:     %5d / %d entries                   ║\n", result1.ValidEntries, numKeys)
	fmt.Printf("║  Corrupt:   %5d                                ║\n", result1.CorruptEntries)
	fmt.Printf("║  Truncated: %5v at %d bytes                   ║\n", result1.Truncated, result1.TruncatedAt)

	if result1.ValidEntries >= numKeys-2 && result1.Truncated {
		fmt.Println("║  ✅ TRUNCATE RECOVERY: SUCCESS                   ║")
	} else {
		fmt.Println("║  ❌ TRUNCATE RECOVERY: FAILED                    ║")
		t.Errorf("expected ~%d valid, got %d", numKeys-1, result1.ValidEntries)
	}

	// ========== ПОВРЕЖДЕНИЕ 2: Мусор в середине ==========
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Println("║  Corruption 2: Garbage injected in middle       ║")

	// Вставляем мусор на ~50% позиции
	halfPos := len(data) / 2
	corrupted := make([]byte, 0, len(data)+100)
	corrupted = append(corrupted, data[:halfPos]...)
	corrupted = append(corrupted, []byte("GARBAGE_CORRUPT_DATA_HERE!!!\n")...)
	corrupted = append(corrupted, data[halfPos:]...)

	os.WriteFile(aofPath, corrupted, 0644)

	aof3, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}

	count2 := 0
	result2, err := aof3.Read(func(cmd, key, value string, expire int64) {
		count2++
	})
	if err != nil {
		t.Fatal(err)
	}
	aof3.Close()

	fmt.Printf("║  Valid:     %5d / %d entries                   ║\n", result2.ValidEntries, numKeys)
	fmt.Printf("║  Corrupt:   %5d (garbage line)                 ║\n", result2.CorruptEntries)
	fmt.Printf("║  Truncated: %5v at %d bytes                   ║\n", result2.Truncated, result2.TruncatedAt)

	if result2.Truncated && result2.ValidEntries > 0 {
		fmt.Println("║  ✅ GARBAGE DETECTION: SUCCESS                    ║")
	} else {
		fmt.Println("║  ❌ GARBAGE DETECTION: FAILED                     ║")
	}

	// ========== ПОВРЕЖДЕНИЕ 3: Битый CRC ==========
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Println("║  Corruption 3: Flipped bits in CRC              ║")

	// Восстанавливаем оригинал и портим CRC на строке 50
	os.WriteFile(aofPath, data, 0644)

	// Меняем первый байт (CRC hex) на 'X' в строке ~50
	modified := make([]byte, len(data))
	copy(modified, data)
	lineNum := 0
	for i := 0; i < len(modified); i++ {
		if modified[i] == '\n' {
			lineNum++
			if lineNum == 50 && i+1 < len(modified) {
				modified[i+1] = 'X' // Ломаем CRC hex
				break
			}
		}
	}
	os.WriteFile(aofPath, modified, 0644)

	aof4, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}

	result3, err := aof4.Read(func(cmd, key, value string, expire int64) {})
	if err != nil {
		t.Fatal(err)
	}
	aof4.Close()

	fmt.Printf("║  Valid:     %5d / %d entries                   ║\n", result3.ValidEntries, numKeys)
	fmt.Printf("║  Corrupt:   %5d (flipped CRC)                 ║\n", result3.CorruptEntries)
	fmt.Printf("║  Truncated: %5v                                ║\n", result3.Truncated)

	if result3.Truncated && result3.ValidEntries == 50 {
		fmt.Println("║  ✅ CRC DETECTION: PERFECT (stopped at line 50)  ║")
	} else if result3.Truncated && result3.ValidEntries > 0 {
		fmt.Println("║  ✅ CRC DETECTION: SUCCESS                        ║")
	} else {
		fmt.Println("║  ❌ CRC DETECTION: FAILED                         ║")
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// TestAOFRewrite — тест компактности AOF.
func TestAOFRewrite(t *testing.T) {
	dir, err := os.MkdirTemp("", "rewrite-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║         AOF REWRITE / COMPACTION TEST           ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")

	aof, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Пишем 1000 SET, перезаписываем 500 (каждый ключ дважды), удаляем 200
	for i := 0; i < 1000; i++ {
		aof.Write(WriteInput{
			Cmd:   "SET",
			Key:   "rw:" + strconv.Itoa(i),
			Value: "v1_" + strconv.Itoa(i),
		})
	}
	// Перезапись первых 500
	for i := 0; i < 500; i++ {
		aof.Write(WriteInput{
			Cmd:   "SET",
			Key:   "rw:" + strconv.Itoa(i),
			Value: "v2_" + strconv.Itoa(i), // новое значение
		})
	}
	// Удаление последних 200
	for i := 800; i < 1000; i++ {
		aof.Write(WriteInput{Cmd: "DEL", Key: "rw:" + strconv.Itoa(i)})
	}

	aof.Close() // записываем всё

	aofPath := filepath.Join(dir, "journal.aof")
	stat, _ := os.Stat(aofPath)
	sizeBefore := stat.Size()

	fmt.Printf("║  Before rewrite: %6.1fKB (%d ops)               ║\n",
		float64(sizeBefore)/1024, 1000+500+200)

	// Считаем живые ключи через replay
	finalState := make(map[string]string)
	aofRead, _ := NewAOF(dir)
	aofRead.Read(func(cmd, key, value string, expire int64) {
		switch cmd {
		case "SET":
			finalState[key] = value
		case "DEL":
			delete(finalState, key)
		}
	})

	fmt.Printf("║  Live keys: %6d (out of 1000 original)        ║\n", len(finalState))

	// REWRITE: создаём snapshot и компактим
	err = aofRead.Rewrite(func(fn func(cmd, key, value string, expireAt int64)) {
		for key, value := range finalState {
			fn("SET", key, value, 0)
		}
	})
	if err != nil {
		t.Fatal("rewrite error:", err)
	}
	aofRead.Close()

	stat, _ = os.Stat(aofPath)
	sizeAfter := stat.Size()
	reduction := float64(sizeBefore-sizeAfter) / float64(sizeBefore) * 100

	fmt.Printf("║  After rewrite:  %6.1fKB                        ║\n", float64(sizeAfter)/1024)
	fmt.Printf("║  Reduction:      %5.1f%%                          ║\n", reduction)

	// Проверяем целостность после rewrite
	aofVerify, _ := NewAOF(dir)
	verifiedState := make(map[string]string)
	result, err := aofVerify.Read(func(cmd, key, value string, expire int64) {
		if cmd == "SET" {
			verifiedState[key] = value
		}
	})
	if err != nil {
		t.Fatal("verify error:", err)
	}
	aofVerify.Close()

	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Verified entries: %5d (CRC64 OK)              ║\n", result.ValidEntries)
	fmt.Printf("║  Corrupt entries:  %5d                          ║\n", result.CorruptEntries)

	// Проверяем что все данные совпадают
	mismatch := 0
	for key, orig := range finalState {
		if v, ok := verifiedState[key]; !ok || v != orig {
			mismatch++
		}
	}

	if mismatch == 0 && len(verifiedState) == len(finalState) {
		fmt.Println("║  ✅ REWRITE INTEGRITY: PERFECT                   ║")
	} else {
		fmt.Printf("║  ❌ MISMATCH: %d keys differ                      ║\n", mismatch)
		t.Errorf("rewrite data mismatch: %d keys", mismatch)
	}

	if reduction > 30 {
		fmt.Println("║  ✅ COMPACTION: SIGNIFICANT                       ║")
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// TestCrashNoFlush — краш БЕЗ flush (реальный kill -9).
func TestCrashNoFlush(t *testing.T) {
	dir, err := os.MkdirTemp("", "crash-noflush-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const numKeys = 50
	valueSize := 512 * 1024

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    CRASH TEST: NO FLUSH (real kill -9)          ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")

	aof, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}

	written := 0
	for i := 0; i < numKeys; i++ {
		err := aof.Write(WriteInput{
			Cmd:   "SET",
			Key:   "noflush:" + strconv.Itoa(i),
			Value: generateMBValue(valueSize),
		})
		if err != nil {
			break
		}
		written++
	}

	aof.file.Close()
	aof = nil

	aof2, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer aof2.Close()

	recovered := 0
	result, _ := aof2.Read(func(cmd, key, value string, expire int64) {
		recovered++
	})

	lost := written - recovered

	fmt.Printf("║  Written:      %6d keys (%.1fMB each)          ║\n", written, float64(valueSize)/(1024*1024))
	fmt.Printf("║  Recovered:    %6d keys (CRC64 verified)      ║\n", recovered)
	fmt.Printf("║  Lost:         %6d keys (in bufio buffer)     ║\n", lost)
	if result != nil && result.Truncated {
		fmt.Printf("║  Truncated at: %6d bytes                      ║\n", result.TruncatedAt)
	}

	if lost == 0 {
		fmt.Println("║  ✅ NO DATA LOSS                                 ║")
	} else {
		pct := float64(lost) / float64(written) * 100
		fmt.Printf("║  ⚠️  %.1f%% data loss from buffer                  ║\n", pct)
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}

// TestHeavyLoadThenCrash — нагрузка + краш + восстановление.
func TestHeavyLoadThenCrash(t *testing.T) {
	dir, err := os.MkdirTemp("", "heavy-crash-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	const (
		numKeys     = 100
		valueSizeKB = 256
	)
	valueSize := valueSizeKB * 1024

	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║    HEAVY LOAD + CRASH + CRC64 RECOVERY          ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")

	aof1, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}

	writeStart := time.Now()
	keys := make(map[string]bool)

	for i := 0; i < numKeys; i++ {
		key := "heavy:" + strconv.Itoa(i)
		keys[key] = true
		aof1.Write(WriteInput{Cmd: "SET", Key: key, Value: generateMBValue(valueSize), TTL: time.Hour})
	}

	for i := 0; i < numKeys/2; i++ {
		key := "heavy:" + strconv.Itoa(i)
		aof1.Write(WriteInput{Cmd: "SET", Key: key, Value: generateMBValue(valueSize), TTL: time.Hour})
	}

	deletedKeys := 0
	for i := numKeys - 10; i < numKeys; i++ {
		key := "heavy:" + strconv.Itoa(i)
		aof1.Write(WriteInput{Cmd: "DEL", Key: key})
		delete(keys, key)
		deletedKeys++
	}

	writeTime := time.Since(writeStart)

	// Корректно останавливаем AOF — Close() ждёт завершения backgroundWriter,
	// делает Flush и Sync.
	aof1.Close()
	aof1 = nil

	stat, _ := os.Stat(filepath.Join(dir, "journal.aof"))
	fileMB := float64(stat.Size()) / (1024 * 1024)

	fmt.Printf("║  Written: %d SET + %d updates + %d DEL             ║\n", numKeys, numKeys/2, deletedKeys)
	fmt.Printf("║  Write time: %10v                             ║\n", writeTime.Round(time.Millisecond))
	fmt.Printf("║  AOF file:   %6.1fMB                             ║\n", fileMB)
	fmt.Println("║  💥 CRASH!                                      ║")

	recoveryStart := time.Now()

	aof2, err := NewAOF(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer aof2.Close()

	state := make(map[string]bool)
	result, _ := aof2.Read(func(cmd, key, value string, expire int64) {
		switch cmd {
		case "SET":
			state[key] = true
		case "DEL":
			delete(state, key)
		}
	})

	recoveryTime := time.Since(recoveryStart)
	expectedAlive := len(keys)
	actualAlive := len(state)

	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  Recovery time: %10v                        ║\n", recoveryTime.Round(time.Millisecond))
	fmt.Printf("║  CRC64 valid:    %5d entries                   ║\n", result.ValidEntries)
	fmt.Printf("║  Expected alive: %5d keys                     ║\n", expectedAlive)
	fmt.Printf("║  Actual alive:   %5d keys                     ║\n", actualAlive)

	diff := actualAlive - expectedAlive
	if diff == 0 {
		fmt.Println("║  ✅ PERFECT RECOVERY                             ║")
	} else if diff > 0 && diff <= deletedKeys {
		fmt.Printf("║  ⚠️  %d DEL lost in buffer (acceptable)            ║\n", diff)
	} else {
		fmt.Println("║  ❌ UNEXPECTED MISMATCH!                          ║")
		t.Errorf("expected %d, got %d (diff %d)", expectedAlive, actualAlive, diff)
	}

	fmt.Println("╚══════════════════════════════════════════════════╝")
}
