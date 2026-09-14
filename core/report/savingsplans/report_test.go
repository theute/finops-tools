package savingsplans

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/openshift-online/finops-tools/core/cost"
)

// fakeSavingsPlansClient implements SavingsPlansAPI for testing.
type fakeSavingsPlansClient struct {
	coverageResp    *costexplorer.GetSavingsPlansCoverageOutput
	utilizationResp *costexplorer.GetSavingsPlansUtilizationOutput
	coverageErr     error
	utilizationErr  error

	coverageByAccount           map[string]*costexplorer.GetSavingsPlansCoverageOutput
	coveragePeriodByAccount     map[string]*costexplorer.GetSavingsPlansCoverageOutput
	coveragePages               []*costexplorer.GetSavingsPlansCoverageOutput
	coveragePageIdx             int
	utilizationByAccount        map[string]*costexplorer.GetSavingsPlansUtilizationOutput
	utilizationPeriodByAccount  map[string]*costexplorer.GetSavingsPlansUtilizationOutput
	utilizationErrByAccount     map[string]error
	utilizationPeriodErrByAccount map[string]error
}

func (f *fakeSavingsPlansClient) GetSavingsPlansCoverage(
	_ context.Context,
	in *costexplorer.GetSavingsPlansCoverageInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansCoverageOutput, error) {
	if f.coverageErr != nil {
		return nil, f.coverageErr
	}
	if len(f.coveragePages) > 0 {
		if f.coveragePageIdx >= len(f.coveragePages) {
			return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
		}
		resp := f.coveragePages[f.coveragePageIdx]
		f.coveragePageIdx++
		return resp, nil
	}
	acctKey := linkedAccountFromFilter(in.Filter)
	if in.Granularity == "" {
		if f.coveragePeriodByAccount != nil {
			if resp, ok := f.coveragePeriodByAccount[acctKey]; ok {
				return resp, nil
			}
		}
		return &costexplorer.GetSavingsPlansCoverageOutput{}, nil
	}
	if f.coverageByAccount != nil {
		if resp, ok := f.coverageByAccount[acctKey]; ok {
			return resp, nil
		}
	}
	return f.coverageResp, nil
}

func (f *fakeSavingsPlansClient) GetSavingsPlansUtilization(
	_ context.Context,
	in *costexplorer.GetSavingsPlansUtilizationInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetSavingsPlansUtilizationOutput, error) {
	acctKey := linkedAccountFromFilter(in.Filter)
	if in.Granularity == "" {
		if f.utilizationPeriodErrByAccount != nil {
			if err, ok := f.utilizationPeriodErrByAccount[acctKey]; ok {
				return nil, err
			}
		}
		if f.utilizationPeriodByAccount != nil {
			if resp, ok := f.utilizationPeriodByAccount[acctKey]; ok {
				return resp, nil
			}
		}
		return &costexplorer.GetSavingsPlansUtilizationOutput{}, nil
	}
	if f.utilizationErrByAccount != nil {
		if err, ok := f.utilizationErrByAccount[acctKey]; ok {
			return nil, err
		}
	}
	if f.utilizationErr != nil {
		return nil, f.utilizationErr
	}
	if f.utilizationByAccount != nil {
		if resp, ok := f.utilizationByAccount[acctKey]; ok {
			return resp, nil
		}
	}
	return f.utilizationResp, nil
}

func linkedAccountFromFilter(filter *types.Expression) string {
	if filter == nil || filter.Dimensions == nil || len(filter.Dimensions.Values) == 0 {
		return ""
	}
	return filter.Dimensions.Values[0]
}

func TestParseCoverageMetrics(t *testing.T) {
	input := []types.SavingsPlansCoverage{
		{
			TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
			Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("75.5")},
		},
		{
			TimePeriod: &types.DateInterval{Start: aws.String("2026-02-01"), End: aws.String("2026-03-01")},
			Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("82.3")},
		},
	}

	metrics := parseCoverageMetrics(input)

	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
	if metrics[0].Month != "2026-01" {
		t.Errorf("metrics[0].Month = %q, want %q", metrics[0].Month, "2026-01")
	}
	if metrics[0].Percentage != 75.5 {
		t.Errorf("metrics[0].Percentage = %f, want 75.5", metrics[0].Percentage)
	}
	if metrics[1].Month != "2026-02" {
		t.Errorf("metrics[1].Month = %q, want %q", metrics[1].Month, "2026-02")
	}
	if metrics[1].Percentage != 82.3 {
		t.Errorf("metrics[1].Percentage = %f, want 82.3", metrics[1].Percentage)
	}
}

func TestParseCoverageMetrics_SkipsNilEntries(t *testing.T) {
	input := []types.SavingsPlansCoverage{
		{TimePeriod: nil, Coverage: nil}, // should be skipped
		{
			TimePeriod: &types.DateInterval{Start: aws.String("2026-03-01"), End: aws.String("2026-04-01")},
			Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("60.0")},
		},
	}

	metrics := parseCoverageMetrics(input)

	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Month != "2026-03" {
		t.Errorf("metrics[0].Month = %q, want %q", metrics[0].Month, "2026-03")
	}
}

func TestParseUtilizationMetrics(t *testing.T) {
	input := []types.SavingsPlansUtilizationByTime{
		{
			TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
			Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("88.2")},
		},
		{
			TimePeriod:  &types.DateInterval{Start: aws.String("2026-02-01"), End: aws.String("2026-03-01")},
			Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("91.0")},
		},
	}

	metrics, err := parseUtilizationMetrics(input)
	if err != nil {
		t.Fatalf("parseUtilizationMetrics: %v", err)
	}

	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
	if metrics[0].Percentage != 88.2 {
		t.Errorf("metrics[0].Percentage = %f, want 88.2", metrics[0].Percentage)
	}
	if metrics[1].Percentage != 91.0 {
		t.Errorf("metrics[1].Percentage = %f, want 91.0", metrics[1].Percentage)
	}
}

func TestParseUtilizationMetrics_rejectsOver100(t *testing.T) {
	_, err := parseUtilizationMetrics([]types.SavingsPlansUtilizationByTime{{
		TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
		Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("101.3")},
	}})
	if err == nil {
		t.Fatal("expected error for utilization over 100%")
	}
	if !strings.Contains(err.Error(), "101.3%") || !strings.Contains(err.Error(), "2026-01") {
		t.Errorf("error = %q, want month and percentage in message", err)
	}
}

func TestParseUtilizationMetrics_SortedByMonth(t *testing.T) {
	// Input is intentionally out of order.
	input := []types.SavingsPlansUtilizationByTime{
		{
			TimePeriod:  &types.DateInterval{Start: aws.String("2026-03-01"), End: aws.String("2026-04-01")},
			Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("95.0")},
		},
		{
			TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
			Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("80.0")},
		},
	}

	metrics, err := parseUtilizationMetrics(input)
	if err != nil {
		t.Fatalf("parseUtilizationMetrics: %v", err)
	}

	if metrics[0].Month != "2026-01" {
		t.Errorf("expected sorted: metrics[0].Month = %q, want 2026-01", metrics[0].Month)
	}
	if metrics[1].Month != "2026-03" {
		t.Errorf("expected sorted: metrics[1].Month = %q, want 2026-03", metrics[1].Month)
	}
}

func TestBuildAccountWith_CoveragePagination(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coveragePages: []*costexplorer.GetSavingsPlansCoverageOutput{
			{
				SavingsPlansCoverages: []types.SavingsPlansCoverage{
					{
						TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("72.0")},
					},
				},
				NextToken: aws.String("page-2"),
			},
			{
				SavingsPlansCoverages: []types.SavingsPlansCoverage{
					{
						TimePeriod: &types.DateInterval{Start: aws.String("2026-02-01"), End: aws.String("2026-03-01")},
						Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("80.0")},
					},
				},
			},
		},
		utilizationResp: &costexplorer.GetSavingsPlansUtilizationOutput{},
	}

	dr := cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}

	metrics, err := buildAccountWith(context.Background(), fake, dr, monthlyCERange(dr), cost.AccountTarget{})
	if err != nil {
		t.Fatalf("buildAccountWith returned error: %v", err)
	}
	coverage := metrics.Coverage
	utilization := metrics.Utilization
	if len(utilization) != 0 {
		t.Fatalf("Utilization len = %d, want 0", len(utilization))
	}
	if len(coverage) != 2 {
		t.Fatalf("Coverage len = %d, want 2", len(coverage))
	}
	if coverage[0].Month != "2026-01" || coverage[0].Percentage != 72.0 {
		t.Errorf("coverage[0] = %+v, want 2026-01 at 72.0", coverage[0])
	}
	if coverage[1].Month != "2026-02" || coverage[1].Percentage != 80.0 {
		t.Errorf("coverage[1] = %+v, want 2026-02 at 80.0", coverage[1])
	}
	if fake.coveragePageIdx != 2 {
		t.Errorf("coveragePageIdx = %d, want 2 API calls", fake.coveragePageIdx)
	}
}

func TestParsePeriodSavings(t *testing.T) {
	total := parsePeriodSavings(periodUtilizationRespWithSavings("88.0", "1234.56", "5000.00"))
	if !total.OK || total.NetSavings != 1234.56 || total.OnDemandCostEquivalent != 5000.0 {
		t.Errorf("parsePeriodSavings = %+v, want net=1234.56 onDemand=5000", total)
	}
	if got, want := total.SavingsPercentage(), 24.6912; (got-want)*(got-want) > 0.0001 {
		t.Errorf("SavingsPercentage = %f, want ~%f", got, want)
	}
	if parsePeriodSavings(nil).OK {
		t.Error("expected empty period savings")
	}
}

func TestParseSavingsMetrics(t *testing.T) {
	input := []types.SavingsPlansUtilizationByTime{
		{
			TimePeriod:  &types.DateInterval{Start: aws.String("2026-02-01"), End: aws.String("2026-03-01")},
			Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("90.0")},
			Savings: &types.SavingsPlansSavings{
				NetSavings:             aws.String("200.00"),
				OnDemandCostEquivalent: aws.String("1000.00"),
			},
		},
		{
			TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
			Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("85.0")},
			Savings: &types.SavingsPlansSavings{
				NetSavings:             aws.String("150.00"),
				OnDemandCostEquivalent: aws.String("750.00"),
			},
		},
	}

	metrics := parseSavingsMetrics(input)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
	if metrics[0].Month != "2026-01" || metrics[0].NetSavings != 150.0 {
		t.Errorf("metrics[0] = %+v, want 2026-01 at 150", metrics[0])
	}
	if metrics[1].Month != "2026-02" || metrics[1].SavingsPercentage() != 20.0 {
		t.Errorf("metrics[1] = %+v, want 2026-02 at 20%%", metrics[1])
	}
}

func TestSavingsPercentage_ZeroOnDemand(t *testing.T) {
	if got := (MonthlySavings{NetSavings: 100}).SavingsPercentage(); got != 0 {
		t.Errorf("SavingsPercentage with zero on-demand = %f, want 0", got)
	}
}

func TestBuildAccountWith_HappyPath(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageResp: &costexplorer.GetSavingsPlansCoverageOutput{
			SavingsPlansCoverages: []types.SavingsPlansCoverage{
				{
					TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
					Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("72.0")},
				},
			},
		},
		coveragePeriodByAccount: map[string]*costexplorer.GetSavingsPlansCoverageOutput{
			"": {
				SavingsPlansCoverages: []types.SavingsPlansCoverage{
					{
						TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("74.5")},
					},
				},
			},
		},
		utilizationResp: &costexplorer.GetSavingsPlansUtilizationOutput{
			SavingsPlansUtilizationsByTime: []types.SavingsPlansUtilizationByTime{
				{
					TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
					Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("85.5")},
					Savings: &types.SavingsPlansSavings{
						NetSavings:             aws.String("500.00"),
						OnDemandCostEquivalent: aws.String("2000.00"),
					},
				},
			},
		},
		utilizationPeriodByAccount: map[string]*costexplorer.GetSavingsPlansUtilizationOutput{
			"": periodUtilizationRespWithSavings("88.0", "500.00", "2000.00"),
		},
	}

	dr := cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}

	metrics, err := buildAccountWith(context.Background(), fake, dr, monthlyCERange(dr), cost.AccountTarget{})
	if err != nil {
		t.Fatalf("buildAccountWith returned error: %v", err)
	}
	coverage := metrics.Coverage
	utilization := metrics.Utilization
	if len(coverage) != 1 {
		t.Fatalf("Coverage len = %d, want 1", len(coverage))
	}
	if coverage[0].Percentage != 72.0 {
		t.Errorf("Coverage[0].Percentage = %f, want 72.0", coverage[0].Percentage)
	}
	if len(utilization) != 1 {
		t.Fatalf("Utilization len = %d, want 1", len(utilization))
	}
	if utilization[0].Percentage != 85.5 {
		t.Errorf("Utilization[0].Percentage = %f, want 85.5", utilization[0].Percentage)
	}
	if !metrics.CoverageAverage.OK || metrics.CoverageAverage.Percentage != 74.5 {
		t.Errorf("CoverageAverage = %+v, want 74.5 from AWS period query", metrics.CoverageAverage)
	}
	if !metrics.UtilizationAverage.OK || metrics.UtilizationAverage.Percentage != 88.0 {
		t.Errorf("UtilizationAverage = %+v, want 88.0 from AWS period query", metrics.UtilizationAverage)
	}
	if len(metrics.Savings) != 1 || metrics.Savings[0].NetSavings != 500.0 {
		t.Errorf("Savings = %+v, want one month at 500", metrics.Savings)
	}
	if !metrics.SavingsTotal.OK || metrics.SavingsTotal.NetSavings != 500.0 {
		t.Errorf("SavingsTotal = %+v, want 500 from AWS period query", metrics.SavingsTotal)
	}
}

func TestParsePeriodCoverage(t *testing.T) {
	avg := parsePeriodCoverage([]types.SavingsPlansCoverage{
		{
			Coverage: &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("91.2")},
		},
	})
	if !avg.OK || avg.Percentage != 91.2 {
		t.Errorf("parsePeriodCoverage = %+v, want 91.2", avg)
	}
	if parsePeriodCoverage(nil).OK {
		t.Error("expected empty coverage period average")
	}
}

func TestParsePeriodUtilization(t *testing.T) {
	avg, err := parsePeriodUtilization(periodUtilizationResp("93.4", nil))
	if err != nil {
		t.Fatalf("parsePeriodUtilization: %v", err)
	}
	if !avg.OK || avg.Percentage != 93.4 {
		t.Errorf("parsePeriodUtilization = %+v, want 93.4", avg)
	}
	empty, err := parsePeriodUtilization(nil)
	if err != nil {
		t.Fatalf("parsePeriodUtilization(nil): %v", err)
	}
	if empty.OK {
		t.Error("expected empty utilization period average")
	}
	_, err = parsePeriodUtilization(periodUtilizationResp("101.0", nil))
	if err == nil {
		t.Fatal("expected error for period utilization over 100%")
	}
}

func periodUtilizationResp(utilPct string, savings *types.SavingsPlansSavings) *costexplorer.GetSavingsPlansUtilizationOutput {
	return &costexplorer.GetSavingsPlansUtilizationOutput{
		Total: &types.SavingsPlansUtilizationAggregates{
			Utilization: &types.SavingsPlansUtilization{
				UtilizationPercentage: aws.String(utilPct),
			},
			Savings: savings,
		},
	}
}

func periodUtilizationRespWithSavings(utilPct, netSavings, onDemand string) *costexplorer.GetSavingsPlansUtilizationOutput {
	return periodUtilizationResp(utilPct, &types.SavingsPlansSavings{
		NetSavings:             aws.String(netSavings),
		OnDemandCostEquivalent: aws.String(onDemand),
	})
}

func TestBuild_LinkedWithPayer_NoBorrowedUtilization(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageByAccount: map[string]*costexplorer.GetSavingsPlansCoverageOutput{
			"111111111111": {
				SavingsPlansCoverages: []types.SavingsPlansCoverage{
					{
						TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("72.0")},
					},
				},
			},
			"": {SavingsPlansCoverages: []types.SavingsPlansCoverage{}},
		},
		utilizationByAccount: map[string]*costexplorer.GetSavingsPlansUtilizationOutput{
			"": {
				SavingsPlansUtilizationsByTime: []types.SavingsPlansUtilizationByTime{
					{
						TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("88.0")},
					},
				},
			},
		},
		utilizationErrByAccount: map[string]error{
			"111111111111": &types.DataUnavailableException{Message: aws.String("unavailable")},
		},
	}
	dr := cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	report, err := buildWith(context.Background(), func(aws.Config) SavingsPlansAPI { return fake }, []cost.AccountTarget{
		{AccountID: "123456789012", DisplayName: "Payer"},
		{AccountID: "111111111111", DisplayName: "Quay", PayerAccountID: "123456789012"},
	}, dr)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if len(report.Accounts) != 2 {
		t.Fatalf("Accounts len = %d, want 2", len(report.Accounts))
	}
	if report.Accounts[1].Coverage[0].Percentage != 72.0 {
		t.Errorf("linked coverage = %f, want 72.0", report.Accounts[1].Coverage[0].Percentage)
	}
	if len(report.Accounts[1].Utilization) != 0 {
		t.Errorf("linked utilization should be empty without owned SPs, got %d rows", len(report.Accounts[1].Utilization))
	}
	if report.Accounts[0].Utilization[0].Percentage != 88.0 {
		t.Errorf("payer utilization = %f, want 88.0", report.Accounts[0].Utilization[0].Percentage)
	}
}

func TestBuild_MultipleAccounts(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageByAccount: map[string]*costexplorer.GetSavingsPlansCoverageOutput{
			"111111111111": {
				SavingsPlansCoverages: []types.SavingsPlansCoverage{
					{
						TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("80.0")},
					},
				},
			},
			"222222222222": {
				SavingsPlansCoverages: []types.SavingsPlansCoverage{
					{
						TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("65.0")},
					},
				},
			},
		},
		utilizationByAccount: map[string]*costexplorer.GetSavingsPlansUtilizationOutput{
			"111111111111": {
				SavingsPlansUtilizationsByTime: []types.SavingsPlansUtilizationByTime{
					{
						TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("90.0")},
					},
				},
			},
			"222222222222": {
				SavingsPlansUtilizationsByTime: []types.SavingsPlansUtilizationByTime{
					{
						TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("55.0")},
					},
				},
			},
		},
	}

	dr := cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}

	report, err := buildWith(context.Background(), func(aws.Config) SavingsPlansAPI { return fake }, []cost.AccountTarget{
		{AccountID: "111111111111", DisplayName: "Member One", PayerAccountID: "123456789012", AWSConfig: aws.Config{}},
		{AccountID: "222222222222", DisplayName: "Member Two", PayerAccountID: "123456789012", AWSConfig: aws.Config{}},
	}, dr)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if report.StartDate != "2026-01-01" {
		t.Errorf("StartDate = %q, want %q", report.StartDate, "2026-01-01")
	}
	if report.EndDate != "2026-01-31" {
		t.Errorf("EndDate = %q, want %q", report.EndDate, "2026-01-31")
	}
	if len(report.Accounts) != 2 {
		t.Fatalf("Accounts len = %d, want 2", len(report.Accounts))
	}
	if report.Accounts[0].AccountName != "Member One" {
		t.Errorf("Accounts[0].AccountName = %q, want Member One", report.Accounts[0].AccountName)
	}
	if !report.Accounts[0].IsLinked {
		t.Error("member account should be marked linked")
	}
	if report.Accounts[0].Coverage[0].Percentage != 80.0 {
		t.Errorf("member one coverage = %f, want 80.0", report.Accounts[0].Coverage[0].Percentage)
	}
	if report.Accounts[1].AccountName != "Member Two" {
		t.Errorf("Accounts[1].AccountName = %q, want Member Two", report.Accounts[1].AccountName)
	}
	if report.Accounts[1].Coverage[0].Percentage != 65.0 {
		t.Errorf("member two coverage = %f, want 65.0", report.Accounts[1].Coverage[0].Percentage)
	}
	if report.GeneratedAt.IsZero() {
		t.Error("GeneratedAt should not be zero")
	}
}

func TestBuild_PreservesRequestedDateRange(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageByAccount: map[string]*costexplorer.GetSavingsPlansCoverageOutput{
			"111111111111": {
				SavingsPlansCoverages: []types.SavingsPlansCoverage{
					{
						TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("80.0")},
					},
				},
			},
		},
		utilizationByAccount: map[string]*costexplorer.GetSavingsPlansUtilizationOutput{
			"111111111111": {
				SavingsPlansUtilizationsByTime: []types.SavingsPlansUtilizationByTime{
					{
						TimePeriod:  &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
						Utilization: &types.SavingsPlansUtilization{UtilizationPercentage: aws.String("90.0")},
					},
				},
			},
		},
	}
	dr := cost.DateRange{
		Start: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC),
	}
	report, err := buildWith(context.Background(), func(aws.Config) SavingsPlansAPI { return fake }, []cost.AccountTarget{
		{AccountID: "111111111111", DisplayName: "Member", PayerAccountID: "123456789012", AWSConfig: aws.Config{}},
	}, dr)
	if err != nil {
		t.Fatalf("buildWith returned error: %v", err)
	}
	if report.StartDate != "2026-01-15" {
		t.Errorf("StartDate = %q, want caller-requested 2026-01-15", report.StartDate)
	}
	if report.EndDate != "2026-06-09" {
		t.Errorf("EndDate = %q, want caller-requested 2026-06-09", report.EndDate)
	}
}

func TestBuildAccountWith_CoverageAPIError(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageErr: fmt.Errorf("access denied"),
	}
	dr := cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	_, err := buildAccountWith(context.Background(), fake, dr, monthlyCERange(dr), cost.AccountTarget{})
	if err == nil {
		t.Fatal("expected error from coverage API failure, got nil")
	}
}

func TestMonthlyCERange(t *testing.T) {
	t.Run("start aligned to month", func(t *testing.T) {
		dr := monthlyCERange(cost.DateRange{
			Start: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC),
		})
		if got, want := dr.Start.Format("2006-01-02"), "2026-01-01"; got != want {
			t.Errorf("Start = %q, want %q", got, want)
		}
		if got, want := dr.End.Format("2006-01-02"), "2026-06-10"; got != want {
			t.Errorf("End = %q, want %q (must not extend past caller End)", got, want)
		}
	})
	t.Run("full month end unchanged", func(t *testing.T) {
		dr := monthlyCERange(cost.DateRange{
			Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		})
		if got, want := dr.End.Format("2006-01-02"), "2026-02-01"; got != want {
			t.Errorf("End = %q, want %q", got, want)
		}
	})
}

func TestBuildAccountWith_DataUnavailableUtilization(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageResp: &costexplorer.GetSavingsPlansCoverageOutput{
			SavingsPlansCoverages: []types.SavingsPlansCoverage{
				{
					TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
					Coverage:   &types.SavingsPlansCoverageData{CoveragePercentage: aws.String("72.0")},
				},
			},
		},
		utilizationErr: &types.DataUnavailableException{Message: aws.String("unavailable")},
	}
	linked := cost.AccountTarget{AccountID: "111111111111", PayerAccountID: "123456789012"}
	dr := cost.DateRange{
		Start: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	metrics, err := buildAccountWith(context.Background(), fake, dr, monthlyCERange(dr), linked)
	if err != nil {
		t.Fatalf("buildAccountWith returned error: %v", err)
	}
	coverage := metrics.Coverage
	utilization := metrics.Utilization
	if len(coverage) != 1 {
		t.Fatalf("Coverage len = %d, want 1", len(coverage))
	}
	if len(utilization) != 0 {
		t.Fatalf("Utilization len = %d, want 0", len(utilization))
	}
}

func TestBuildAccountWith_NilCoverageResponse(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageResp: nil,
	}
	dr := cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	_, err := buildAccountWith(context.Background(), fake, dr, monthlyCERange(dr), cost.AccountTarget{})
	if err == nil {
		t.Fatal("expected error for nil coverage response, got nil")
	}
}

func TestBuildAccountWith_NilUtilizationResponse(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageResp:    &costexplorer.GetSavingsPlansCoverageOutput{},
		utilizationResp: nil,
	}
	dr := cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	_, err := buildAccountWith(context.Background(), fake, dr, monthlyCERange(dr), cost.AccountTarget{})
	if err == nil {
		t.Fatal("expected error for nil utilization response, got nil")
	}
}

func TestBuildAccountWith_UtilizationAPIError(t *testing.T) {
	fake := &fakeSavingsPlansClient{
		coverageResp:   &costexplorer.GetSavingsPlansCoverageOutput{},
		utilizationErr: fmt.Errorf("throttled"),
	}
	dr := cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	_, err := buildAccountWith(context.Background(), fake, dr, monthlyCERange(dr), cost.AccountTarget{})
	if err == nil {
		t.Fatal("expected error from utilization API failure, got nil")
	}
}

func TestBuild_RequiresAccounts(t *testing.T) {
	_, err := buildWith(context.Background(), defaultCEClientFactory, nil, cost.DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected error for empty accounts")
	}
}

func TestMonthLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-01-01", "2026-01"},
		{"2026-12-31", "2026-12"},
		{"2025-06-15", "2025-06"},
		{"short", "short"}, // too short, returned as-is
	}
	for _, tt := range tests {
		got := monthLabel(tt.input)
		if got != tt.want {
			t.Errorf("monthLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
