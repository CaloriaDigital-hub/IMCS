package cmd




var Handlers = map[string]CmdFunc{
	"SET": handleSet,
	"GET": handleGet,
	"DEL": handleDel,
	"SAVE": handlerSave,
}

