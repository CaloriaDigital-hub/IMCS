package AOF

import (
	"bufio"
	"hash/crc64"
	"io"
	"log"
	"strconv"
	"strings"
)

const maxScanSize = 16 * 1024 * 1024 // 16MB макс размер строки

// Read считывает все команды из AOF, проверяя CRC64.
// Формат строки: crc64hex|cmd|key|expire|value
// При обнаружении битой записи — обрезает файл до последней валидной.
// Возвращает ReadResult с информацией о восстановлении.
func (a *AOF) Read(rf func(cmd, key, value string, expire int64)) (*ReadResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, err := a.file.Seek(0, 0); err != nil {
		return nil, err
	}

	result := &ReadResult{}
	scanner := bufio.NewScanner(a.file)
	scanner.Buffer(make([]byte, 64*1024), maxScanSize)

	var lastValidPos int64

	for scanner.Scan() {
		line := scanner.Text()
		lineLen := int64(len(scanner.Bytes())) + 1 // +1 для \n

		// Парсим: crc64hex|cmd|key|expire|value
		// Первый | отделяет CRC от payload
		sepIdx := strings.IndexByte(line, '|')
		if sepIdx < 1 {
			// Формат не соответствует — считаем corrupt, обрезаем
			log.Printf("AOF: corrupt entry at offset %d (no CRC separator)", lastValidPos)
			result.CorruptEntries++
			result.Truncated = true
			result.TruncatedAt = lastValidPos
			break
		}

		crcHex := line[:sepIdx]
		payload := line[sepIdx+1:]

		// Проверяем CRC64
		storedCRC, err := strconv.ParseUint(crcHex, 16, 64)
		if err != nil {
			// CRC не парсится — может быть старый формат без CRC
			// Попробуем прочитать как legacy формат: cmd|key|expire|value
			if parseLegacy(line, rf) {
				result.ValidEntries++
				lastValidPos += lineLen
				continue
			}
			log.Printf("AOF: corrupt CRC at offset %d", lastValidPos)
			result.CorruptEntries++
			result.Truncated = true
			result.TruncatedAt = lastValidPos
			break
		}

		computedCRC := crc64.Checksum([]byte(payload), crcTable)
		if storedCRC != computedCRC {
			log.Printf("AOF: CRC mismatch at offset %d (stored=%x computed=%x)",
				lastValidPos, storedCRC, computedCRC)
			result.CorruptEntries++
			result.Truncated = true
			result.TruncatedAt = lastValidPos
			break
		}

		// CRC OK — парсим payload: cmd|key|expire|value
		parts := strings.SplitN(payload, "|", 4)
		if len(parts) < 4 {
			log.Printf("AOF: malformed payload at offset %d", lastValidPos)
			result.CorruptEntries++
			result.Truncated = true
			result.TruncatedAt = lastValidPos
			break
		}

		expire, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			log.Printf("AOF: bad expire at offset %d", lastValidPos)
			result.CorruptEntries++
			result.Truncated = true
			result.TruncatedAt = lastValidPos
			break
		}

		result.ValidEntries++
		lastValidPos += lineLen
		rf(parts[0], parts[1], parts[3], expire)
	}

	if err := scanner.Err(); err != nil {
		return result, err
	}

	// Обрезаем файл если нашли corruption
	if result.Truncated {
		log.Printf("AOF: truncating file at offset %d (recovered %d entries, discarded %d)",
			result.TruncatedAt, result.ValidEntries, result.CorruptEntries)
		if err := a.file.Truncate(result.TruncatedAt); err != nil {
			return result, err
		}
		// Перемещаем seek на конец для дальнейшей записи
		if _, err := a.file.Seek(0, io.SeekEnd); err != nil {
			return result, err
		}
	}

	return result, nil
}

// parseLegacy пытается прочитать запись в старом формате (без CRC): cmd|key|expire|value
func parseLegacy(line string, rf func(cmd, key, value string, expire int64)) bool {
	parts := strings.SplitN(line, "|", 4)
	if len(parts) < 4 {
		return false
	}

	// Проверяем что первая часть — валидная команда (SET, DEL, GET)
	cmd := parts[0]
	if cmd != "SET" && cmd != "DEL" && cmd != "GET" {
		return false
	}

	expire, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return false
	}

	rf(cmd, parts[1], parts[3], expire)
	return true
}
