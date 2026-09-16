package accountreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	coreaccount "github.com/openshift-online/finops-tools/core/account"
	"github.com/openshift-online/finops-tools/core/cost"
	"github.com/openshift-online/finops-tools/core/inventory"
)

func TestBuildSkipsInventoryScanWhenNoTargets(t *testing.T) {
	t.Parallel()

	scanCalls := 0
	result, err := Build(context.Background(), BuildInput{
		CostTargets: []cost.AccountTarget{{
			AccountID:   "111111111111",
			DisplayName: "linked",
			AWSConfig:   aws.Config{Region: "us-east-1"},
		}},
		Workers: 1,
		Now:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		listTags: func(context.Context, aws.Config, string) ([]coreaccount.Tag, error) {
			return []coreaccount.Tag{{Key: "owner", Value: "jdoe"}}, nil
		},
		fetchMonthly: func(_ context.Context, q cost.CostQuery) ([]cost.AccountMonthlyCosts, error) {
			out := make([]cost.AccountMonthlyCosts, len(q.Accounts))
			for i, acct := range q.Accounts {
				out[i] = cost.AccountMonthlyCosts{AccountID: acct.AccountID}
			}
			return out, nil
		},
		scanInventory: func(context.Context, inventory.Query) (inventory.Result, error) {
			scanCalls++
			return inventory.Result{}, fmt.Errorf("at least one account target is required")
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if scanCalls != 0 {
		t.Fatalf("scanInventory called %d times, want 0", scanCalls)
	}
	if len(result.Reports) != 1 || result.Reports[0].AccountID != "111111111111" {
		t.Fatalf("reports = %+v", result.Reports)
	}
	if result.Reports[0].OwnerEmail != "jdoe@redhat.com" {
		t.Fatalf("owner email = %q", result.Reports[0].OwnerEmail)
	}
}

func TestBuildListsTagsWithEachTargetConfig(t *testing.T) {
	t.Parallel()

	var listed []string
	_, err := Build(context.Background(), BuildInput{
		CostTargets: []cost.AccountTarget{
			{
				AccountID:      "111111111111",
				PayerAccountID: "123456789012",
				AWSConfig:      aws.Config{Region: "payer-a"},
			},
			{
				AccountID:      "222222222222",
				PayerAccountID: "987654321098",
				AWSConfig:      aws.Config{Region: "payer-b"},
			},
		},
		Workers: 1,
		Now:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		listTags: func(_ context.Context, cfg aws.Config, accountID string) ([]coreaccount.Tag, error) {
			listed = append(listed, cfg.Region+":"+accountID)
			return []coreaccount.Tag{{Key: "owner", Value: "jdoe"}}, nil
		},
		fetchMonthly: func(_ context.Context, q cost.CostQuery) ([]cost.AccountMonthlyCosts, error) {
			out := make([]cost.AccountMonthlyCosts, len(q.Accounts))
			for i, acct := range q.Accounts {
				out[i] = cost.AccountMonthlyCosts{AccountID: acct.AccountID}
			}
			return out, nil
		},
		scanInventory: func(context.Context, inventory.Query) (inventory.Result, error) {
			t.Fatal("scanInventory should not run without inventory targets")
			return inventory.Result{}, nil
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"payer-a:111111111111", "payer-b:222222222222"}
	if len(listed) != len(want) {
		t.Fatalf("listed = %v, want %v", listed, want)
	}
	for i, got := range listed {
		if got != want[i] {
			t.Fatalf("listed[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func TestBuildAppliesExcludeRecentDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	want, err := cost.ResolvePeriod(cost.PeriodSpec{Months: 6, ExcludeRecentDays: 2}, now)
	if err != nil {
		t.Fatal(err)
	}
	var gotRange cost.DateRange
	_, err = Build(context.Background(), BuildInput{
		CostTargets: []cost.AccountTarget{{
			AccountID: "111111111111",
			AWSConfig: aws.Config{Region: "us-east-1"},
		}},
		Months:            6,
		ExcludeRecentDays: 2,
		Workers:           1,
		Now:               now,
		listTags: func(context.Context, aws.Config, string) ([]coreaccount.Tag, error) {
			return []coreaccount.Tag{{Key: "owner", Value: "jdoe"}}, nil
		},
		fetchMonthly: func(_ context.Context, q cost.CostQuery) ([]cost.AccountMonthlyCosts, error) {
			gotRange = q.Range
			return []cost.AccountMonthlyCosts{{AccountID: "111111111111"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !gotRange.Start.Equal(want.Start) || !gotRange.End.Equal(want.End) {
		t.Fatalf("range = %s/%s, want %s/%s", gotRange.Start, gotRange.End, want.Start, want.End)
	}
}

func TestBuildContinuesWhenListTagsFailsForOneAccount(t *testing.T) {
	t.Parallel()

	result, err := Build(context.Background(), BuildInput{
		CostTargets: []cost.AccountTarget{
			{AccountID: "111111111111", AWSConfig: aws.Config{Region: "us-east-1"}},
			{AccountID: "222222222222", AWSConfig: aws.Config{Region: "us-east-1"}},
		},
		Workers: 1,
		Now:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		listTags: func(_ context.Context, _ aws.Config, accountID string) ([]coreaccount.Tag, error) {
			if accountID == "111111111111" {
				return nil, fmt.Errorf("throttled")
			}
			return []coreaccount.Tag{{Key: "owner", Value: "jdoe"}}, nil
		},
		fetchMonthly: func(_ context.Context, q cost.CostQuery) ([]cost.AccountMonthlyCosts, error) {
			out := make([]cost.AccountMonthlyCosts, len(q.Accounts))
			for i, acct := range q.Accounts {
				out[i] = cost.AccountMonthlyCosts{AccountID: acct.AccountID}
			}
			return out, nil
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(result.Reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(result.Reports))
	}
	if !strings.Contains(result.Reports[0].OwnerError, "list tags") {
		t.Fatalf("owner error = %q", result.Reports[0].OwnerError)
	}
	if result.Reports[1].OwnerEmail != "jdoe@redhat.com" {
		t.Fatalf("second owner = %q", result.Reports[1].OwnerEmail)
	}
}

func TestBuildReturnsContextAbortFromListTags(t *testing.T) {
	t.Parallel()

	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(want.Error(), func(t *testing.T) {
			t.Parallel()
			abort := want
			result, err := Build(context.Background(), BuildInput{
				CostTargets: []cost.AccountTarget{
					{AccountID: "111111111111", AWSConfig: aws.Config{Region: "us-east-1"}},
					{AccountID: "222222222222", AWSConfig: aws.Config{Region: "us-east-1"}},
				},
				Workers: 1,
				Now:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
				listTags: func(_ context.Context, _ aws.Config, accountID string) ([]coreaccount.Tag, error) {
					if accountID == "111111111111" {
						return nil, abort
					}
					return []coreaccount.Tag{{Key: "owner", Value: "jdoe"}}, nil
				},
				fetchMonthly: func(_ context.Context, q cost.CostQuery) ([]cost.AccountMonthlyCosts, error) {
					out := make([]cost.AccountMonthlyCosts, len(q.Accounts))
					for i, acct := range q.Accounts {
						out[i] = cost.AccountMonthlyCosts{AccountID: acct.AccountID}
					}
					return out, nil
				},
			})
			if !errors.Is(err, abort) {
				t.Fatalf("Build() error = %v, want %v", err, abort)
			}
			if len(result.Reports) != 0 {
				t.Fatalf("got partial BuildResult with %d reports, want none", len(result.Reports))
			}
		})
	}
}

func TestBuildInventoryErrorIncludesGlobalWarnings(t *testing.T) {
	t.Parallel()

	result, err := Build(context.Background(), BuildInput{
		CostTargets: []cost.AccountTarget{{
			AccountID: "111111111111",
			AWSConfig: aws.Config{Region: "us-east-1"},
		}},
		InventoryTargets: []inventory.AccountTarget{{AccountID: "111111111111"}},
		Workers:          1,
		Now:              time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		listTags: func(context.Context, aws.Config, string) ([]coreaccount.Tag, error) {
			return []coreaccount.Tag{{Key: "owner", Value: "jdoe"}}, nil
		},
		fetchMonthly: func(_ context.Context, q cost.CostQuery) ([]cost.AccountMonthlyCosts, error) {
			return []cost.AccountMonthlyCosts{{AccountID: "111111111111"}}, nil
		},
		scanInventory: func(context.Context, inventory.Query) (inventory.Result, error) {
			return inventory.Result{Accounts: []inventory.AccountInventory{{
				AccountID: "111111111111",
				Warnings:  []string{"route53: access denied"},
				S3Buckets: []inventory.S3Bucket{{Name: "keep-me", Region: "us-east-1"}},
			}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !strings.Contains(result.Reports[0].InventoryError, "route53: access denied") {
		t.Fatalf("inventory error = %q", result.Reports[0].InventoryError)
	}
	if len(result.Reports[0].Inventory.S3Buckets) != 1 {
		t.Fatalf("s3 buckets = %+v", result.Reports[0].Inventory.S3Buckets)
	}
}

func TestBuildContinuesWhenInventoryScanFailsForOneAccount(t *testing.T) {
	t.Parallel()

	result, err := Build(context.Background(), BuildInput{
		CostTargets: []cost.AccountTarget{
			{AccountID: "111111111111", AWSConfig: aws.Config{Region: "us-east-1"}},
			{AccountID: "222222222222", AWSConfig: aws.Config{Region: "us-east-1"}},
		},
		InventoryTargets: []inventory.AccountTarget{
			{AccountID: "111111111111"},
			{AccountID: "222222222222"},
		},
		Workers: 1,
		Now:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		listTags: func(context.Context, aws.Config, string) ([]coreaccount.Tag, error) {
			return []coreaccount.Tag{{Key: "owner", Value: "jdoe"}}, nil
		},
		fetchMonthly: func(_ context.Context, q cost.CostQuery) ([]cost.AccountMonthlyCosts, error) {
			out := make([]cost.AccountMonthlyCosts, len(q.Accounts))
			for i, acct := range q.Accounts {
				out[i] = cost.AccountMonthlyCosts{AccountID: acct.AccountID}
			}
			return out, nil
		},
		scanInventory: func(context.Context, inventory.Query) (inventory.Result, error) {
			return inventory.Result{Accounts: []inventory.AccountInventory{
				{AccountID: "111111111111", Warnings: []string{"list regions: access denied"}},
				{AccountID: "222222222222", S3Buckets: []inventory.S3Bucket{{Name: "ok", Region: "us-east-1"}}},
			}}, nil
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(result.Reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(result.Reports))
	}
	if !strings.Contains(result.Reports[0].InventoryError, "list regions") {
		t.Fatalf("first inventory error = %q", result.Reports[0].InventoryError)
	}
	if result.Reports[1].InventoryError != "" || len(result.Reports[1].Inventory.S3Buckets) != 1 {
		t.Fatalf("second report = %+v", result.Reports[1])
	}
}
