package command

import "imcs/internal/storage/cache"

// CmdFunc — тип функции-обработчика команды.
type CmdFunc func(args []string, c *storage.Cache) []byte

// Handlers — реестр команд.
var Handlers = map[string]CmdFunc{
	"SET": handleSet,
	"GET": handleGet,
	"DEL": handleDel,
}

// Pre-allocated ответы — 0 аллокаций для частых случаев.
var (
	respOK  = []byte("OK\n")
	respNil = []byte("(nil)\n")

	respErrSet = []byte("ERR wrong number of args for 'SET'\n")
	respErrGet = []byte("ERR wrong number of args for 'GET'\n")
	respErrDel = []byte("ERR wrong number of args for 'DEL'\n")
	respErrSyn = []byte("ERR syntax error\n")
)
