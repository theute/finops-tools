// Package awsaccounts queries Snowflake marts for AWS organization account metrics.
package awsaccounts

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/openshift-online/finops-tools/core/cost"
	"github.com/openshift-online/finops-tools/core/sqlrows"
)

const (
	AggregatePayer = "payer"
	AggregateSum   = "sum"
)

var (
	awsAccountIDRE    = regexp.MustCompile(`^\d{12}$`)
	qualifiedTableRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*\.[A-Za-z_][A-Za-z0-9_$]*\.[A-Za-z_][A-Za-z0-9_$]*$`)
	dateOnlyLayout    = "2006-01-02"
)

// QueryOptions configures a historical account-count query.
type QueryOptions struct {
	Table          string
	PayerAccountID string
	From           time.Time // optional; date-only semantics when non-zero
	To             time.Time // optional; date-only semantics when non-zero
	Aggregate      string    // AggregatePayer or AggregateSum
	MaxRows        int       // 0 means unlimited
}

// DailyPoint is one daily account-count observation.
type DailyPoint struct {
	Date              string
	PayerAccountID    string
	NBActiveAccounts  int64
	NBClosedAccounts  int64
	NBDeletedAccounts int64
}

// QueryResult holds daily points and whether additional rows were omitted.
type QueryResult struct {
	Points    []DailyPoint
	Truncated bool
}

// RowIterator mirrors database/sql.Rows; kept as an alias for backward compatibility.
type RowIterator = sqlrows.Rows

// RowQuerier executes a SQL query and returns iterable rows; kept as an alias for backward compatibility.
type RowQuerier = sqlrows.Querier

// DBQuerier adapts *sql.DB to RowQuerier.
type DBQuerier struct {
	DB *sql.DB
}

// QueryContext implements RowQuerier.
func (q DBQuerier) QueryContext(ctx context.Context, query string, args ...any) (RowIterator, error) {
	return q.DB.QueryContext(ctx, query, args...)
}

// ValidateQueryOptions normalizes and validates query options.
func ValidateQueryOptions(opts QueryOptions) (QueryOptions, error) {
	opts.Table = strings.TrimSpace(opts.Table)
	if opts.Table == "" {
		return QueryOptions{}, fmt.Errorf("table is required")
	}
	if !qualifiedTableRE.MatchString(opts.Table) {
		return QueryOptions{}, fmt.Errorf("invalid table %q: expected DATABASE.SCHEMA.TABLE", opts.Table)
	}

	opts.PayerAccountID = strings.TrimSpace(opts.PayerAccountID)
	if opts.PayerAccountID != "" {
		if !awsAccountIDRE.MatchString(opts.PayerAccountID) {
			return QueryOptions{}, fmt.Errorf("invalid payer_account_id %q (expected 12 digits)", opts.PayerAccountID)
		}
	}

	opts.Aggregate = strings.TrimSpace(strings.ToLower(opts.Aggregate))
	if opts.Aggregate == "" {
		opts.Aggregate = AggregatePayer
	}
	switch opts.Aggregate {
	case AggregatePayer, AggregateSum:
	default:
		return QueryOptions{}, fmt.Errorf("invalid aggregate %q (expected payer or sum)", opts.Aggregate)
	}

	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return QueryOptions{}, fmt.Errorf("from date must not be after to date")
	}

	if opts.MaxRows < 0 {
		return QueryOptions{}, fmt.Errorf("maxRows must not be negative")
	}

	return opts, nil
}

// ParseDate parses a YYYY-MM-DD date string. An empty string returns the zero
// time (used for optional From/To filters).
func ParseDate(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, nil
	}
	return cost.ParseDate(s)
}

// BuildHistoricalCountSQL returns SQL and bind args for the configured options. opts must be validated first.
func BuildHistoricalCountSQL(opts QueryOptions) (string, []any, error) {
	opts, err := ValidateQueryOptions(opts)
	if err != nil {
		return "", nil, err
	}

	var (
		filters []string
		args    []any
	)
	if opts.PayerAccountID != "" {
		filters = append(filters, "PAYER_ACCOUNT_ID = ?")
		args = append(args, opts.PayerAccountID)
	}
	if !opts.From.IsZero() {
		filters = append(filters, "TIMESTAMP >= ?")
		args = append(args, opts.From.Format(dateOnlyLayout))
	}
	if !opts.To.IsZero() {
		filters = append(filters, "TIMESTAMP < ?")
		args = append(args, opts.To.AddDate(0, 0, 1).Format(dateOnlyLayout))
	}

	where := ""
	if len(filters) > 0 {
		where = "WHERE " + strings.Join(filters, " AND ")
	}

	inner := fmt.Sprintf(`
WITH daily AS (
  SELECT
    RUN_ID,
    TIMESTAMP,
    PAYER_ACCOUNT_ID,
    NB_ACTIVE_ACCOUNTS,
    NB_CLOSED_ACCOUNTS,
    NB_DELETED_ACCOUNTS
  FROM %s
  %s
  QUALIFY ROW_NUMBER() OVER (
    PARTITION BY PAYER_ACCOUNT_ID, DATE(TIMESTAMP)
    ORDER BY TIMESTAMP DESC, RUN_ID DESC
  ) = 1
)`, opts.Table, where)

	switch opts.Aggregate {
	case AggregateSum:
		return strings.TrimSpace(inner + `
SELECT
  DATE(TIMESTAMP) AS snapshot_date,
  SUM(NB_ACTIVE_ACCOUNTS) AS nb_active_accounts,
  SUM(NB_CLOSED_ACCOUNTS) AS nb_closed_accounts,
  SUM(NB_DELETED_ACCOUNTS) AS nb_deleted_accounts
FROM daily
GROUP BY DATE(TIMESTAMP)
ORDER BY snapshot_date`), args, nil
	default:
		return strings.TrimSpace(inner + `
SELECT
  DATE(TIMESTAMP) AS snapshot_date,
  PAYER_ACCOUNT_ID,
  NB_ACTIVE_ACCOUNTS,
  NB_CLOSED_ACCOUNTS,
  NB_DELETED_ACCOUNTS
FROM daily
ORDER BY snapshot_date, PAYER_ACCOUNT_ID`), args, nil
	}
}

// QueryHistoricalCount runs the historical account-count query.
func QueryHistoricalCount(ctx context.Context, querier RowQuerier, opts QueryOptions) (result QueryResult, err error) {
	opts, err = ValidateQueryOptions(opts)
	if err != nil {
		return result, err
	}
	if querier == nil {
		return result, fmt.Errorf("querier is required")
	}

	sqlText, args, err := BuildHistoricalCountSQL(opts)
	if err != nil {
		return result, err
	}

	rows, err := querier.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return result, fmt.Errorf("aws accounts historical count query: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close aws accounts historical count rows: %w", closeErr)
		}
	}()

	for rows.Next() {
		if opts.MaxRows > 0 && len(result.Points) >= opts.MaxRows {
			result.Truncated = true
			break
		}
		point, scanErr := scanDailyPoint(rows, opts.Aggregate)
		if scanErr != nil {
			return result, scanErr
		}
		result.Points = append(result.Points, point)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return result, fmt.Errorf("iterate aws accounts historical count rows: %w", iterErr)
	}

	return result, nil
}

func scanDailyPoint(rows RowIterator, aggregate string) (DailyPoint, error) {
	switch aggregate {
	case AggregateSum:
		var (
			snapshotDate sql.NullTime
			active       sql.NullInt64
			closed       sql.NullInt64
			deleted      sql.NullInt64
		)
		if err := rows.Scan(&snapshotDate, &active, &closed, &deleted); err != nil {
			return DailyPoint{}, fmt.Errorf("scan sum row: %w", err)
		}
		return DailyPoint{
			Date:              formatSnapshotDate(snapshotDate),
			NBActiveAccounts:  active.Int64,
			NBClosedAccounts:  closed.Int64,
			NBDeletedAccounts: deleted.Int64,
		}, nil
	default:
		var (
			snapshotDate sql.NullTime
			payerID      sql.NullString
			active       sql.NullInt64
			closed       sql.NullInt64
			deleted      sql.NullInt64
		)
		if err := rows.Scan(&snapshotDate, &payerID, &active, &closed, &deleted); err != nil {
			return DailyPoint{}, fmt.Errorf("scan payer row: %w", err)
		}
		return DailyPoint{
			Date:              formatSnapshotDate(snapshotDate),
			PayerAccountID:    payerID.String,
			NBActiveAccounts:  active.Int64,
			NBClosedAccounts:  closed.Int64,
			NBDeletedAccounts: deleted.Int64,
		}, nil
	}
}

func formatSnapshotDate(v sql.NullTime) string {
	if !v.Valid {
		return ""
	}
	return v.Time.UTC().Format(dateOnlyLayout)
}
