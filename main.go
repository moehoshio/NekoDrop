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
	"strconv"
	"syscall"
	"time"

	"github.com/moehoshio/NekoDrop/internal/server"
)

func main() {
	addr := flag.String("addr", defaultEnv("NEKODROP_ADDR", ":8080"), "HTTP listen address")
	maxUpload := flag.Int64("max-upload", defaultEnvInt("NEKODROP_MAX_UPLOAD", server.DefaultMaxUploadBytes), "maximum upload size in bytes")
	flag.Parse()

	srv, err := server.New(server.Options{MaxUploadBytes: *maxUpload})
	if err != nil {
		log.Fatalf("nekodrop: failed to initialize server: %v", err)
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("nekodrop: listening on %s", *addr)
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

func defaultEnvInt(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}
