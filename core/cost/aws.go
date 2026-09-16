// aws.go calls AWS Cost Explorer to fetch NetAmortizedCost with optional group-by service or linked account.
package cost

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/openshift-online/finops-tools/core/apilog"
)

// CostExplorerRegion is the AWS region Cost Explorer's API is served from
// (Cost Explorer is a global service reachable only via us-east-1).
const CostExplorerRegion = "us-east-1"

// CostExplorerAPI is the subset of the CE client used for cost fetch (mockable).
type CostExplorerAPI interface {
	GetCostAndUsage(
		ctx context.Context,
		params *costexplorer.GetCostAndUsageInput,
		optFns ...func(*costexplorer.Options),
	) (*costexplorer.GetCostAndUsageOutput, error)
}

// ListAWSAccountNamesFunc maps organization account IDs to display names for group-by-account output.
type ListAWSAccountNamesFunc func(context.Context, aws.Config) (map[string]string, error)

type fetchAWSOptions struct {
	Now                 time.Time
	NewCostExplorer     func(aws.Config) CostExplorerAPI
	ListAccountNames    ListAWSAccountNamesFunc
	ResolveAccountNames ResolveAWSAccountNamesFunc
}

func fetchAWSNetAmortized(ctx context.Context, q CostQuery) (CostResult, error) {
	opts := fetchAWSOptions{
		Now:             time.Now(),
		NewCostExplorer: defaultCostExplorerFactory(),
	}
	if q.AWSFetch != nil {
		opts.ListAccountNames = q.AWSFetch.ListAccountNames
		opts.ResolveAccountNames = q.AWSFetch.ResolveAccountNames
	}
	return fetchAWSNetAmortizedWith(ctx, q, opts)
}

func fetchAWSNetAmortizedWith(ctx context.Context, q CostQuery, opts fetchAWSOptions) (CostResult, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.NewCostExplorer == nil {
		opts.NewCostExplorer = defaultCostExplorerFactory()
	}

	acct := q.Accounts[0]
	accountID := acct.AccountID
	cfg := acct.AWSConfig
	if cfg.Region == "" {
		cfg.Region = CostExplorerRegion
	}

	dr := EffectiveRange(q, opts.Now)
	ce := opts.NewCostExplorer(cfg)
	filter := LinkedAccountFilter(accountID, acct.ScopeToAccount())

	var (
		amount    float64
		currency  string
		breakdown []CostBreakdownItem
		fetchErr  error
	)
	switch q.GroupBy {
	case GroupByService:
		amount, currency, breakdown, fetchErr = sumNetAmortizedGrouped(ctx, ce, dr, "SERVICE", GroupByService, filter)
	case GroupByAccount, GroupByOU:
		amount, currency, breakdown, fetchErr = sumNetAmortizedGrouped(ctx, ce, dr, "LINKED_ACCOUNT", GroupByAccount, filter)
	case GroupByNone:
		amount, currency, fetchErr = sumNetAmortizedCost(ctx, ce, dr, filter)
	default:
		return CostResult{}, fmt.Errorf("unknown group-by %q", q.GroupBy)
	}
	if fetchErr != nil {
		return CostResult{}, fetchErr
	}

	if q.GroupBy == GroupByAccount || q.GroupBy == GroupByOU {
		breakdown = applyAWSAccountNames(ctx, cfg, breakdown, opts)
	}
	if q.GroupBy == GroupByOU {
		breakdown = rollupOUBreakdown(breakdown, q.AWSFetch)
	}

	return CostResult{
		Provider:    ProviderAWS,
		AccountName: acct.AccountDisplayName(),
		AccountID:   accountID,
		Metric:      MetricNetAmortized,
		GroupBy:     q.GroupBy,
		StartDate:   FormatDate(dr.Start),
		EndDate:     FormatDate(dr.End.AddDate(0, 0, -1)),
		Amount:      amount,
		Currency:    currency,
		Breakdown:   breakdown,
		Linked:      acct.IsLinked(),
	}, nil
}

func applyAWSAccountNames(
	ctx context.Context,
	cfg aws.Config,
	breakdown []CostBreakdownItem,
	opts fetchAWSOptions,
) []CostBreakdownItem {
	if len(breakdown) == 0 {
		return breakdown
	}
	names, err := lookupAWSAccountNames(ctx, cfg, breakdownAccountIDs(breakdown), opts)
	if err != nil || len(names) == 0 {
		return breakdown
	}
	for i := range breakdown {
		if name, ok := names[breakdown[i].Account]; ok {
			breakdown[i].AccountName = name
		}
	}
	return breakdown
}

// UniqueAccountIDs deduplicates and trims account IDs from a given slice.
func UniqueAccountIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func breakdownAccountIDs(breakdown []CostBreakdownItem) []string {
	rawIDs := make([]string, 0, len(breakdown))
	for _, item := range breakdown {
		rawIDs = append(rawIDs, item.Account)
	}
	return UniqueAccountIDs(rawIDs)
}

func lookupAWSAccountNames(
	ctx context.Context,
	cfg aws.Config,
	accountIDs []string,
	opts fetchAWSOptions,
) (map[string]string, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	if opts.ResolveAccountNames != nil {
		return opts.ResolveAccountNames(ctx, cfg, accountIDs)
	}
	if opts.ListAccountNames != nil {
		return opts.ListAccountNames(ctx, cfg)
	}
	return nil, nil
}

// LinkedAccountFilter builds a Cost Explorer filter expression scoping results
// to a single linked account, or nil when the query should be unscoped.
func LinkedAccountFilter(accountID string, linked bool) *types.Expression {
	if !linked {
		return nil
	}
	return &types.Expression{
		Dimensions: &types.DimensionValues{
			Key:    types.DimensionLinkedAccount,
			Values: []string{accountID},
		},
	}
}

func sumNetAmortizedCost(ctx context.Context, ce CostExplorerAPI, dr DateRange, filter *types.Expression) (float64, string, error) {
	var (
		total    float64
		currency = "USD"
		token    *string
	)

	for {
		out, err := ce.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &types.DateInterval{
				Start: aws.String(FormatDate(dr.Start)),
				End:   aws.String(FormatDate(dr.End)),
			},
			Granularity:   types.GranularityDaily,
			Metrics:       []string{MetricNetAmortized},
			Filter:        filter,
			NextPageToken: token,
		})
		if err != nil {
			return 0, "", fmt.Errorf("cost explorer GetCostAndUsage: %w", err)
		}

		for _, row := range out.ResultsByTime {
			m, ok := row.Total[MetricNetAmortized]
			if !ok {
				continue
			}
			amt, err := strconv.ParseFloat(aws.ToString(m.Amount), 64)
			if err != nil {
				return 0, "", fmt.Errorf("parse %s amount: %w", MetricNetAmortized, err)
			}
			total += amt
			if u := aws.ToString(m.Unit); u != "" {
				currency = u
			}
		}

		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}

	return total, currency, nil
}

func sumNetAmortizedGrouped(
	ctx context.Context,
	ce CostExplorerAPI,
	dr DateRange,
	dimension string,
	groupBy GroupBy,
	filter *types.Expression,
) (float64, string, []CostBreakdownItem, error) {
	byKey := make(map[string]float64)
	currency := "USD"
	var token *string

	ceGroupDefs := []types.GroupDefinition{{
		Type: types.GroupDefinitionTypeDimension,
		Key:  aws.String(dimension),
	}}

	for {
		out, err := ce.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &types.DateInterval{
				Start: aws.String(FormatDate(dr.Start)),
				End:   aws.String(FormatDate(dr.End)),
			},
			Granularity:   types.GranularityDaily,
			Metrics:       []string{MetricNetAmortized},
			GroupBy:       ceGroupDefs,
			Filter:        filter,
			NextPageToken: token,
		})
		if err != nil {
			return 0, "", nil, fmt.Errorf("cost explorer GetCostAndUsage: %w", err)
		}

		for _, row := range out.ResultsByTime {
			for _, g := range row.Groups {
				if len(g.Keys) == 0 {
					continue
				}
				key := g.Keys[0]
				m, ok := g.Metrics[MetricNetAmortized]
				if !ok {
					continue
				}
				amt, err := strconv.ParseFloat(aws.ToString(m.Amount), 64)
				if err != nil {
					return 0, "", nil, fmt.Errorf("parse %s amount for %q: %w", MetricNetAmortized, key, err)
				}
				byKey[key] += amt
				if u := aws.ToString(m.Unit); u != "" {
					currency = u
				}
			}
		}

		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}

	breakdown := make([]CostBreakdownItem, 0, len(byKey))
	var total float64
	for key, amt := range byKey {
		if amt == 0 {
			continue
		}
		item := CostBreakdownItem{Amount: amt}
		switch groupBy {
		case GroupByAccount:
			item.Account = key
		default:
			item.Service = key
		}
		breakdown = append(breakdown, item)
		total += amt
	}
	sort.Slice(breakdown, func(i, j int) bool {
		return breakdown[i].Amount > breakdown[j].Amount
	})

	return total, currency, breakdown, nil
}

func fetchAWSDailyNetAmortized(ctx context.Context, q CostQuery) ([]DailyCostItem, string, error) {
	opts := fetchAWSOptions{
		Now:             time.Now(),
		NewCostExplorer: defaultCostExplorerFactory(),
	}
	return fetchAWSDailyNetAmortizedWith(ctx, q, opts)
}

func fetchAWSDailyNetAmortizedWith(ctx context.Context, q CostQuery, opts fetchAWSOptions) ([]DailyCostItem, string, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.NewCostExplorer == nil {
		opts.NewCostExplorer = defaultCostExplorerFactory()
	}

	acct := q.Accounts[0]
	cfg := acct.AWSConfig
	if cfg.Region == "" {
		cfg.Region = CostExplorerRegion
	}

	dr := EffectiveRange(q, opts.Now)
	ce := opts.NewCostExplorer(cfg)
	filter := LinkedAccountFilter(acct.AccountID, acct.ScopeToAccount())
	return sumNetAmortizedDaily(ctx, ce, dr, filter)
}

func defaultCostExplorerFactory() func(aws.Config) CostExplorerAPI {
	return func(cfg aws.Config) CostExplorerAPI {
		inner := costexplorer.NewFromConfig(cfg, func(o *costexplorer.Options) {
			o.Region = CostExplorerRegion
		})
		return apilog.WrapGetCostAndUsage(inner)
	}
}

func sumNetAmortizedDaily(ctx context.Context, ce CostExplorerAPI, dr DateRange, filter *types.Expression) ([]DailyCostItem, string, error) {
	byDate := make(map[string]float64)
	currency := "USD"
	var token *string

	for {
		out, err := ce.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &types.DateInterval{
				Start: aws.String(FormatDate(dr.Start)),
				End:   aws.String(FormatDate(dr.End)),
			},
			Granularity:   types.GranularityDaily,
			Metrics:       []string{MetricNetAmortized},
			Filter:        filter,
			NextPageToken: token,
		})
		if err != nil {
			return nil, "", fmt.Errorf("cost explorer GetCostAndUsage: %w", err)
		}

		for _, row := range out.ResultsByTime {
			date := strings.TrimSpace(aws.ToString(row.TimePeriod.Start))
			if date == "" {
				continue
			}
			m, ok := row.Total[MetricNetAmortized]
			if !ok {
				continue
			}
			amt, err := strconv.ParseFloat(aws.ToString(m.Amount), 64)
			if err != nil {
				return nil, "", fmt.Errorf("parse %s amount for %s: %w", MetricNetAmortized, date, err)
			}
			byDate[date] += amt
			if u := aws.ToString(m.Unit); u != "" {
				currency = u
			}
		}

		if out.NextPageToken == nil || *out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}

	daily := make([]DailyCostItem, 0, len(byDate))
	for date, amt := range byDate {
		if amt == 0 {
			continue
		}
		daily = append(daily, DailyCostItem{Date: date, Amount: amt})
	}
	sort.Slice(daily, func(i, j int) bool {
		return daily[i].Date < daily[j].Date
	})
	return daily, currency, nil
}
