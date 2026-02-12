package cmd

import (
	"imcs/cache"
	"strconv"
	"strings"
	"time"
)



func handleSet(args []string, c *cache.Cache) []byte {

	
	
	if len(args) == 3 {

		key := args[0]
		value := args[1]
		var ttl time.Duration
		var nx bool


		for i := 2; i < len(args); i++ {
			arg := strings.ToUpper(args[i])

			if arg == "NX" {
				nx = true
				continue
			}

			ttlInt, err := strconv.Atoi(arg)		

			if err == nil {
				ttl =  time.Duration(ttlInt) * time.Second
				continue
			}


			return []byte("ERR syntex error\n")

		

		}

		err := c.Set(key, value, ttl, nx)
		

		if err == cache.ErrKeyExist {
			return []byte("(nil)\n")
		}

		return []byte("OK\n")
	}

	return []byte("ERR wrong number of args for 'set'\n")
}