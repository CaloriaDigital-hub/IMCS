package janitor

import "time"

/*
	Каждые tickInterval janitor удаляет истёкшие ключи. Один шард держится
	под Lock не дольше 256 удалений, а весь тик ограничен tickBudget —
	так клиентские запросы не стопорятся даже на 2 vCPU, а при потоке
	истекающих ключей тик сам растягивается до бюджета.
*/

const (
	tickInterval = 100 * time.Millisecond
	tickBudget   = 25 * time.Millisecond
)

// Start запускает фоновую горутину. Повторный вызов — no-op.
func (j *Janitor) Start() {
	j.start.Do(func() {
		j.started = true
		go j.run()
	})
}

// Stop останавливает janitor и ждёт завершения текущего тика.
func (j *Janitor) Stop() {
	j.stop.Do(func() {
		close(j.stopCh)
		if j.started {
			<-j.done
		}
	})
}

func (j *Janitor) run() {
	defer close(j.done)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			j.cache.ExpireByTTL(tickBudget)
		case <-j.stopCh:
			return
		}
	}
}
