package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Nse09940/template/internal/config"
	"github.com/Nse09940/template/internal/generated"
	"github.com/Nse09940/template/internal/httpapi"
	"github.com/Nse09940/template/internal/postgres"
	"github.com/Nse09940/template/internal/trip"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const serviceName = "trip-service"

func main() {
	slog.SetDefault(newJSONLogger(os.Stdout, slog.LevelInfo))
	if err := run(); err != nil {
		slog.Error("trip service exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger, err := newLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("configure logger: %w", err)
	}
	slog.SetDefault(logger)

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolConfig.MaxConns = cfg.DatabaseMaxConns
	poolConfig.MinConns = cfg.DatabaseMinConns
	poolConfig.MaxConnLifetime = cfg.DatabaseMaxConnLifetime
	poolConfig.ConnConfig.ConnectTimeout = cfg.DatabaseConnectTimeout

	connectCtx, connectCancel := context.WithTimeout(
		context.Background(),
		cfg.DatabaseConnectTimeout,
	)
	defer connectCancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}

	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return fmt.Errorf("ping database: %w", err)
	}

	repository := postgres.NewRepository(pool, cfg.DatabaseQueryTimeout)
	txManager := postgres.NewTxManager(pool, cfg.DatabaseQueryTimeout)
	service := trip.NewService(repository, txManager)
	handler := httpapi.NewHandler(service, pool, cfg.DatabaseQueryTimeout, logger)

	router := chi.NewRouter()
	api.HandlerWithOptions(handler, api.ChiServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: handler.ParameterError,
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadTimeout:       cfg.HTTPReadTimeout,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
	}

	appCtx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP server started", "address", cfg.HTTPAddr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-appCtx.Done():
		logger.Info("shutdown signal received")
	case serveErr := <-serverErrors:
		closeCtx, closeCancel := context.WithTimeout(
			context.Background(),
			cfg.ShutdownTimeout,
		)
		defer closeCancel()
		closeErr := closeWithinDeadline(closeCtx, pool.Close)
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(fmt.Errorf("serve HTTP: %w", serveErr), closeErr)
		}
		return closeErr
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		cfg.ShutdownTimeout,
	)
	defer shutdownCancel()

	var shutdownErr error
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown timed out", "error", err)
		_ = server.Close()
		shutdownErr = fmt.Errorf("shutdown HTTP server: %w", err)
	}

	if err := closeWithinDeadline(shutdownCtx, pool.Close); err != nil {
		logger.Error("database pool shutdown timed out", "error", err)
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("close database pool: %w", err))
	}

	if shutdownErr != nil {
		return shutdownErr
	}

	logger.Info("HTTP server stopped")
	return nil
}

func closeWithinDeadline(ctx context.Context, closeFn func()) error {
	done := make(chan struct{})
	go func() {
		closeFn()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newLogger(levelText string) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelText)); err != nil {
		return nil, fmt.Errorf("LOG_LEVEL: %w", err)
	}

	return newJSONLogger(os.Stdout, level), nil
}

func newJSONLogger(output io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: level,
	})).With("service", serviceName)
}
