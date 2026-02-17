package AOF

import (
	"bufio"
	"hash/crc64"
	"log"
	"os"
	"path/filepath"
	"strconv"
)

// Rewrite компактит AOF с буфером докатки (как Redis).
//
// Алгоритм:
//  1. Включаем rewriting flag → backgroundWriter начинает дублировать записи в rewriteBuf
//  2. Делаем Snapshot через callback — пишем живые ключи в новый файл
//  3. Останавливаем backgroundWriter на мьютексе
//  4. Дописываем rewriteBuf (записи, пришедшие во время snapshot) в новый файл
//  5. Atomic rename нового файла → старый
//  6. Переоткрываем файл для дальнейших записей
//  7. Выключаем rewriting flag
//
// Новые записи НЕ теряются — они в rewriteBuf.
func (a *AOF) Rewrite(snapshot func(fn func(cmd, key, value string, expireAt int64))) error {
	tmpPath := filepath.Join(a.dir, "journal.aof.rewrite")

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	writer := bufio.NewWriterSize(tmpFile, writeBufSize)
	written := 0

	// === Шаг 1: Включаем буфер докатки ===
	a.rewriteMu.Lock()
	a.rewriteBuf = a.rewriteBuf[:0] // очищаем
	a.rewriteMu.Unlock()
	a.rewriting.Store(true)

	// === Шаг 2: Snapshot — пишем живые ключи ===
	snapshot(func(cmd, key, value string, expireAt int64) {
		payload := make([]byte, 0, len(cmd)+len(key)+len(value)+32)
		payload = append(payload, cmd...)
		payload = append(payload, '|')
		payload = append(payload, key...)
		payload = append(payload, '|')
		payload = strconv.AppendInt(payload, expireAt, 10)
		payload = append(payload, '|')
		payload = append(payload, value...)

		checksum := crc64.Checksum(payload, crcTable)
		crcHex := strconv.FormatUint(checksum, 16)

		entry := make([]byte, 0, len(crcHex)+1+len(payload)+1)
		entry = append(entry, crcHex...)
		entry = append(entry, '|')
		entry = append(entry, payload...)
		entry = append(entry, '\n')

		writer.Write(entry)
		written++
	})

	// === Шаг 3-4: Забираем записи из буфера докатки ===
	// Останавливаем rewriting ПОСЛЕ забора буфера
	a.rewriteMu.Lock()
	buffered := make([][]byte, len(a.rewriteBuf))
	copy(buffered, a.rewriteBuf)
	a.rewriteBuf = a.rewriteBuf[:0]
	a.rewriting.Store(false) // новые записи больше не дублируются
	a.rewriteMu.Unlock()

	// Дописываем буфер докатки в новый файл
	for _, entry := range buffered {
		writer.Write(entry)
		written++
	}

	if err := writer.Flush(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	tmpFile.Close()

	// === Шаг 5-6: Atomic rename ===
	a.mu.Lock()
	defer a.mu.Unlock()

	a.writer.Flush()
	a.file.Sync()
	a.file.Close()

	origPath := filepath.Join(a.dir, "journal.aof")

	if err := os.Rename(tmpPath, origPath); err != nil {
		// Восстанавливаем старый файл
		f, _ := os.OpenFile(origPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
		a.file = f
		a.writer = bufio.NewWriterSize(f, writeBufSize)
		return err
	}

	f, err := os.OpenFile(origPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	a.file = f
	a.writer = bufio.NewWriterSize(f, writeBufSize)

	log.Printf("AOF rewrite: %d entries (incl %d buffered during rewrite)", written, len(buffered))
	return nil
}
