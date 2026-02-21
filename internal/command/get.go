package command

import "imcs/internal/storage/cache"

// handleGet обрабатывает команду GET key.
func handleGet(args []string, c *storage.Cache) []byte {
	if len(args) != 1 {
		return respErrGet
	}

	value, found := c.Get(args[0])
	if !found {
		return respNil
	}

	// Собираем ответ в один буфер — 1 аллокация вместо 2
	buf := make([]byte, len(value)+1)
	copy(buf, value)
	buf[len(value)] = '\n'
	return buf
}
