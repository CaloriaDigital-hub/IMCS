package cold

import "sync"

/*

	Типы для package cold

*/

type entry struct {
	Key 	string
	Value	string
	ExpireAt int64
}


// Store - файловое хранилище для холодных данных (старые имеется ввиду)
type Store struct {
	mu		sync.RWMutex	//Я думаю над тем, чтобы поменять это на атомарные, если не стоит, то пишите
	dir 	string			
	index	map[string]entry // ключ значение
}

// Item — элемент для batch операций.
type Item struct {
	Key      string
	Value    string
	ExpireAt int64
}