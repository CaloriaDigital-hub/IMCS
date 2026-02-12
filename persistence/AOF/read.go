package AOF

import (
	"bufio"

	"strconv"
	"strings"
)


		// Package AOF предоставляет механизмы для работы с журналом команд (append-only file).
		//
		// Файл read.go содержит функцию Read, которая читает команды из файла журнала и передает их обработчику.
		//
		// Read:
		//   - Считывает строки из файла журнала команд.
		//   - Каждая строка должна содержать команду, ключ, время истечения (expire) и значение, разделённые символом '|'.
		//   - Если строка не соответствует формату или время истечения некорректно, она пропускается.
		//   - Для каждой корректной строки вызывается переданная функция-обработчик rf(cmd, key, value, expire).
		//   - Функция блокирует доступ к файлу на время чтения.
func (a *AOF) Read(rf func(cmd, key, value string, expire int64)) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	_, err := a.file.Seek(0, 0)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(a.file)
	for scanner.Scan() {
		line := scanner.Text()

		parts := strings.Split(line, "|")

		if len(parts) < 4 {
			continue
		}

		cmd := parts[0]
		key := parts[1]
		expireStr := parts[2]


		value := parts[3]

		expire, err := strconv.ParseInt(expireStr, 10, 64)

		if err != nil {
			continue
		}

		rf(cmd, key, value, expire)

	}

	return scanner.Err()
}
