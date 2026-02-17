package command

import "imcs/internal/storage"

// handleDel обрабатывает команду DEL key.
func handleDel(args []string, c *storage.Cache) []byte {
	if len(args) != 1 {
		return respErrDel
	}

	c.Delete(args[0])
	return respOK
}
