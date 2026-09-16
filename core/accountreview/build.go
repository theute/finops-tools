package accountreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	coreaccount "github.com/openshift-online/finops-tools/core/account"
	"github.com/openshift-online/finops-tools/core/cost"
	"github.com/openshift-online/finops-tools/core/inventory"
	"github.com/openshift-online/finops-tools/core/parallel"
)

const maxTagListWorkers = 5

// BuildInput describes an account review build request.
type BuildInput struct {
	// CostTargets are the accounts to include. Each target's AWSConfig is used
	// for that account's Organizations ListTags and Cost Explorer queries.
	CostTargets []cost.AccountTarget
	// InventoryTargets, when empty, skips inventory.Scan entirely (cost-only reports).
	InventoryTargets []inventory.AccountTarget
	// EmailDomain is appended when the owner tag has no '@' (default @redhat.com).
	EmailDomain       string
	Months            int
	ExcludeRecentDays int
	Workers           int
	// OUPaths maps account ID to a "Root / Parent / OU" label for the email.
	OUPaths  map[string]string
	Now      time.Time
	Progress cost.FetchProgress

	listTags      func(context.Context, aws.Config, string) ([]coreaccount.Tag, error)
	fetchMonthly  func(context.Context, cost.CostQuery) ([]cost.AccountMonthlyCosts, error)
	scanInventory func(context.Context, inventory.Query) (inventory.Result, error)
}

// Build gathers account metadata, costs, and inventory for all targets.
// Per-account ListTags, Cost Explorer, and inventory failures are recorded on
// the corresponding AccountReport. Context cancellation or deadline still aborts the run.
func Build(ctx context.Context, in BuildInput) (BuildResult, error) {
	in = in.withDefaults()
	if len(in.CostTargets) == 0 {
		return BuildResult{}, fmt.Errorf("at least one account target is required")
	}

	dr, err := cost.ResolvePeriod(cost.PeriodSpec{
		Months:            in.Months,
		ExcludeRecentDays: in.ExcludeRecentDays,
	}, in.Now)
	if err != nil {
		return BuildResult{}, fmt.Errorf("cost period: %w", err)
	}
	monthlyByAccount, err := in.fetchMonthly(ctx, cost.CostQuery{
		Provider: cost.ProviderAWS,
		Accounts: in.CostTargets,
		Range:    dr,
		Workers:  in.Workers,
		Progress: in.Progress,
	})
	if err != nil {
		return BuildResult{}, fmt.Errorf("fetch monthly costs: %w", err)
	}
	monthlyMap := make(map[string]cost.AccountMonthlyCosts, len(monthlyByAccount))
	for _, m := range monthlyByAccount {
		monthlyMap[m.AccountID] = m
	}

	invMap := map[string]inventory.AccountInventory{}
	if len(in.InventoryTargets) > 0 {
		if in.Progress != nil {
			in.Progress.Step(fmt.Sprintf("Scanning inventory for %d account(s)…", len(in.InventoryTargets)))
		}
		var onInventoryProgress func(string)
		if in.Progress != nil {
			onInventoryProgress = in.Progress.Step
		}
		invResult, scanErr := in.scanInventory(ctx, inventory.Query{
			Targets:    in.InventoryTargets,
			Workers:    in.Workers,
			Now:        in.Now,
			OnProgress: onInventoryProgress,
		})
		if scanErr != nil {
			return BuildResult{}, fmt.Errorf("scan inventory: %w", scanErr)
		}
		invMap = make(map[string]inventory.AccountInventory, len(invResult.Accounts))
		for _, inv := range invResult.Accounts {
			invMap[inv.AccountID] = inv
		}
	}

	tagSets := make([][]coreaccount.Tag, len(in.CostTargets))
	tagErrs := make([]error, len(in.CostTargets))
	tagWorkers := in.Workers
	if tagWorkers > maxTagListWorkers {
		tagWorkers = maxTagListWorkers
	}
	if in.Progress != nil && len(in.CostTargets) > 1 {
		in.Progress.Step(fmt.Sprintf("Listing organization tags for %d account(s)…", len(in.CostTargets)))
	}
	if err := parallel.ForEach(ctx, tagWorkers, len(in.CostTargets), func(ctx context.Context, i int) error {
		ct := in.CostTargets[i]
		accountID := strings.TrimSpace(ct.AccountID)
		reportTagProgress(in.Progress, ct, i+1, len(in.CostTargets))
		tags, tagErr := in.listTags(ctx, ct.AWSConfig, accountID)
		if tagErr != nil {
			if isContextAbort(tagErr) {
				return tagErr
			}
			// Keep building the rest of the reports; this account will have OwnerError set.
			tagErrs[i] = tagErr
			return nil
		}
		tagSets[i] = tags
		return nil
	}); err != nil {
		return BuildResult{}, err
	}

	reports := make([]AccountReport, len(in.CostTargets))
	err = parallel.ForEach(ctx, in.Workers, len(in.CostTargets), func(ctx context.Context, i int) error {
		ct := in.CostTargets[i]
		report, buildErr := buildAccountReport(ctx, in, ct, tagSets[i], tagErrs[i], monthlyMap, invMap)
		if buildErr != nil {
			return buildErr
		}
		reports[i] = report
		return nil
	})
	if err != nil {
		return BuildResult{}, err
	}
	return BuildResult{Reports: reports}, nil
}

func (in BuildInput) withDefaults() BuildInput {
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	if in.Months <= 0 {
		in.Months = 6
	}
	if in.listTags == nil {
		in.listTags = coreaccount.ListTags
	}
	if in.fetchMonthly == nil {
		in.fetchMonthly = cost.FetchMonthly
	}
	if in.scanInventory == nil {
		in.scanInventory = inventory.Scan
	}
	return in
}

func buildAccountReport(
	ctx context.Context,
	in BuildInput,
	ct cost.AccountTarget,
	tags []coreaccount.Tag,
	tagErr error,
	monthlyMap map[string]cost.AccountMonthlyCosts,
	invMap map[string]inventory.AccountInventory,
) (AccountReport, error) {
	accountID := strings.TrimSpace(ct.AccountID)

	report := AccountReport{
		AccountID:    accountID,
		AccountName:  ct.AccountDisplayName(),
		DisplayAlias: strings.TrimSpace(ct.DisplayAlias),
		Tags:         tags,
		MonthlyCosts: monthlyMap[accountID],
		Inventory:    invMap[accountID],
		GeneratedAt:  in.Now,
	}
	if in.OUPaths != nil {
		report.OUPath = in.OUPaths[accountID]
	}

	if tagErr != nil {
		report.OwnerError = fmt.Sprintf("list tags: %v", tagErr)
	} else {
		ownerEmail, ownerErr := ResolveOwnerEmail(tags, in.EmailDomain)
		if ownerErr != nil {
			report.OwnerError = ownerErr.Error()
		} else {
			report.OwnerEmail = ownerEmail
		}
	}

	report.InventoryError = inventoryScanError(report.Inventory)
	return report, nil
}

// inventoryScanError joins regional API failures and account-level warnings
// (Route53/S3, credential/region-list errors) into one email-facing string.
func inventoryScanError(inv inventory.AccountInventory) string {
	var parts []string
	for _, w := range inv.SkippedRegions {
		msg := strings.TrimSpace(w.Message)
		if msg == "" {
			continue
		}
		if region := strings.TrimSpace(w.Region); region != "" {
			parts = append(parts, region+": "+msg)
		} else {
			parts = append(parts, msg)
		}
	}
	for _, w := range inv.Warnings {
		if s := strings.TrimSpace(w); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "; ")
}

func isContextAbort(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func reportTagProgress(progress cost.FetchProgress, target cost.AccountTarget, index, total int) {
	if progress == nil || !parallel.ShouldReportProgress(index, total) {
		return
	}
	label := strings.TrimSpace(target.AccountID)
	if name := target.AccountDisplayName(); name != "" && name != label {
		label = fmt.Sprintf("%s (%s)", name, label)
	}
	progress.Step(fmt.Sprintf("Listing organization tags for %s [%d/%d]…", label, index, total))
}

// GroupReports partitions reports for email delivery.
// Accounts with an empty OwnerEmail are returned as DeliveryResult failures
// instead of groups, so they appear in the summary and are not emailed.
func GroupReports(reports []AccountReport, groupBy GroupBy) ([]OwnerGroup, []DeliveryResult) {
	if groupBy == GroupByOwner {
		return groupByOwner(reports)
	}
	return groupByAccount(reports)
}

func groupByAccount(reports []AccountReport) ([]OwnerGroup, []DeliveryResult) {
	groups := make([]OwnerGroup, 0, len(reports))
	var failures []DeliveryResult
	for _, r := range reports {
		if fail, ok := ownerDeliveryFailure(r); ok {
			failures = append(failures, fail)
			continue
		}
		groups = append(groups, OwnerGroup{
			OwnerEmail: r.OwnerEmail,
			Reports:    []AccountReport{r},
		})
	}
	return groups, failures
}

func groupByOwner(reports []AccountReport) ([]OwnerGroup, []DeliveryResult) {
	byOwner := make(map[string][]AccountReport)
	var failures []DeliveryResult
	for _, r := range reports {
		if fail, ok := ownerDeliveryFailure(r); ok {
			failures = append(failures, fail)
			continue
		}
		byOwner[r.OwnerEmail] = append(byOwner[r.OwnerEmail], r)
	}
	groups := make([]OwnerGroup, 0, len(byOwner))
	for email, reps := range byOwner {
		groups = append(groups, OwnerGroup{OwnerEmail: email, Reports: reps})
	}
	return groups, failures
}

// ownerDeliveryFailure reports accounts that cannot be emailed because owner
// resolution failed. Inventory and cost warnings still appear on emailed reports.
func ownerDeliveryFailure(r AccountReport) (DeliveryResult, bool) {
	if r.OwnerEmail != "" {
		return DeliveryResult{}, false
	}
	status, reason := OwnerErrorStatus(parseOwnerError(r.OwnerError))
	return DeliveryResult{
		AccountID: r.AccountID,
		Status:    status,
		Reason:    reason,
	}, true
}

func parseOwnerError(msg string) error {
	if msg == "" {
		return ErrOwnerTagMissing
	}
	switch {
	case strings.Contains(msg, ErrOwnerTagMissing.Error()):
		return ErrOwnerTagMissing
	case strings.Contains(msg, ErrOwnerTagEmpty.Error()):
		return ErrOwnerTagEmpty
	case strings.Contains(msg, ErrOwnerEmailInvalid.Error()):
		return ErrOwnerEmailInvalid
	default:
		return fmt.Errorf("%s", msg)
	}
}
