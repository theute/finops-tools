// Package cost fetches and aggregates cloud cost data from provider APIs using caller-supplied credentials.
package cost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/openshift-online/finops-tools/core/parallel"
	"github.com/openshift-online/finops-tools/core/progress"
)

// Provider identifies a cloud cost data source.
type Provider string

const (
	ProviderAWS Provider = "aws"
	ProviderGCP Provider = "gcp"
)

// DefaultDays is the lookback window for account get-cost.
const DefaultDays = 30

// MetricNetAmortized is the AWS Cost Explorer metric name.
const MetricNetAmortized = "NetAmortizedCost"

// GroupBy identifies how cost results are grouped.
type GroupBy string

const (
	GroupByNone    GroupBy = ""
	GroupByService GroupBy = "service"
	GroupByAccount GroupBy = "account"
	GroupByOU      GroupBy = "ou"
)

var errProviderNotImplemented = errors.New("cost provider not implemented")

// ParseGroupBy parses a --group-by flag value (case-insensitive). Empty means no grouping.
func ParseGroupBy(s string) (GroupBy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return GroupByNone, nil
	case string(GroupByService):
		return GroupByService, nil
	case string(GroupByAccount):
		return GroupByAccount, nil
	case string(GroupByOU):
		return GroupByOU, nil
	default:
		return "", fmt.Errorf("unknown group-by %q (supported: service, account, ou)", s)
	}
}

// ParseProvider parses a provider flag value (case-insensitive).
func ParseProvider(s string) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(ProviderAWS), "":
		return ProviderAWS, nil
	case string(ProviderGCP):
		return ProviderGCP, nil
	default:
		return "", fmt.Errorf("unknown provider %q (supported: aws, gcp)", s)
	}
}

// ResolveAWSAccountNamesFunc maps specific account IDs to display names (Organizations).
type ResolveAWSAccountNamesFunc func(context.Context, aws.Config, []string) (map[string]string, error)

// AWSFetchOptions configures optional AWS-specific behavior for cost fetch.
type AWSFetchOptions struct {
	// ListAccountNames loads all organization accounts (slow on large orgs).
	// Prefer ResolveAccountNames when available.
	ListAccountNames ListAWSAccountNamesFunc
	// ResolveAccountNames looks up only the given account IDs (fast for small sets).
	ResolveAccountNames ResolveAWSAccountNamesFunc
	// AccountOUBuckets maps linked account IDs to OU rollup buckets for GroupByOU.
	// Prefer immediate parent OUs when OUHierarchy is also set (tree rollup).
	AccountOUBuckets map[string]OUBucket
	// OUHierarchy is the DFS pre-order OU tree under the selection root for GroupByOU.
	OUHierarchy []OUHierarchyNode
}

// OUBucket identifies an Organizational Unit for cost rollup.
type OUBucket struct {
	ID   string
	Name string
}

// FetchProgress reports long-running steps while fetching costs.
type FetchProgress = progress.Reporter

// CostQuery describes a cost fetch request.
type CostQuery struct {
	Provider Provider
	Accounts []AccountTarget
	Range    DateRange
	GroupBy  GroupBy
	AWSFetch *AWSFetchOptions
	Progress FetchProgress
	// Workers bounds concurrent Cost Explorer queries for multi-account fetches (0 = default).
	Workers int
}

// AccountTarget identifies an AWS account whose costs are fetched.
type AccountTarget struct {
	// AccountID is the 12-digit account ID whose costs are reported.
	AccountID string
	// PayerAccountID is set when AccountID is a linked (member) account.
	PayerAccountID string
	// AWSConfig holds authenticated payer credentials for Cost Explorer (set by the CLI).
	AWSConfig aws.Config
	// DisplayName is the AWS Organizations account name when resolved by the CLI.
	DisplayName string
	// DisplayAlias is the configured finops alias when the target was selected by alias.
	DisplayAlias string
	// ScopeAccountOnly forces a LINKED_ACCOUNT CE filter even when the target is the payer account.
	ScopeAccountOnly bool
}

// AccountDisplayName returns the preferred display name for the target,
// falling back to DisplayAlias, and finally AccountID.
func (t AccountTarget) AccountDisplayName() string {
	if name := strings.TrimSpace(t.DisplayName); name != "" {
		return name
	}
	if alias := strings.TrimSpace(t.DisplayAlias); alias != "" {
		return alias
	}
	return strings.TrimSpace(t.AccountID)
}

// CredentialsAccountID returns the account ID whose credentials are in AWSConfig.
func (t AccountTarget) CredentialsAccountID() string {
	if id := strings.TrimSpace(t.PayerAccountID); id != "" {
		return id
	}
	return strings.TrimSpace(t.AccountID)
}

// ScopeToAccount reports whether Cost Explorer should filter to AccountID only.
func (t AccountTarget) ScopeToAccount() bool {
	return t.IsLinked() || t.ScopeAccountOnly
}

// IsLinked reports whether costs are scoped to a linked (member) account.
func (t AccountTarget) IsLinked() bool {
	payer := strings.TrimSpace(t.PayerAccountID)
	return payer != "" && payer != strings.TrimSpace(t.AccountID)
}

// DailyCostItem is net amortized cost for one calendar day.
type DailyCostItem struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
}

// CostBreakdownItem is one row when costs are split by service, linked account, or OU.
type CostBreakdownItem struct {
	Service     string  `json:"service,omitempty"`
	Account     string  `json:"account,omitempty"`
	AccountName string  `json:"account_name,omitempty"`
	OUID        string  `json:"ou_id,omitempty"`
	OUName      string  `json:"ou_name,omitempty"`
	OUDepth     int     `json:"ou_depth"`
	Amount      float64 `json:"amount"`
}

// Label returns the merge/group key for this breakdown row (always the raw dimension value).
func (b CostBreakdownItem) Label(groupBy GroupBy) string {
	switch groupBy {
	case GroupByAccount:
		return b.Account
	case GroupByOU:
		if id := strings.TrimSpace(b.OUID); id != "" {
			return id
		}
		return b.OUName
	default:
		return b.Service
	}
}

// DisplayLabel returns the formatted label for output (includes account/OU ID when a name is known).
func (b CostBreakdownItem) DisplayLabel(groupBy GroupBy) string {
	switch groupBy {
	case GroupByAccount:
		if name := strings.TrimSpace(b.AccountName); name != "" && name != b.Account {
			return name + " (" + b.Account + ")"
		}
		return b.Label(groupBy)
	case GroupByOU:
		name := strings.TrimSpace(b.OUName)
		id := strings.TrimSpace(b.OUID)
		if name != "" && id != "" && name != id {
			return name + " (" + id + ")"
		}
		if name != "" {
			return name
		}
		return id
	default:
		return b.Label(groupBy)
	}
}

// CostResult is the aggregated cost summary returned to callers.
type CostResult struct {
	Provider    Provider            `json:"provider"`
	AccountName string              `json:"account_name"`
	AccountID   string              `json:"account_id"`
	Metric      string              `json:"metric"`
	GroupBy     GroupBy             `json:"group_by,omitempty"`
	StartDate   string              `json:"start_date"`
	EndDate     string              `json:"end_date"`
	Amount      float64             `json:"amount"`
	Currency    string              `json:"currency"`
	Breakdown   []CostBreakdownItem `json:"breakdown,omitempty"`
	// Linked is true when costs are scoped to linked (member) accounts rather than payers.
	Linked bool `json:"linked,omitempty"`
}

// EmptyResult is a zero-amount summary for a period when no accounts were selected.
func EmptyResult(provider Provider, dr DateRange, groupBy GroupBy) CostResult {
	endInclusive := dr.End.AddDate(0, 0, -1)
	return CostResult{
		Provider:  provider,
		Metric:    MetricNetAmortized,
		GroupBy:   groupBy,
		StartDate: formatDate(dr.Start),
		EndDate:   formatDate(endInclusive),
	}
}

// Fetch retrieves cost data for one or more accounts and returns a combined summary.
func Fetch(ctx context.Context, q CostQuery) (CostResult, error) {
	if len(q.Accounts) == 0 {
		return CostResult{}, errors.New("at least one account is required")
	}
	targets := FilterOverlappingTargets(q.Accounts)

	if _, ok := planBulkFetch(targets); ok {
		reportBulkFetchProgress(q.Progress, len(targets), q.GroupBy)
		switch q.Provider {
		case ProviderAWS, "":
			opts := fetchAWSOptions{Now: time.Now()}
			if q.AWSFetch != nil {
				opts.ListAccountNames = q.AWSFetch.ListAccountNames
				opts.ResolveAccountNames = q.AWSFetch.ResolveAccountNames
			}
			return fetchAWSNetAmortizedBulk(ctx, q, targets, opts)
		case ProviderGCP:
			return CostResult{}, fmt.Errorf("%w: gcp", errProviderNotImplemented)
		default:
			return CostResult{}, fmt.Errorf("unknown provider %q", q.Provider)
		}
	}

	// For GroupByOU, fetch per-account LINKED_ACCOUNT rows and roll up once after
	// merge. Rolling up before merge flattens the tree (loses depth/DFS order) and
	// can disorder siblings when multiple payers/orgs are combined.
	fetchGroupBy := q.GroupBy
	deferOURollup := q.GroupBy == GroupByOU
	if deferOURollup {
		fetchGroupBy = GroupByAccount
	}

	results := make([]CostResult, len(targets))
	err := parallel.ForEach(ctx, q.Workers, len(targets), func(ctx context.Context, i int) error {
		acct := targets[i]
		reportFetchProgress(q.Progress, acct, i+1, len(targets), q.GroupBy)
		single := q
		single.Accounts = []AccountTarget{acct}
		single.GroupBy = fetchGroupBy
		if deferOURollup && single.AWSFetch != nil {
			cp := *single.AWSFetch
			cp.AccountOUBuckets = nil
			cp.OUHierarchy = nil
			single.AWSFetch = &cp
		}

		var r CostResult
		var err error
		switch q.Provider {
		case ProviderAWS, "":
			r, err = fetchAWSNetAmortized(ctx, single)
		case ProviderGCP:
			err = fmt.Errorf("%w: gcp", errProviderNotImplemented)
		default:
			err = fmt.Errorf("unknown provider %q", q.Provider)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", acct.AccountID, err)
		}
		results[i] = r
		return nil
	})
	if err != nil {
		return CostResult{}, err
	}
	merged, err := MergeResults(results)
	if err != nil {
		return CostResult{}, err
	}
	if deferOURollup {
		merged.GroupBy = GroupByOU
		merged.Breakdown = rollupOUBreakdown(merged.Breakdown, q.AWSFetch)
	}
	return merged, nil
}

// FetchDaily retrieves per-day net amortized costs for one or more accounts.
func FetchDaily(ctx context.Context, q CostQuery) ([]DailyCostItem, string, error) {
	if len(q.Accounts) == 0 {
		return nil, "", errors.New("at least one account is required")
	}
	switch q.Provider {
	case ProviderAWS, "":
		targets := FilterOverlappingTargets(q.Accounts)
		if _, ok := planBulkFetch(targets); ok {
			reportBulkFetchProgress(q.Progress, len(targets), GroupByNone)
			opts := fetchAWSOptions{Now: time.Now()}
			return fetchAWSDailyNetAmortizedBulk(ctx, q, targets, opts)
		}
		series := make([][]DailyCostItem, len(targets))
		currencies := make([]string, len(targets))
		err := parallel.ForEach(ctx, q.Workers, len(targets), func(ctx context.Context, i int) error {
			acct := targets[i]
			reportFetchProgress(q.Progress, acct, i+1, len(targets), GroupByNone)
			single := q
			single.Accounts = []AccountTarget{acct}
			daily, cur, err := fetchAWSDailyNetAmortized(ctx, single)
			if err != nil {
				return fmt.Errorf("%s: %w", acct.AccountID, err)
			}
			series[i] = daily
			currencies[i] = cur
			return nil
		})
		if err != nil {
			return nil, "", err
		}
		var currency string
		for _, cur := range currencies {
			if currency == "" {
				currency = cur
			} else if cur != "" && cur != currency {
				return nil, "", fmt.Errorf("cannot merge accounts with different currencies (%s vs %s)", currency, cur)
			}
		}
		return MergeDaily(series), currency, nil
	case ProviderGCP:
		return nil, "", fmt.Errorf("%w: gcp", errProviderNotImplemented)
	default:
		return nil, "", fmt.Errorf("unknown provider %q", q.Provider)
	}
}

func reportBulkFetchProgress(progress FetchProgress, accountCount int, groupBy GroupBy) {
	if progress == nil || accountCount <= 1 {
		return
	}
	switch groupBy {
	case GroupByService:
		progress.Step(fmt.Sprintf("Fetching costs by service for %d account(s) in batched Cost Explorer queries…", accountCount))
	case GroupByOU:
		progress.Step(fmt.Sprintf("Fetching costs by OU for %d account(s) in batched Cost Explorer queries…", accountCount))
	default:
		progress.Step(fmt.Sprintf("Fetching costs for %d account(s) in one bulk Cost Explorer query…", accountCount))
	}
}

func reportFetchProgress(progress FetchProgress, acct AccountTarget, index, total int, groupBy GroupBy) {
	if progress == nil || total <= 1 || !shouldReportFetchProgress(index, total) {
		return
	}
	label := targetProgressLabel(acct)
	switch groupBy {
	case GroupByService:
		progress.Step(fmt.Sprintf("Fetching costs by service for %s [%d/%d]…", label, index, total))
	case GroupByAccount:
		progress.Step(fmt.Sprintf("Fetching costs for %s [%d/%d]…", label, index, total))
	default:
		progress.Step(fmt.Sprintf("Fetching costs for %s [%d/%d]…", label, index, total))
	}
}

func shouldReportFetchProgress(index, total int) bool {
	if total <= 1 {
		return false
	}
	if index == 1 || index == total {
		return true
	}
	if total <= 10 {
		return true
	}
	return index%25 == 0
}

func targetProgressLabel(acct AccountTarget) string {
	accountID := strings.TrimSpace(acct.AccountID)
	name := acct.AccountDisplayName()
	if name != accountID {
		return fmt.Sprintf("%s (%s)", name, accountID)
	}
	return accountID
}
