package command

import (
	"imcs/internal/storage"
	"strconv"
	"time"
)

// handleSet обрабатывает команду SET key value [seconds] [NX].
func handleSet(args []string, c *storage.Cache) []byte {
	if len(args) < 2 {
		return respErrSet
	}

	key := args[0]
	value := args[1]
	var ttl time.Duration
	var nx bool

	for i := 2; i < len(args); i++ {
		arg := args[i]

		// NX без аллокации (без strings.ToUpper)
		if arg == "NX" || arg == "nx" || arg == "Nx" || arg == "nX" {
			nx = true
			continue
		}

		seconds, err := strconv.Atoi(arg)
		if err != nil {
			return respErrSyn
		}
		ttl = time.Duration(seconds) * time.Second
	}

	if err := c.Set(key, value, ttl, nx); err == storage.ErrKeyExist {
		return respNil
	}

	return respOK
}
