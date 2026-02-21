package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"imcs/internal/persistence/AOF"
	"imcs/internal/server"
	"imcs/internal/storage/cache"
	"imcs/internal/storage/janitor"
)

func main() {
	port := flag.String("port", ":6380", "TCP port to listen on")
	dir := flag.String("dir", "./cache-files", "Directory for AOF journal")
	auth := flag.String("auth", "", "Password for AUTH (empty = no auth)")
	flag.Parse()

	// Создаём AOF-персистер
	persister, err := AOF.NewPersister(*dir)
	if err != nil {
		log.Fatal("cannot open AOF:", err)
	}

	// Создаём шардированный кеш
	cache := storage.New(persister)

	// Инициализируем cold storage
	if err := cache.InitColdStorage(*dir); err != nil {
		log.Println("warning: cold storage init error:", err)
	}

	// Запускаем janitor (TTL expiry + cold eviction + cold flush)
	j := janitor.New(cache)
	j.Start()

	// Восстанавливаем данные из AOF с CRC64 проверкой
	result, err := persister.Read(func(cmd, key, value string, expire int64) {
		switch cmd {
		case "SET":
			if expire > 0 && expire < time.Now().UnixNano() {
				return
			}
			var ttl time.Duration
			if expire > 0 {
				ttl = time.Duration(expire-time.Now().UnixNano()) * time.Nanosecond
			}
			cache.Set(key, value, ttl, false)
		case "DEL":
			cache.Delete(key)
		}
	})
	if err != nil {
		log.Println("warning: AOF restore error:", err)
	}
	if result != nil {
		log.Printf("AOF: loaded %d entries", result.ValidEntries)
		if result.Truncated {
			log.Printf("AOF: truncated at offset %d (%d corrupt entries discarded)",
				result.TruncatedAt, result.CorruptEntries)
		}
	}

	// Создаём сервер с опциональным AUTH
	var opts []server.Option
	if *auth != "" {
		opts = append(opts, server.WithAuth(*auth))
	}
	srv := server.New(*port, cache, opts...)

	// Graceful shutdown: перехватываем SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down gracefully...")
		srv.Shutdown()
		j.Stop()
		cache.Close()
		persister.Close()
		log.Println("Bye!")
		os.Exit(0)
	}()

	log.Fatal(srv.Listen())
}
