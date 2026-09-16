// Package sqlrows defines the minimal SQL row-iteration interfaces shared by
// packages that query Snowflake or other SQL-like backends (mirrors database/sql).
package sqlrows

import "context"

// Rows is the minimal row-iteration interface (mirrors database/sql.Rows).
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// Querier executes a SQL query and returns iterable rows.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
}
