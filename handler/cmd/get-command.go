package cmd

import (
	"imcs/cache"
	
)



func handleGet(args []string, c *cache.Cache) []byte {
	
	if len(args) != 1 {
		return []byte("ERR wrong number of args for 'get'\n")
	}

	value, found := c.Get(args[0])

	if !found {
		return []byte("(nil)\n")
	}

	return []byte(value + "\n")
}


