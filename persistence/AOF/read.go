package AOF

import (
	"bufio"
	
	"strconv"
	"strings"
)



func(a *AOF) Read(rf func(cmd, key, value string, expire int64)) error {
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