package container

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nse09940/template/internal/config"
	"github.com/Nse09940/template/internal/generated"
	transport "github.com/Nse09940/template/internal/http"
	"github.com/Nse09940/template/internal/http/middleware"
	createhandler "github.com/Nse09940/template/internal/http/trip/create"
	finishhandler "github.com/Nse09940/template/internal/http/trip/finish"
	gethandler "github.com/Nse09940/template/internal/http/trip/get"
	"github.com/Nse09940/template/internal/infra/postgres"
	"github.com/Nse09940/template/internal/infra/postgres/transaction"
	"github.com/Nse09940/template/internal/infra/telemetry"
	repository "github.com/Nse09940/template/internal/repository/trip"
	createusecase "github.com/Nse09940/template/internal/usecase/trip/create"
	finishusecase "github.com/Nse09940/template/internal/usecase/trip/finish"
	getusecase "github.com/Nse09940/template/internal/usecase/trip/get"
)

type Container struct {
	server    *http.Server
	pool      *pgxpool.Pool
	telemetry *telemetry.Telemetry
	logger    *slog.Logger
}

func New(ctx context.Context, cfg config.Config) (*Container, error) {
	signals, err := telemetry.New(ctx, cfg.LogLevel)
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}
	app := &Container{telemetry: signals, logger: signals.Logger}
	if err := app.configure(ctx, cfg); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()
		return nil, errors.Join(err, app.Close(cleanupCtx))
	}
	return app, nil
}

func (c *Container) configure(ctx context.Context, cfg config.Config) error {
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	poolConfig.MaxConns = cfg.DatabaseMaxConns
	poolConfig.MinConns = cfg.DatabaseMinConns
	poolConfig.MaxConnLifetime = cfg.DatabaseMaxConnLifetime
	poolConfig.ConnConfig.ConnectTimeout = cfg.DatabaseConnectTimeout
	dbTracer, err := postgres.NewTracer(c.telemetry.TracerProvider, c.telemetry.MeterProvider)
	if err != nil {
		return err
	}
	poolConfig.ConnConfig.Tracer = dbTracer
	connectCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseConnectTimeout)
	defer cancel()
	c.pool, err = pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	if err := c.pool.Ping(connectCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	repo := repository.NewRepository(c.pool, cfg.DatabaseQueryTimeout)
	if err := c.telemetry.ObserveActiveTrips(repo); err != nil {
		return err
	}
	tx := transaction.NewTxManager(c.pool, cfg.DatabaseQueryTimeout, c.logger)
	create := createusecase.New(repo, tx, c.logger, c.telemetry)
	get := getusecase.New(repo)
	finish := finishusecase.New(repo, tx, c.logger, c.telemetry)
	handler := transport.NewHandler(createhandler.New(create, c.logger), gethandler.New(get, c.logger), finishhandler.New(finish, c.logger), c.pool, cfg.DatabaseQueryTimeout, c.logger)
	router := chi.NewRouter()
	api.HandlerWithOptions(handler, api.ChiServerOptions{BaseRouter: router, ErrorHandlerFunc: handler.ParameterError})
	instrumented, err := middleware.Wrap(router, c.logger, c.telemetry.TracerProvider, c.telemetry.MeterProvider)
	if err != nil {
		return fmt.Errorf("configure HTTP middleware: %w", err)
	}
	c.server = &http.Server{Addr: cfg.HTTPAddr, Handler: instrumented, ReadTimeout: cfg.HTTPReadTimeout, ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout, WriteTimeout: cfg.HTTPWriteTimeout, IdleTimeout: cfg.HTTPIdleTimeout}
	return nil
}

func (c *Container) Run(ctx context.Context) error {
	failures := make(chan error, 1)
	go func() {
		c.logger.InfoContext(ctx, "HTTP server started", "address", c.server.Addr)
		failures <- c.server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		return nil
	case err := <-failures:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}

func (c *Container) Close(ctx context.Context) error {
	var closeErr error
	if c.server != nil {
		if err := c.server.Shutdown(ctx); err != nil {
			closeErr = errors.Join(fmt.Errorf("shutdown HTTP server: %w", err), c.server.Close())
		}
	}
	if c.telemetry != nil {
		closeErr = errors.Join(closeErr, c.telemetry.Shutdown(ctx))
	}
	if c.pool != nil {
		closeErr = errors.Join(closeErr, closeWithinDeadline(ctx, c.pool.Close))
	}
	c.logger.InfoContext(ctx, "HTTP server stopped")
	return closeErr
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
