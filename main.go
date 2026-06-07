// Command nekodrop runs the NekoDrop web-based file & message sharing server.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moehoshio/NekoDrop/internal/config"
	"github.com/moehoshio/NekoDrop/internal/room"
	"github.com/moehoshio/NekoDrop/internal/server"
	"github.com/moehoshio/NekoDrop/internal/storage"
)

func main() {
	// Resolve configuration: file < environment < flags.
	configPath := flag.String("config", defaultEnv("NEKODROP_CONFIG", "config.json"), "path to JSON config file (optional)")

	// Flags default to the empty/zero sentinel so we can tell whether the user
	// set them explicitly and only then override the file/env values.
	addr := flag.String("addr", "", "HTTP listen address (host:port); overrides config host/port")
	host := flag.String("host", "", "IP address to listen on (overrides config)")
	port := flag.Int("port", 0, "TCP port to listen on (overrides config)")
	maxUpload := flag.Int64("max-upload", 0, "maximum upload size in bytes (overrides config)")
	maxChannels := flag.Int("max-channels-per-user", -1, "maximum channels a single user may own (0 = unlimited)")
	storageBackend := flag.String("storage", "", "storage backend: memory, sqlite or mysql (overrides config)")
	storageDSN := flag.String("storage-dsn", "", "storage DSN: sqlite file path or mysql DSN (overrides config)")
	flag.Parse()

	// The config file is optional unless the user explicitly named one.
	explicitConfig := *configPath != "config.json" || os.Getenv("NEKODROP_CONFIG") != ""
	cfg, err := config.LoadFile(*configPath, !explicitConfig)
	if err != nil {
		log.Fatalf("nekodrop: %v", err)
	}
	cfg.ApplyEnv()

	// Flag overrides (highest precedence).
	if *addr != "" {
		cfg.ApplyAddr(*addr)
	}
	if *host != "" {
		cfg.Host = *host
	}
	if *port != 0 {
		cfg.Port = *port
	}
	if *maxUpload != 0 {
		cfg.MaxUploadBytes = *maxUpload
	}
	if *maxChannels >= 0 {
		cfg.MaxChannelsPerUser = *maxChannels
	}
	if *storageBackend != "" {
		cfg.Storage.Backend = *storageBackend
	}
	if *storageDSN != "" {
		cfg.Storage.DSN = *storageDSN
	}

	store, err := storage.Open(cfg.Storage.Backend, cfg.Storage.DSN)
	if err != nil {
		log.Fatalf("nekodrop: storage: %v", err)
	}
	defer store.Close()

	srv, err := server.New(server.Options{
		MaxUploadBytes:     cfg.MaxUploadBytes,
		MaxChannelsPerUser: cfg.MaxChannelsPerUser,
		Store:              store,
		Limits: room.Limits{
			MaxMessagesPerChannel:    cfg.Limits.MaxMessagesPerChannel,
			MaxFileBytesPerChannel:   cfg.Limits.MaxFileBytesPerChannel,
			MaxSubscribersPerChannel: cfg.Limits.MaxSubscribersPerChannel,
			MaxChannels:              cfg.Limits.MaxChannels,
		},
		MaxUsers: cfg.Limits.MaxUsers,
	})
	if err != nil {
		log.Fatalf("nekodrop: failed to initialize server: %v", err)
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("nekodrop: listening on %s (storage: %s)", cfg.Addr(), store.Backend())
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("nekodrop: server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("nekodrop: shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("nekodrop: graceful shutdown failed: %v", err)
	}
}

func defaultEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
