package AOF

import (
	"bufio"
	"fmt"
	"log"
	"os"
)

// Rewrite компактит журнал: снапшот живых ключей + буфер докатки.
//
//  1. Включаем rewriting: каждая запись, которую writer пишет в старый файл,
//     дублируется в rewriteBuf.
//  2. Пишем снапшот во временный файл (без a.mu — writer продолжает работать).
//  3. Под a.mu дописываем rewriteBuf, fsync, атомарно подменяем файл.
//
// Запись, попавшая и в снапшот, и в буфер, применится повторно — это безопасно,
// потому что все записи журнала — абсолютные присваивания (см. types.go), а
// порядок записей по каждому ключу совпадает с порядком изменений в памяти.
//
// snapshot должен вызвать emit для каждого живого ключа.
func (a *AOF) Rewrite(snapshot func(emit func(cmd, key, value string, expire int64))) (err error) {
	if !a.rewriteRunning.CompareAndSwap(false, true) {
		return ErrRewriteInProgress
	}
	defer a.rewriteRunning.Store(false)

	if err := a.Err(); err != nil {
		return err
	}

	tmpPath := a.path + ".rewrite"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(tmp, writeBufSize)

	swapped := false
	defer func() {
		if swapped {
			return
		}
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		a.mu.Lock()
		a.rewriting = false
		a.rewriteBuf = nil
		a.mu.Unlock()
	}()

	// === 1. Включаем буфер докатки ===
	a.mu.Lock()
	a.rewriting = true
	a.rewriteBuf = nil
	a.mu.Unlock()

	// === 2. Снапшот ===
	var (
		scratch []byte
		werr    error
		written int
	)
	snapshot(func(cmd, key, value string, expire int64) {
		if werr != nil {
			return
		}
		scratch, _, werr = writeRecord(w, scratch, record{cmd: cmd, key: key, value: value, expire: expire})
		written++
	})
	if werr != nil {
		return werr
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// === 3. Докатка и подмена под a.mu ===
	a.mu.Lock()
	defer a.mu.Unlock()

	buffered := a.rewriteBuf
	a.rewriting = false
	a.rewriteBuf = nil

	for _, r := range buffered {
		if scratch, _, err = writeRecord(w, scratch, r); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	st, err := tmp.Stat()
	if err != nil {
		return err
	}
	// Windows: открытый файл нельзя переименовать.
	if err := tmp.Close(); err != nil {
		return err
	}

	// Старый файл дописываем до конца: если rename упадёт, он остаётся актуальным.
	if err := a.writer.Flush(); err != nil {
		a.fail(err)
		return err
	}
	if err := a.file.Sync(); err != nil {
		a.fail(err)
		return err
	}
	_ = a.file.Close() // уже сброшен и синхронизирован выше

	if err := os.Rename(tmpPath, a.path); err != nil {
		if reopenErr := a.reopenLocked(); reopenErr != nil {
			a.fail(reopenErr)
			return fmt.Errorf("rename failed: %v; reopen failed: %w", err, reopenErr)
		}
		return err
	}
	swapped = true
	syncDir(a.dir)

	if err := a.reopenLocked(); err != nil {
		a.fail(err)
		return err
	}
	a.size = st.Size()
	a.dirty = false
	a.lastRewriteSize.Store(a.size)

	log.Printf("AOF rewrite: %d keys + %d buffered records, %d bytes", written, len(buffered), a.size)
	return nil
}

// RewriteRunning сообщает, идёт ли сейчас rewrite.
func (a *AOF) RewriteRunning() bool {
	return a.rewriteRunning.Load()
}

func (a *AOF) reopenLocked() error {
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	a.file = f
	a.writer.Reset(f)
	return nil
}

// syncDir делает fsync каталога, чтобы rename пережил потерю питания.
// На Windows каталоги не синхронизируются — ошибку игнорируем.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
