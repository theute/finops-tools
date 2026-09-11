package cost

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

type fakeMonthlyCE struct {
	calls              int
	servicePeriodStart string
	servicePeriodEnd   string
}

func (f *fakeMonthlyCE) GetCostAndUsage(
	_ context.Context,
	params *costexplorer.GetCostAndUsageInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	f.calls++
	if params.Granularity == types.GranularityMonthly {
		return &costexplorer.GetCostAndUsageOutput{
			ResultsByTime: []types.ResultByTime{{
				TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
				Total: map[string]types.MetricValue{
					MetricNetAmortized: {Amount: aws.String("100.00"), Unit: aws.String("USD")},
				},
			}},
		}, nil
	}
	if params.TimePeriod != nil {
		f.servicePeriodStart = aws.ToString(params.TimePeriod.Start)
		f.servicePeriodEnd = aws.ToString(params.TimePeriod.End)
	}
	return &costexplorer.GetCostAndUsageOutput{
		ResultsByTime: []types.ResultByTime{{
			Groups: []types.Group{{
				Keys: []string{"Amazon EC2"},
				Metrics: map[string]types.MetricValue{
					MetricNetAmortized: {Amount: aws.String("50.00"), Unit: aws.String("USD")},
				},
			}},
		}},
	}, nil
}

func TestFetchMonthly(t *testing.T) {
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	dr := LastNCalendarMonthsRange(6, now)
	fake := &fakeMonthlyCE{}
	opts := fetchAWSOptions{
		Now: now,
		NewCostExplorer: func(aws.Config) CostExplorerAPI {
			return fake
		},
	}

	result, err := fetchAccountMonthlyWith(context.Background(), AccountTarget{
		AccountID: "111111111111",
		AWSConfig: aws.Config{Region: CostExplorerRegion},
	}, dr, opts)
	if err != nil {
		t.Fatalf("fetchAccountMonthlyWith() error = %v", err)
	}
	if len(result.Months) != 1 || result.Months[0].Amount != 100 {
		t.Fatalf("months = %+v", result.Months)
	}
	if len(result.TopServices) != 1 || result.TopServices[0].Service != "Amazon EC2" {
		t.Fatalf("top services = %+v", result.TopServices)
	}
	if fake.servicePeriodStart != "2026-02-01" || fake.servicePeriodEnd != "2026-03-01" {
		t.Fatalf("top services period = %s/%s, want 2026-02-01/2026-03-01", fake.servicePeriodStart, fake.servicePeriodEnd)
	}
}

func TestFetchMonthlyKeepsOverlappingPayerAndLinked(t *testing.T) {
	fake := &fakeMonthlyCE{}
	got, err := fetchMonthlyWith(context.Background(), CostQuery{
		Accounts: []AccountTarget{
			{AccountID: "123456789012", AWSConfig: aws.Config{Region: CostExplorerRegion}},
			{AccountID: "111111111111", PayerAccountID: "123456789012", AWSConfig: aws.Config{Region: CostExplorerRegion}},
		},
		Range:   LastNCalendarMonthsRange(1, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)),
		Workers: 1,
	}, fetchAWSOptions{
		NewCostExplorer: func(aws.Config) CostExplorerAPI { return fake },
	})
	if err != nil {
		t.Fatalf("fetchMonthlyWith() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2 (payer and linked)", len(got))
	}
	if got[0].AccountID != "123456789012" || got[1].AccountID != "111111111111" {
		t.Fatalf("account IDs = %s,%s", got[0].AccountID, got[1].AccountID)
	}
}

type failingMonthlyCE struct{}

func (failingMonthlyCE) GetCostAndUsage(
	context.Context,
	*costexplorer.GetCostAndUsageInput,
	...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	return nil, fmt.Errorf("throttled")
}

func TestFetchMonthlyContinuesWhenOneAccountFails(t *testing.T) {
	got, err := fetchMonthlyWith(context.Background(), CostQuery{
		Accounts: []AccountTarget{
			{AccountID: "111111111111", AWSConfig: aws.Config{Region: "ok"}},
			{AccountID: "222222222222", AWSConfig: aws.Config{Region: "fail"}},
		},
		Range:   LastNCalendarMonthsRange(1, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)),
		Workers: 1,
	}, fetchAWSOptions{
		NewCostExplorer: func(cfg aws.Config) CostExplorerAPI {
			if cfg.Region == "fail" {
				return failingMonthlyCE{}
			}
			return &fakeMonthlyCE{}
		},
	})
	if err != nil {
		t.Fatalf("fetchMonthlyWith() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2", len(got))
	}
	if got[0].Error != "" || got[0].AccountID != "111111111111" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].AccountID != "222222222222" || got[1].Error == "" {
		t.Fatalf("second = %+v", got[1])
	}
}

type contextAbortMonthlyCE struct {
	err error
}

func (c contextAbortMonthlyCE) GetCostAndUsage(
	context.Context,
	*costexplorer.GetCostAndUsageInput,
	...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	return nil, c.err
}

func TestFetchMonthlyReturnsContextAbort(t *testing.T) {
	t.Parallel()

	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		want := want
		t.Run(want.Error(), func(t *testing.T) {
			t.Parallel()
			got, err := fetchMonthlyWith(context.Background(), CostQuery{
				Accounts: []AccountTarget{
					{AccountID: "111111111111", AWSConfig: aws.Config{Region: CostExplorerRegion}},
					{AccountID: "222222222222", AWSConfig: aws.Config{Region: CostExplorerRegion}},
				},
				Range:   LastNCalendarMonthsRange(1, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)),
				Workers: 1,
			}, fetchAWSOptions{
				NewCostExplorer: func(aws.Config) CostExplorerAPI {
					return contextAbortMonthlyCE{err: want}
				},
			})
			if !errors.Is(err, want) {
				t.Fatalf("fetchMonthlyWith() error = %v, want %v", err, want)
			}
			if len(got) != 0 {
				t.Fatalf("got %d accounts, want none on context abort", len(got))
			}
		})
	}
}

type contextAbortTopServicesCE struct {
	err error
}

func (c contextAbortTopServicesCE) GetCostAndUsage(
	_ context.Context,
	params *costexplorer.GetCostAndUsageInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	if params != nil && len(params.GroupBy) > 0 {
		return nil, c.err
	}
	return (&fakeMonthlyCE{}).GetCostAndUsage(context.Background(), params)
}

func TestFetchMonthlyReturnsContextAbortFromTopServices(t *testing.T) {
	t.Parallel()

	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		want := want
		t.Run(want.Error(), func(t *testing.T) {
			t.Parallel()
			got, err := fetchMonthlyWith(context.Background(), CostQuery{
				Accounts: []AccountTarget{
					{AccountID: "111111111111", AWSConfig: aws.Config{Region: CostExplorerRegion}},
				},
				Range:   LastNCalendarMonthsRange(6, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)),
				Workers: 1,
			}, fetchAWSOptions{
				Now: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
				NewCostExplorer: func(aws.Config) CostExplorerAPI {
					return contextAbortTopServicesCE{err: want}
				},
			})
			if !errors.Is(err, want) {
				t.Fatalf("fetchMonthlyWith() error = %v, want %v", err, want)
			}
			if len(got) != 0 {
				t.Fatalf("got %d accounts, want none on context abort", len(got))
			}
		})
	}
}

type topServicesFailCE struct{}

func (topServicesFailCE) GetCostAndUsage(
	_ context.Context,
	params *costexplorer.GetCostAndUsageInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	if params != nil && len(params.GroupBy) > 0 {
		return nil, fmt.Errorf("service breakdown denied")
	}
	return (&fakeMonthlyCE{}).GetCostAndUsage(context.Background(), params)
}

func TestFetchAccountMonthlyKeepsMonthsWhenTopServicesFail(t *testing.T) {
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	result, err := fetchAccountMonthlyWith(context.Background(), AccountTarget{
		AccountID: "111111111111",
		AWSConfig: aws.Config{Region: CostExplorerRegion},
	}, LastNCalendarMonthsRange(6, now), fetchAWSOptions{
		Now:             now,
		NewCostExplorer: func(aws.Config) CostExplorerAPI { return topServicesFailCE{} },
	})
	if err != nil {
		t.Fatalf("fetchAccountMonthlyWith() error = %v", err)
	}
	if result.Error != "" {
		t.Fatalf("Error = %q, want empty", result.Error)
	}
	if len(result.Months) != 1 || result.Months[0].Amount != 100 {
		t.Fatalf("months = %+v", result.Months)
	}
	if len(result.TopServices) != 0 {
		t.Fatalf("top services = %+v", result.TopServices)
	}
	if result.TopServicesError == "" {
		t.Fatal("TopServicesError is empty")
	}
}

func TestSumNetAmortizedMonthlyKeepsZeroMonths(t *testing.T) {
	ce := &fakeCE{
		pages: [][]types.ResultByTime{{
			{
				TimePeriod: &types.DateInterval{Start: aws.String("2026-01-01"), End: aws.String("2026-02-01")},
				Total: map[string]types.MetricValue{
					MetricNetAmortized: {Amount: aws.String("100.00"), Unit: aws.String("USD")},
				},
			},
			{
				TimePeriod: &types.DateInterval{Start: aws.String("2026-02-01"), End: aws.String("2026-03-01")},
				Total: map[string]types.MetricValue{
					MetricNetAmortized: {Amount: aws.String("0"), Unit: aws.String("USD")},
				},
			},
			{
				TimePeriod: &types.DateInterval{Start: aws.String("2026-03-01"), End: aws.String("2026-04-01")},
				Total: map[string]types.MetricValue{
					MetricNetAmortized: {Amount: aws.String("50.00"), Unit: aws.String("USD")},
				},
			},
		}},
	}
	months, currency, total, err := sumNetAmortizedMonthly(context.Background(), ce, DateRange{
		Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if currency != "USD" {
		t.Errorf("currency = %q, want USD", currency)
	}
	if total != 150 {
		t.Errorf("total = %v, want 150", total)
	}
	want := []MonthlyCostPoint{
		{Month: "2026-01", Amount: 100},
		{Month: "2026-02", Amount: 0},
		{Month: "2026-03", Amount: 50},
	}
	if len(months) != len(want) {
		t.Fatalf("months = %+v, want %+v", months, want)
	}
	for i := range want {
		if months[i] != want[i] {
			t.Errorf("months[%d] = %+v, want %+v", i, months[i], want[i])
		}
	}
}

func TestMonthlyCERange(t *testing.T) {
	dr := DateRange{
		Start: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	got := monthlyCERange(dr)
	if got.Start.Day() != 1 || got.Start.Month() != time.January {
		t.Fatalf("start = %v", got.Start)
	}
}

func TestLastCompleteMonthRange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		start     string
		end       string
		wantStart string
		wantEnd   string
		wantZero  bool
	}{
		{
			name:      "exclusive end on first of month",
			start:     "2025-09-01",
			end:       "2026-03-01",
			wantStart: "2026-02-01",
			wantEnd:   "2026-03-01",
		},
		{
			name:      "mid-month end uses previous calendar month",
			start:     "2025-09-01",
			end:       "2026-03-15",
			wantStart: "2026-02-01",
			wantEnd:   "2026-03-01",
		},
		{
			name:     "range has no complete month",
			start:    "2026-03-01",
			end:      "2026-03-15",
			wantZero: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			start, err := time.ParseInLocation("2006-01-02", tc.start, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			end, err := time.ParseInLocation("2006-01-02", tc.end, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			got := lastCompleteMonthRange(DateRange{Start: start, End: end})
			if tc.wantZero {
				if !got.IsZero() {
					t.Fatalf("got %s/%s, want zero", formatDate(got.Start), formatDate(got.End))
				}
				return
			}
			if formatDate(got.Start) != tc.wantStart || formatDate(got.End) != tc.wantEnd {
				t.Fatalf("got %s/%s, want %s/%s", formatDate(got.Start), formatDate(got.End), tc.wantStart, tc.wantEnd)
			}
		})
	}
}
