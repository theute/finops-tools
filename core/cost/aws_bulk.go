// aws_bulk.go fetches costs for many linked accounts under one payer in fewer Cost Explorer calls.
package cost

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/openshift-online/finops-tools/core/parallel"
)

const (
	bulkFetchMinAccounts         = 2
	linkedAccountFilterBatchSize = 100
)

type bulkFetchPlan struct {
	credTarget AccountTarget
	accountIDs map[string]struct{}
}

func planBulkFetch(targets []AccountTarget) (bulkFetchPlan, bool) {
	if len(targets) < bulkFetchMinAccounts {
		return bulkFetchPlan{}, false
	}

	credID := targets[0].CredentialsAccountID()
	accountIDs := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		if t.CredentialsAccountID() != credID || !t.ScopeToAccount() {
			return bulkFetchPlan{}, false
		}
		id := strings.TrimSpace(t.AccountID)
		if id == "" {
			return bulkFetchPlan{}, false
		}
		accountIDs[id] = struct{}{}
	}

	return bulkFetchPlan{
		credTarget: targets[0],
		accountIDs: accountIDs,
	}, true
}

func fetchAWSNetAmortizedBulk(ctx context.Context, q CostQuery, targets []AccountTarget, opts fetchAWSOptions) (CostResult, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.NewCostExplorer == nil {
		opts.NewCostExplorer = defaultCostExplorerFactory()
	}

	plan, ok := planBulkFetch(targets)
	if !ok {
		return CostResult{}, errors.New("bulk fetch plan unavailable")
	}

	cfg := plan.credTarget.AWSConfig
	if cfg.Region == "" {
		cfg.Region = CostExplorerRegion
	}
	dr := EffectiveRange(q, opts.Now)
	ce := opts.NewCostExplorer(cfg)
	displayNames := targetDisplayNames(targets)

	switch q.GroupBy {
	case GroupByService:
		return fetchAWSNetAmortizedBulkByService(ctx, ce, q, targets, plan, dr)
	case GroupByAccount, GroupByNone, GroupByOU:
		ids := sortedAccountIDs(plan.accountIDs)
		batches := batchStrings(ids, linkedAccountFilterBatchSize)
		type accountBatchResult struct {
			breakdown []CostBreakdownItem
			currency  string
		}
		batchResults := make([]accountBatchResult, len(batches))
		err := parallel.ForEach(ctx, q.Workers, len(batches), func(ctx context.Context, i int) error {
			filter := linkedAccountsFilter(batches[i])
			_, cur, batchBreakdown, err := sumNetAmortizedGrouped(ctx, ce, dr, "LINKED_ACCOUNT", GroupByAccount, filter)
			if err != nil {
				return err
			}
			batchResults[i] = accountBatchResult{breakdown: batchBreakdown, currency: cur}
			return nil
		})
		if err != nil {
			return CostResult{}, err
		}

		var (
			breakdown []CostBreakdownItem
			currency  string
		)
		for _, br := range batchResults {
			if currency == "" {
				currency = br.currency
			} else if br.currency != "" && br.currency != currency {
				return CostResult{}, fmt.Errorf("cannot merge account batches with different currencies (%s vs %s)", currency, br.currency)
			}
			breakdown = append(breakdown, br.breakdown...)
		}
		breakdown = filterBreakdownAccounts(breakdown, plan.accountIDs)
		breakdown = applyTargetDisplayNames(breakdown, displayNames)

		var total float64
		for _, item := range breakdown {
			total += item.Amount
		}

		out := bulkMergedCostResult(targets, q, dr, total, currency, plan.credTarget)
		if q.GroupBy == GroupByAccount {
			out.Breakdown = breakdown
		}
		if q.GroupBy == GroupByOU {
			out.Breakdown = rollupOUBreakdown(breakdown, q.AWSFetch)
		}
		return out, nil
	default:
		return CostResult{}, fmt.Errorf("unknown group-by %q", q.GroupBy)
	}
}

func fetchAWSNetAmortizedBulkByService(
	ctx context.Context,
	ce CostExplorerAPI,
	q CostQuery,
	targets []AccountTarget,
	plan bulkFetchPlan,
	dr DateRange,
) (CostResult, error) {
	ids := sortedAccountIDs(plan.accountIDs)
	batches := batchStrings(ids, linkedAccountFilterBatchSize)
	type serviceBatchResult struct {
		byService map[string]float64
		currency  string
	}
	batchResults := make([]serviceBatchResult, len(batches))
	err := parallel.ForEach(ctx, q.Workers, len(batches), func(ctx context.Context, i int) error {
		filter := linkedAccountsFilter(batches[i])
		_, cur, breakdown, err := sumNetAmortizedGrouped(ctx, ce, dr, "SERVICE", GroupByService, filter)
		if err != nil {
			return err
		}
		byService := make(map[string]float64)
		for _, item := range breakdown {
			byService[item.Service] += item.Amount
		}
		batchResults[i] = serviceBatchResult{byService: byService, currency: cur}
		return nil
	})
	if err != nil {
		return CostResult{}, err
	}

	byService := make(map[string]float64)
	var currency string
	for _, br := range batchResults {
		if currency == "" {
			currency = br.currency
		} else if br.currency != "" && br.currency != currency {
			return CostResult{}, fmt.Errorf("cannot merge service batches with different currencies (%s vs %s)", currency, br.currency)
		}
		for service, amt := range br.byService {
			byService[service] += amt
		}
	}

	breakdown := make([]CostBreakdownItem, 0, len(byService))
	var total float64
	for service, amt := range byService {
		if amt == 0 {
			continue
		}
		breakdown = append(breakdown, CostBreakdownItem{Service: service, Amount: amt})
		total += amt
	}
	sort.Slice(breakdown, func(i, j int) bool {
		return breakdown[i].Amount > breakdown[j].Amount
	})

	out := bulkMergedCostResult(targets, q, dr, total, currency, plan.credTarget)
	out.Breakdown = breakdown
	return out, nil
}

func fetchAWSDailyNetAmortizedBulk(ctx context.Context, q CostQuery, targets []AccountTarget, opts fetchAWSOptions) ([]DailyCostItem, string, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.NewCostExplorer == nil {
		opts.NewCostExplorer = defaultCostExplorerFactory()
	}

	plan, ok := planBulkFetch(targets)
	if !ok {
		return nil, "", errors.New("bulk fetch plan unavailable")
	}

	cfg := plan.credTarget.AWSConfig
	if cfg.Region == "" {
		cfg.Region = CostExplorerRegion
	}
	dr := EffectiveRange(q, opts.Now)
	ce := opts.NewCostExplorer(cfg)

	ids := sortedAccountIDs(plan.accountIDs)
	batches := batchStrings(ids, linkedAccountFilterBatchSize)
	type dailyBatchResult struct {
		daily    []DailyCostItem
		currency string
	}
	batchResults := make([]dailyBatchResult, len(batches))
	err := parallel.ForEach(ctx, q.Workers, len(batches), func(ctx context.Context, i int) error {
		filter := linkedAccountsFilter(batches[i])
		daily, cur, err := sumNetAmortizedDaily(ctx, ce, dr, filter)
		if err != nil {
			return err
		}
		batchResults[i] = dailyBatchResult{daily: daily, currency: cur}
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	series := make([][]DailyCostItem, len(batchResults))
	var currency string
	for i, br := range batchResults {
		if currency == "" {
			currency = br.currency
		} else if br.currency != "" && br.currency != currency {
			return nil, "", fmt.Errorf("cannot merge daily batches with different currencies (%s vs %s)", currency, br.currency)
		}
		series[i] = br.daily
	}

	return MergeDaily(series), currency, nil
}

func bulkMergedCostResult(targets []AccountTarget, q CostQuery, dr DateRange, amount float64, currency string, credTarget AccountTarget) CostResult {
	names := make([]string, len(targets))
	ids := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.AccountDisplayName()
		ids[i] = t.AccountID
	}

	return CostResult{
		Provider:    ProviderAWS,
		AccountName: strings.Join(names, ", "),
		AccountID:   strings.Join(ids, ", "),
		Metric:      MetricNetAmortized,
		GroupBy:     q.GroupBy,
		StartDate:   FormatDate(dr.Start),
		EndDate:     FormatDate(dr.End.AddDate(0, 0, -1)),
		Amount:      amount,
		Currency:    currency,
		Linked:      true,
	}
}

func linkedAccountsFilter(accountIDs []string) *types.Expression {
	if len(accountIDs) == 0 {
		return nil
	}
	if len(accountIDs) == 1 {
		return LinkedAccountFilter(accountIDs[0], true)
	}
	return &types.Expression{
		Dimensions: &types.DimensionValues{
			Key:    types.DimensionLinkedAccount,
			Values: accountIDs,
		},
	}
}

func filterBreakdownAccounts(breakdown []CostBreakdownItem, wanted map[string]struct{}) []CostBreakdownItem {
	out := make([]CostBreakdownItem, 0, len(wanted))
	for _, item := range breakdown {
		if _, ok := wanted[item.Account]; ok {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Amount > out[j].Amount
	})
	return out
}

func targetDisplayNames(targets []AccountTarget) map[string]string {
	names := make(map[string]string, len(targets))
	for _, t := range targets {
		if name := t.AccountDisplayName(); name != "" {
			names[t.AccountID] = name
		}
	}
	return names
}

func applyTargetDisplayNames(breakdown []CostBreakdownItem, names map[string]string) []CostBreakdownItem {
	for i := range breakdown {
		if name, ok := names[breakdown[i].Account]; ok {
			breakdown[i].AccountName = name
		}
	}
	return breakdown
}

func sortedAccountIDs(wanted map[string]struct{}) []string {
	ids := make([]string, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func batchStrings(values []string, size int) [][]string {
	if size <= 0 || len(values) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(values)+size-1)/size)
	for i := 0; i < len(values); i += size {
		end := i + size
		if end > len(values) {
			end = len(values)
		}
		out = append(out, values[i:end])
	}
	return out
}
