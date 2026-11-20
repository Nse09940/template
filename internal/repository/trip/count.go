package trip

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/Nse09940/template/internal/domain/trip"
)

func (r *Repository) CountActive(ctx context.Context) (int64, error) {
	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	query, args, err := statementBuilder.Select("count(*)").From("trips").
		Where(sq.Eq{statusColumn: trip.StatusActive}).ToSql()
	if err != nil {
		return 0, fmt.Errorf("build active trips query: %w", err)
	}
	var count int64
	if err := r.executor(ctx).QueryRow(queryCtx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active trips: %w", err)
	}
	return count, nil
}
