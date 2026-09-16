// Package savingsplans fetches Savings Plans coverage and utilization from AWS Cost Explorer.
package savingsplans

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/openshift-online/finops-tools/core/apilog"
	"github.com/openshift-online/finops-tools/core/cost"
)

// SavingsPlansAPI is the subset of the CE client used for savings plans fetch (mockable).
type SavingsPlansAPI interface {
	GetSavingsPlansCoverage(
		ctx context.Context,
		params *costexplorer.GetSavingsPlansCoverageInput,
		optFns ...func(*costexplorer.Options),
	) (*costexplorer.GetSavingsPlansCoverageOutput, error)

	GetSavingsPlansUtilization(
		ctx context.Context,
		params *costexplorer.GetSavingsPlansUtilizationInput,
		optFns ...func(*costexplorer.Options),
	) (*costexplorer.GetSavingsPlansUtilizationOutput, error)
}

// MonthlyMetric holds a savings plan percentage metric for one calendar month.
type MonthlyMetric struct {
	Month      string  // YYYY-MM
	Percentage float64 // 0–100
}

// PeriodAverage is AWS-reported coverage or utilization for the full requested period.
type PeriodAverage struct {
	Percentage float64 // 0–100
	OK         bool
}

// MonthlySavings holds net savings from Savings Plans for one calendar month.
type MonthlySavings struct {
	Month                  string  // YYYY-MM
	NetSavings             float64 // absolute savings vs on-demand
	OnDemandCostEquivalent float64
}

// SavingsPercentage returns net savings as a percentage of on-demand equivalent (0–100).
func (s MonthlySavings) SavingsPercentage() float64 {
	return savingsPercentage(s.NetSavings, s.OnDemandCostEquivalent)
}

// PeriodSavings is AWS-reported net savings for the full requested period.
type PeriodSavings struct {
	NetSavings             float64
	OnDemandCostEquivalent float64
	OK                     bool
}

// SavingsPercentage returns net savings as a percentage of on-demand equivalent (0–100).
func (s PeriodSavings) SavingsPercentage() float64 {
	return savingsPercentage(s.NetSavings, s.OnDemandCostEquivalent)
}

// AccountReport holds Savings Plans coverage and utilization for one account.
type AccountReport struct {
	AccountID            string
	AccountName          string
	IsLinked             bool
	Coverage             []MonthlyMetric
	CoverageAverage      PeriodAverage
	Utilization          []MonthlyMetric
	UtilizationAverage   PeriodAverage
	Savings              []MonthlySavings
	SavingsTotal         PeriodSavings
}

// Report holds Savings Plans coverage and utilization ready for HTML rendering.
type Report struct {
	GeneratedAt time.Time
	StartDate   string
	EndDate     string
	Accounts    []AccountReport
}

// Build fetches Savings Plans coverage and utilization from AWS Cost Explorer
// for each account in the given date range.
func Build(ctx context.Context, accounts []cost.AccountTarget, dr cost.DateRange) (Report, error) {
	return buildWith(ctx, defaultCEClientFactory, accounts, dr)
}

type ceClientFactory func(cfg aws.Config) SavingsPlansAPI

func defaultCEClientFactory(cfg aws.Config) SavingsPlansAPI {
	region := cfg.Region
	if region == "" {
		region = cost.CostExplorerRegion
	}
	cfg.Region = region
	inner := costexplorer.NewFromConfig(cfg, func(o *costexplorer.Options) {
		o.Region = cost.CostExplorerRegion
	})
	return apilog.WrapSavingsPlans(inner)
}

func buildWith(ctx context.Context, newClient ceClientFactory, accounts []cost.AccountTarget, dr cost.DateRange) (Report, error) {
	if len(accounts) == 0 {
		return Report{}, fmt.Errorf("at least one account required")
	}
	ceDR := monthlyCERange(dr)
	ceClients := make(map[string]SavingsPlansAPI)

	sections := make([]AccountReport, 0, len(accounts))
	for _, acct := range accounts {
		credID := acct.CredentialsAccountID()
		ce, ok := ceClients[credID]
		if !ok {
			ce = newClient(acct.AWSConfig)
			ceClients[credID] = ce
		}

		metrics, err := buildAccountWith(ctx, ce, dr, ceDR, acct)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", acct.AccountDisplayName(), err)
		}
		sections = append(sections, AccountReport{
			AccountID:          acct.AccountID,
			AccountName:        acct.AccountDisplayName(),
			IsLinked:           acct.IsLinked(),
			Coverage:           metrics.Coverage,
			CoverageAverage:    metrics.CoverageAverage,
			Utilization:        metrics.Utilization,
			UtilizationAverage: metrics.UtilizationAverage,
			Savings:            metrics.Savings,
			SavingsTotal:       metrics.SavingsTotal,
		})
	}

	return Report{
		GeneratedAt: time.Now().UTC(),
		StartDate:   dr.Start.Format("2006-01-02"),
		EndDate:     dr.End.AddDate(0, 0, -1).Format("2006-01-02"),
		Accounts:    sections,
	}, nil
}

type accountMetrics struct {
	Coverage           []MonthlyMetric
	CoverageAverage    PeriodAverage
	Utilization        []MonthlyMetric
	UtilizationAverage PeriodAverage
	Savings            []MonthlySavings
	SavingsTotal       PeriodSavings
}

// buildAccountWith is the testable core for one account scope.
func buildAccountWith(
	ctx context.Context,
	ce SavingsPlansAPI,
	dr cost.DateRange,
	ceDR cost.DateRange,
	acct cost.AccountTarget,
) (accountMetrics, error) {
	filter := linkedAccountFilter(acct)
	monthlyInterval := &types.DateInterval{
		Start: aws.String(ceDR.Start.Format("2006-01-02")),
		End:   aws.String(ceDR.End.Format("2006-01-02")),
	}
	periodInterval := &types.DateInterval{
		Start: aws.String(dr.Start.Format("2006-01-02")),
		End:   aws.String(dr.End.Format("2006-01-02")),
	}

	coverages, err := fetchSavingsPlansCoverage(ctx, ce, monthlyInterval, types.GranularityMonthly, filter)
	if err != nil {
		return accountMetrics{}, err
	}

	utilizationResp, err := ce.GetSavingsPlansUtilization(ctx, &costexplorer.GetSavingsPlansUtilizationInput{
		TimePeriod:  monthlyInterval,
		Granularity: types.GranularityMonthly,
		Filter:      filter,
	})
	var utils []types.SavingsPlansUtilizationByTime
	if err != nil {
		if !isDataUnavailable(err) {
			return accountMetrics{}, fmt.Errorf("fetch SP utilization: %w", err)
		}
	} else {
		if utilizationResp == nil {
			return accountMetrics{}, fmt.Errorf("nil response from GetSavingsPlansUtilization")
		}
		utils = utilizationResp.SavingsPlansUtilizationsByTime
	}

	periodCoverages, err := fetchSavingsPlansCoverage(ctx, ce, periodInterval, "", filter)
	if err != nil {
		return accountMetrics{}, err
	}

	periodUtilResp, err := ce.GetSavingsPlansUtilization(ctx, &costexplorer.GetSavingsPlansUtilizationInput{
		TimePeriod: periodInterval,
		Filter:     filter,
	})
	var periodUtil PeriodAverage
	var periodSavings PeriodSavings
	if err != nil {
		if !isDataUnavailable(err) {
			return accountMetrics{}, fmt.Errorf("fetch SP period utilization: %w", err)
		}
	} else {
		if periodUtilResp == nil {
			return accountMetrics{}, fmt.Errorf("nil response from GetSavingsPlansUtilization")
		}
		periodUtil, err = parsePeriodUtilization(periodUtilResp)
		if err != nil {
			return accountMetrics{}, err
		}
		periodSavings = parsePeriodSavings(periodUtilResp)
	}

	utilization, err := parseUtilizationMetrics(utils)
	if err != nil {
		return accountMetrics{}, err
	}

	return accountMetrics{
		Coverage:           parseCoverageMetrics(coverages),
		CoverageAverage:    parsePeriodCoverage(periodCoverages),
		Utilization:        utilization,
		UtilizationAverage: periodUtil,
		Savings:            parseSavingsMetrics(utils),
		SavingsTotal:       periodSavings,
	}, nil
}

func fetchSavingsPlansCoverage(
	ctx context.Context,
	ce SavingsPlansAPI,
	interval *types.DateInterval,
	granularity types.Granularity,
	filter *types.Expression,
) ([]types.SavingsPlansCoverage, error) {
	var (
		coverages []types.SavingsPlansCoverage
		token     *string
	)

	for {
		out, err := ce.GetSavingsPlansCoverage(ctx, &costexplorer.GetSavingsPlansCoverageInput{
			TimePeriod:  interval,
			Granularity: granularity,
			Filter:      filter,
			NextToken:   token,
		})
		if err != nil {
			if isDataUnavailable(err) && len(coverages) == 0 {
				return nil, nil
			}
			return nil, fmt.Errorf("fetch SP coverage: %w", err)
		}
		if out == nil {
			return nil, fmt.Errorf("nil response from GetSavingsPlansCoverage")
		}
		coverages = append(coverages, out.SavingsPlansCoverages...)

		if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
			break
		}
		token = out.NextToken
	}

	return coverages, nil
}

// monthlyCERange aligns dr to calendar-month boundaries for CE MONTHLY granularity.
// Start moves to the first day of its month. End (exclusive) moves to the first day of
// the month after the last included calendar day, but never beyond the caller's End —
// extending the window past the latest available CE data triggers ValidationException.
func monthlyCERange(dr cost.DateRange) cost.DateRange {
	start := time.Date(dr.Start.Year(), dr.Start.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastIncluded := dr.End.AddDate(0, 0, -1)
	end := time.Date(lastIncluded.Year(), lastIncluded.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	if end.After(dr.End) {
		end = dr.End
	}
	return cost.DateRange{Start: start, End: end}
}

func isDataUnavailable(err error) bool {
	var du *types.DataUnavailableException
	return errors.As(err, &du)
}

func linkedAccountFilter(acct cost.AccountTarget) *types.Expression {
	return cost.LinkedAccountFilter(acct.AccountID, acct.ScopeToAccount())
}

// parseCoverageMetrics converts AWS SavingsPlansCoverages to sorted MonthlyMetric slice.
func parseCoverageMetrics(coverages []types.SavingsPlansCoverage) []MonthlyMetric {
	metrics := make([]MonthlyMetric, 0, len(coverages))
	for _, c := range coverages {
		if c.TimePeriod == nil || c.Coverage == nil {
			continue
		}
		pct := parseFloatPtr(c.Coverage.CoveragePercentage)
		metrics = append(metrics, MonthlyMetric{
			Month:      monthLabel(aws.ToString(c.TimePeriod.Start)),
			Percentage: pct,
		})
	}
	sortMetrics(metrics)
	return metrics
}

func parsePeriodCoverage(coverages []types.SavingsPlansCoverage) PeriodAverage {
	if len(coverages) == 0 {
		return PeriodAverage{}
	}
	c := coverages[0]
	if c.Coverage == nil {
		return PeriodAverage{}
	}
	return PeriodAverage{
		Percentage: parseFloatPtr(c.Coverage.CoveragePercentage),
		OK:         true,
	}
}

func parsePeriodUtilization(resp *costexplorer.GetSavingsPlansUtilizationOutput) (PeriodAverage, error) {
	if resp == nil || resp.Total == nil || resp.Total.Utilization == nil {
		return PeriodAverage{}, nil
	}
	pct := parseFloatPtr(resp.Total.Utilization.UtilizationPercentage)
	if err := validateUtilizationPercentage(pct, "period average"); err != nil {
		return PeriodAverage{}, err
	}
	return PeriodAverage{
		Percentage: pct,
		OK:         true,
	}, nil
}

func parsePeriodSavings(resp *costexplorer.GetSavingsPlansUtilizationOutput) PeriodSavings {
	if resp == nil || resp.Total == nil || resp.Total.Savings == nil {
		return PeriodSavings{}
	}
	return savingsFromData(resp.Total.Savings)
}

func parseSavingsMetrics(utils []types.SavingsPlansUtilizationByTime) []MonthlySavings {
	metrics := make([]MonthlySavings, 0, len(utils))
	for _, u := range utils {
		if u.TimePeriod == nil || u.Savings == nil {
			continue
		}
		s := savingsFromData(u.Savings)
		if !s.OK {
			continue
		}
		metrics = append(metrics, MonthlySavings{
			Month:                  monthLabel(aws.ToString(u.TimePeriod.Start)),
			NetSavings:             s.NetSavings,
			OnDemandCostEquivalent: s.OnDemandCostEquivalent,
		})
	}
	sortSavings(metrics)
	return metrics
}

func savingsFromData(s *types.SavingsPlansSavings) PeriodSavings {
	if s == nil {
		return PeriodSavings{}
	}
	return PeriodSavings{
		NetSavings:             parseFloatPtr(s.NetSavings),
		OnDemandCostEquivalent: parseFloatPtr(s.OnDemandCostEquivalent),
		OK:                     true,
	}
}

func savingsPercentage(netSavings, onDemandEquivalent float64) float64 {
	if onDemandEquivalent <= 0 {
		return 0
	}
	return netSavings / onDemandEquivalent * 100
}

func sortSavings(m []MonthlySavings) {
	sort.Slice(m, func(i, j int) bool { return m[i].Month < m[j].Month })
}

// parseUtilizationMetrics converts AWS SavingsPlansUtilizationsByTime to sorted MonthlyMetric slice.
func parseUtilizationMetrics(utils []types.SavingsPlansUtilizationByTime) ([]MonthlyMetric, error) {
	metrics := make([]MonthlyMetric, 0, len(utils))
	for _, u := range utils {
		if u.TimePeriod == nil || u.Utilization == nil {
			continue
		}
		month := monthLabel(aws.ToString(u.TimePeriod.Start))
		pct := parseFloatPtr(u.Utilization.UtilizationPercentage)
		if err := validateUtilizationPercentage(pct, month); err != nil {
			return nil, err
		}
		metrics = append(metrics, MonthlyMetric{
			Month:      month,
			Percentage: pct,
		})
	}
	sortMetrics(metrics)
	return metrics, nil
}

func sortMetrics(m []MonthlyMetric) {
	sort.Slice(m, func(i, j int) bool { return m[i].Month < m[j].Month })
}

// monthLabel converts an AWS date string (YYYY-MM-DD) to YYYY-MM.
func monthLabel(dateStr string) string {
	if len(dateStr) >= 7 {
		return dateStr[:7]
	}
	return dateStr
}


// validateUtilizationPercentage rejects utilization outside [0, 100].
func validateUtilizationPercentage(pct float64, scope string) error {
	if pct < 0 {
		return fmt.Errorf("utilization percentage %.1f%% for %s is negative", pct, scope)
	}
	if pct > 100 {
		return fmt.Errorf("utilization percentage %.1f%% for %s exceeds 100%%", pct, scope)
	}
	return nil
}

// parseFloatPtr parses a *string to float64, returning 0 on nil or error.
func parseFloatPtr(s *string) float64 {
	if s == nil {
		return 0
	}
	f, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return 0
	}
	return f
}
