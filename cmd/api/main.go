// Command api is the KaziWise LMS HTTP server.
//
// It loads configuration, connects to Postgres, applies the schema, builds
// the storage driver and the auth service, and serves the API until the
// process is asked to stop.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kaziwise/kaziwise_backend/internal/api"
	"github.com/kaziwise/kaziwise_backend/internal/auth"
	"github.com/kaziwise/kaziwise_backend/internal/config"
	"github.com/kaziwise/kaziwise_backend/internal/storage"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet, so the failure is reported on
		// stderr as well as through the exit code.
		fmt.Fprintln(os.Stderr, "kaziwise-api: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	log.Info("starting kaziwise api", "config", cfg.Redacted())

	// The boot context is independent of the request context so a cancelled
	// request cannot abort a migration that is still in progress.
	bootCtx, cancelBoot := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBoot()

	db, err := connectWithRetry(bootCtx, cfg, log)
	if err != nil {
		return err
	}
	defer db.Close()

	// Schema application is opt-in through RUN_MIGRATIONS, which defaults to
	// on for local development. A deployment that manages its schema
	// elsewhere sets it to false and the boot skips straight to serving.
	if cfg.RunMigrations {
		if err := store.RunMigrations(bootCtx, db, cfg); err != nil {
			return fmt.Errorf("migrations: %w", err)
		}
		log.Info("schema is up to date")
	} else {
		log.Info("skipping migrations", "run_migrations", false)
	}
	// The demo content is opt-in. It is a separate step from the schema so
	// a production boot can never create example data by accident.
	if cfg.SeedDemo {
		if err := store.SeedDemo(bootCtx, db, cfg); err != nil {
			return fmt.Errorf("demo seed: %w", err)
		}
		log.Info("demo data seeded", "org", cfg.SeedOrgName, "org_slug", cfg.SeedOrgSlug)
	}

	authSvc := auth.New(cfg)

	// SEED_AUTH turns the seeded profiles into accounts that can actually
	// sign in. It is separate from the SQL seed because it may call the
	// Supabase admin API.
	if cfg.SeedAuthOnStart || cfg.SeedDemo {
		if err := seedAuthUsers(bootCtx, db, cfg, authSvc, log); err != nil {
			return fmt.Errorf("demo credentials: %w", err)
		}
	}

	st, err := buildStorage(cfg, log)
	if err != nil {
		return err
	}

	server := api.New(cfg, log, db, authSvc, st)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.Router(),
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	// A failed listen must not leave the process running with no way to
	// reach it, so the serve error is surfaced through a channel.
	serveErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := httpServer.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
		}
		return nil
	case sig := <-stop:
		log.Info("shutdown requested", "signal", sig.String())
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownGrace)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		// A grace period that expires is not a crash: the listener is
		// closed and in-flight requests are dropped.
		log.Error("graceful shutdown timed out", "error", err)
		_ = httpServer.Close()
	}
	// Wait for ListenAndServe to return so the process does not exit while
	// a goroutine is still writing to a closed listener.
	if err := <-serveErr; err != nil {
		log.Error("server stopped with an error", "error", err)
	}
	log.Info("stopped")
	return nil
}

// connectWithRetry waits for Postgres to accept connections. Supabase
// projects briefly refuse connections while a pooler warms up, so a single
// failed dial at boot should not crash the process.
func connectWithRetry(ctx context.Context, cfg *config.Config, log *slog.Logger) (*store.DB, error) {
	attempts := 1
	if cfg.DBConnectRetry > 0 {
		attempts = 10
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		db, err := store.Connect(ctx, cfg, log)
		if err == nil {
			return db, nil
		}
		lastErr = err
		log.Warn("database not ready, retrying", "attempt", i+1, "error", err)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect to database: %w", lastErr)
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("connect to database: %w", lastErr)
}

// buildStorage selects the storage driver named in the configuration. The
// local driver writes to disk, which is only appropriate for development.
func buildStorage(cfg *config.Config, log *slog.Logger) (storage.Driver, error) {
	switch cfg.Storage {
	case config.StorageSupabase:
		return storage.NewSupabase(cfg.SupabaseURL, cfg.SupabaseService), nil
	case config.StorageLocal:
		st, err := storage.NewLocal(cfg.LocalDiskRoot, cfg.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("local storage: %w", err)
		}
		return st, nil
	default:
		return nil, fmt.Errorf("unknown STORAGE_DRIVER %q", cfg.Storage)
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug", "DEBUG":
		level = slog.LevelDebug
	case "warn", "WARN":
		level = slog.LevelWarn
	case "error", "ERROR":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Env == "development" {
		// A text handler is far easier to read in a terminal; JSON is for
		// anything that aggregates logs.
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
