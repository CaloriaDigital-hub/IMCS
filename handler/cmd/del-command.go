package cmd

import (
	"imcs/cache"
)


func handleDel(args []string, c *cache.Cache) []byte {
	if len(args) != 1 {
		return []byte("ERR wrong number of args for 'del'\n")
	}


	c.Delete(args[0])
	return []byte("OK\n")
}