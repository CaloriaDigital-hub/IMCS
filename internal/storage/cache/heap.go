package storage

// Len возвращает длину очереди.
func (pq priorityQueue) Len() int { return len(pq) }

// Less сравнивает элементы по времени истечения.
func (pq priorityQueue) Less(i, j int) bool {
	return pq[i].ExpireAt < pq[j].ExpireAt
}

// Swap меняет элементы местами и обновляет HeapIndex.
func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].HeapIndex = i
	pq[j].HeapIndex = j
}

// Push добавляет элемент в очередь.
func (pq *priorityQueue) Push(x interface{}) {
	item := x.(*Item)
	item.HeapIndex = len(*pq)
	*pq = append(*pq, item)
}

// Pop извлекает элемент с минимальным ExpireAt.
func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.HeapIndex = -1
	*pq = old[:n-1]
	return item
}
