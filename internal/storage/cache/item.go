package storage

// Item — элемент кеша.
//
// Key, Value, ExpireAt, HeapIndex меняются только под Lock шарда.
// LastAccess пишется атомарно под RLock (в Get).
type Item struct {
	Key        string
	Value      string
	ExpireAt   int64 // unix ns, 0 = без TTL
	LastAccess int64 // unix ns, atomic
	HeapIndex  int   // позиция в heap, -1 если TTL нет
}

func (i *Item) expired(now int64) bool {
	return i.ExpireAt > 0 && i.ExpireAt <= now
}
