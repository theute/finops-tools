package cost

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/openshift-online/finops-tools/core/parallel"
)

const defaultTopServices = 10

// MonthlyCostPoint is net amortized cost for one calendar month (YYYY-MM).
type MonthlyCostPoint struct {
	Month  string  `json:"month"`
	Amount float64 `json:"amount"`
}

// AccountMonthlyCosts holds monthly cost history and top services for one account.
//
// Error means the monthly Cost Explorer query failed; Months/Total are then empty.
// TopServicesError means the month series succeeded but the last-month service
// breakdown failed; Months/Total are still populated.
type AccountMonthlyCosts struct {
	AccountID        string              `json:"account_id"`
	Currency         string              `json:"currency"`
	Months           []MonthlyCostPoint  `json:"months"`
	Total            float64             `json:"total"`
	TopServices      []CostBreakdownItem `json:"top_services,omitempty"`
	TopServicesError string              `json:"top_services_error,omitempty"`
	Error            string              `json:"error,omitempty"`
}

// FetchMonthly retrieves monthly net amortized costs for each account in parallel.
// Unlike Fetch, it does not drop linked accounts when their payer is also requested:
// each target is billed as its own account review series.
//
// A Cost Explorer failure for one account is stored on that result's Error field
// and does not abort the batch. Context cancellation or deadline still returns an error.
func FetchMonthly(ctx context.Context, q CostQuery) ([]AccountMonthlyCosts, error) {
	if len(q.Accounts) == 0 {
		return nil, fmt.Errorf("at least one account is required")
	}
	opts := fetchAWSOptions{
		Now:             time.Now(),
		NewCostExplorer: defaultCostExplorerFactory(),
	}
	if q.AWSFetch != nil {
		opts.ListAccountNames = q.AWSFetch.ListAccountNames
		opts.ResolveAccountNames = q.AWSFetch.ResolveAccountNames
	}
	return fetchMonthlyWith(ctx, q, opts)
}

func fetchMonthlyWith(ctx context.Context, q CostQuery, opts fetchAWSOptions) ([]AccountMonthlyCosts, error) {
	targets := q.Accounts
	if q.Progress != nil && len(targets) > 1 {
		q.Progress.Step(fmt.Sprintf("Fetching monthly costs for %d account(s)…", len(targets)))
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	results := make([]AccountMonthlyCosts, len(targets))
	err := parallel.ForEach(ctx, q.Workers, len(targets), func(ctx context.Context, i int) error {
		acct := targets[i]
		reportFetchProgress(q.Progress, acct, i+1, len(targets), GroupByNone)
		monthly, err := fetchAccountMonthlyWith(ctx, acct, q.Range, opts)
		if err != nil {
			if isContextAbort(err) {
				return err
			}
			results[i] = AccountMonthlyCosts{
				AccountID: acct.AccountID,
				Error:     err.Error(),
			}
			return nil
		}
		results[i] = monthly
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func fetchAccountMonthlyWith(ctx context.Context, acct AccountTarget, dr DateRange, opts fetchAWSOptions) (AccountMonthlyCosts, error) {
	cfg := acct.AWSConfig
	if cfg.Region == "" {
		cfg.Region = CostExplorerRegion
	}
	ce := opts.NewCostExplorer(cfg)
	filter := LinkedAccountFilter(acct.AccountID, acct.ScopeToAccount())
	ceDR := monthlyCERange(dr)

	months, currency, total, err := sumNetAmortizedMonthly(ctx, ce, ceDR, filter)
	if err != nil {
		return AccountMonthlyCosts{}, err
	}

	out := AccountMonthlyCosts{
		AccountID: acct.AccountID,
		Currency:  currency,
		Months:    months,
		Total:     total,
	}
	topServices, err := fetchTopServicesLastMonth(ctx, ce, dr, filter, defaultTopServices)
	if err != nil {
		if isContextAbort(err) {
			return AccountMonthlyCosts{}, err
		}
		// Keep the month series; emails can still show totals without a service breakdown.
		out.TopServicesError = err.Error()
		return out, nil
	}
	out.TopServices = topServices
	return out, nil
}

func isContextAbort(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// monthlyCERange aligns a date range to calendar month boundaries for CE monthly granularity.
func monthlyCERange(dr DateRange) DateRange {
	start := time.Date(dr.Start.Year(), dr.Start.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastIncluded := dr.End.AddDate(0, 0, -1)
	end := time.Date(lastIncluded.Year(), lastIncluded.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	if end.After(dr.End) {
		end = dr.End
	}
	return DateRange{Start: start, End: end}
}

func sumNetAmortizedMonthly(
	ctx context.Context,
	ce CostExplorerAPI,
	dr DateRange,
	filter *types.Expression,
) ([]MonthlyCostPoint, string, float64, error) {
	byMonth := make(map[string]float64)
	currency := "USD"
	var token *string

	for {
		out, err := ce.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &types.DateInterval{
				Start: aws.String(formatDate(dr.Start)),
				End:   aws.String(formatDate(dr.End)),
			},
			Granularity:   types.GranularityMonthly,
			Metrics:       []string{MetricNetAmortized},
			Filter:        filter,
			NextPageToken: token,
		})
		if err != nil {
			return nil, "", 0, fmt.Errorf("cost explorer GetCostAndUsage: %w", err)
		}

		for _, row := range out.ResultsByTime {
			month := monthKeyFromPeriod(row.TimePeriod)
			if month == "" {
				continue
			}
			m, ok := row.Total[MetricNetAmortized]
			if !ok {
				continue
			}
			amt, err := strconv.ParseFloat(aws.ToString(m.Amount), 64)
			if err != nil {
				return nil, "", 0, fmt.Errorf("parse %s amount for %s: %w", MetricNetAmortized, month, err)
			}
			byMonth[month] += amt
			if u := aws.ToString(m.Unit); u != "" {
				currency = u
			}
		}

		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}

	months := make([]MonthlyCostPoint, 0, len(byMonth))
	var total float64
	for month, amt := range byMonth {
		// Keep $0 months so the series matches the Cost Explorer window
		// (idle months are real data, not missing metrics).
		months = append(months, MonthlyCostPoint{Month: month, Amount: amt})
		total += amt
	}
	sort.Slice(months, func(i, j int) bool {
		return months[i].Month < months[j].Month
	})
	return months, currency, total, nil
}

func monthKeyFromPeriod(tp *types.DateInterval) string {
	if tp == nil {
		return ""
	}
	start := strings.TrimSpace(aws.ToString(tp.Start))
	if len(start) >= 7 {
		return start[:7]
	}
	return start
}

// fetchTopServicesLastMonth groups net amortized cost by SERVICE for the last
// complete calendar month in dr (not a partial current month).
func fetchTopServicesLastMonth(
	ctx context.Context,
	ce CostExplorerAPI,
	dr DateRange,
	filter *types.Expression,
	limit int,
) ([]CostBreakdownItem, error) {
	lastMonth := lastCompleteMonthRange(dr)
	if lastMonth.IsZero() {
		return nil, nil
	}
	_, _, breakdown, err := sumNetAmortizedGrouped(ctx, ce, lastMonth, "SERVICE", GroupByService, filter)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(breakdown) > limit {
		breakdown = breakdown[:limit]
	}
	return breakdown, nil
}

// lastCompleteMonthRange returns the last calendar month fully contained in dr
// (exclusive End). A mid-month End does not count the current month as complete,
// so top-services figures are not a partial month-to-date.
func lastCompleteMonthRange(dr DateRange) DateRange {
	if dr.IsZero() || !dr.End.After(dr.Start) {
		return DateRange{}
	}
	lastIncluded := dr.End.AddDate(0, 0, -1)
	start := time.Date(lastIncluded.Year(), lastIncluded.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	if dr.End.Before(end) {
		start = start.AddDate(0, -1, 0)
		end = start.AddDate(0, 1, 0)
	}
	if start.Before(dr.Start) {
		return DateRange{}
	}
	return DateRange{Start: start, End: end}
}
