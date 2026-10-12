package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txContextKey struct{}

type TxManager struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

func NewTxManager(pool *pgxpool.Pool, timeout time.Duration) *TxManager {
	return &TxManager{pool: pool, timeout: timeout}
}

func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if transactionFromContext(ctx) != nil {
		return fn(ctx)
	}

	txCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	tx, err := m.pool.BeginTx(txCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	txCtx = context.WithValue(txCtx, txContextKey{}, tx)

	defer func() {
		if recovered := recover(); recovered != nil {
			rollbackCtx, rollbackCancel := m.cleanupContext(ctx)
			rollbackErr := tx.Rollback(rollbackCtx)
			if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				slog.ErrorContext(
					rollbackCtx,
					"rollback transaction after panic failed",
					"error", rollbackErr,
				)
			}
			rollbackCancel()
			panic(recovered)
		}
	}()

	if err = fn(txCtx); err != nil {
		rollbackCtx, rollbackCancel := m.cleanupContext(ctx)
		rollbackErr := tx.Rollback(rollbackCtx)
		rollbackCancel()
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
		}
		return err
	}

	if err = tx.Commit(txCtx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (m *TxManager) cleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), m.timeout)
}

func transactionFromContext(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(txContextKey{}).(pgx.Tx)
	return tx
}
