package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"studbud/backend/db_sql"
	"studbud/backend/internal/config"
)

// main is the binary entrypoint.
func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

// run wires config, deps, schema setup, router, and runs the HTTP server with graceful shutdown.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config:\n%w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, cleanup, err := buildDeps(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := db_sql.SetupAll(ctx, d.db); err != nil {
		return fmt.Errorf("setup schema:\n%w", err)
	}

	d.scheduler.Start(ctx)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           buildRouter(d),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go serve(srv, cfg.Port)

	<-ctx.Done()
	log.Print("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown:\n%w", err)
	}
	return nil
}

// serve runs the HTTP listener, logging any unexpected errors.
func serve(srv *http.Server, port string) {
	log.Printf("listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("listen error: %v", err)
	}
}
