package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	imcs "github.com/CaloriaDigital-hub/IMCS"
)

func main() {
	addr := flag.String("port", ":6380", "address to listen on (host:port or :port)")
	dir := flag.String("dir", "./cache-files", "directory for the AOF journal")
	auth := flag.String("auth", "", "password for AUTH (or env IMCS_PASSWORD; empty = no auth)")
	maxKeys := flag.Int64("maxkeys", 0, "max number of keys, LRU eviction above it (0 = unlimited)")
	maxClients := flag.Int("maxclients", 10000, "max simultaneous client connections")
	idle := flag.Duration("timeout", 0, "close idle client connections after this duration (0 = never)")
	protected := flag.Bool("protected-mode", true, "without a password, accept only loopback clients")
	repair := flag.Bool("aof-repair", false, "truncate the journal at a corrupt record in the middle (data after it is lost)")
	flag.Parse()

	password := *auth
	if password == "" {
		// Пароль из окружения не виден в `ps`.
		password = os.Getenv("IMCS_PASSWORD")
	}

	db, err := imcs.OpenWithOptions(*dir, imcs.Options{
		MaxKeys:              *maxKeys,
		Password:             password,
		MaxClients:           *maxClients,
		IdleTimeout:          *idle,
		DisableProtectedMode: !*protected,
		RepairAOF:            *repair,
	})
	if err != nil {
		log.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- db.ListenAndServe(*addr) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	exitCode := 0
	select {
	case sig := <-sigCh:
		log.Printf("received %v, shutting down...", sig)
	case err := <-errCh:
		log.Printf("server error: %v", err)
		exitCode = 1
	}

	start := time.Now()
	if err := db.Close(); err != nil {
		log.Printf("close error: %v", err)
		exitCode = 1
	}
	log.Printf("stopped in %v", time.Since(start).Round(time.Millisecond))
	os.Exit(exitCode)
}
