package cmd

import (
	"imcs/cache"
)



type CmdFunc func(args []string, c *cache.Cache) []byte
