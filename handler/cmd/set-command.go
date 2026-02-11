package cmd

import (
	"imcs/cache"
	"strconv"
	"time"
)





func handleSet(args []string, c *cache.Cache) []byte {
	


	if len(args) == 3 {
		ttlSec, err := strconv.Atoi(args[2])

		if err != nil {
			return []byte("ERR invalid TTL format\n")
		}

		c.Set(args[0], args[1], time.Duration(ttlSec)*time.Second)
		return  []byte("OK\n")
	}

	return []byte("ERR wrong number of args for 'set'\n")
}