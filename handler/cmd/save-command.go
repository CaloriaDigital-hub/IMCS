package cmd

import (
	"fmt"
	"imcs/cache"
)




func handlerSave(args []string, c *cache.Cache) []byte {
	err := c.SaveToFile("cache.dumb")

	if err != nil {
		fmt.Println("Save error: ", err)
		return []byte("ERR save to file for 'save'\n")
	}

	return []byte("OK\n")
}