// snapshot_list.go implements "finops snapshot list" to find stale EBS and RDS snapshots.
package cmd

import (
	"fmt"
	"sort"
	"time"

	"github.com/openshift-online/finops-tools/cli/internal/configstore"
	"github.com/openshift-online/finops-tools/cli/internal/output"
	"github.com/openshift-online/finops-tools/cli/internal/progress"
	"github.com/openshift-online/finops-tools/core/cost"
	"github.com/openshift-online/finops-tools/core/snapshot"
	"github.com/spf13/cobra"
)

var (
	snapshotListAccount         string
	snapshotListAccountAliases  string
	snapshotListFormat          string
	snapshotListOutput          string
	snapshotListMinSizeGiB      float64
	snapshotListOlderThanDays   int
	snapshotListOU              string
	snapshotListPayer           string
	snapshotListQuiet           bool
	snapshotListRegions         string
	snapshotListRole            string
	snapshotListSkipOrgCache    bool
	snapshotListRefreshOrgCache bool
	snapshotListTag             string
	snapshotListTypes           string
	snapshotListProvider        string
	snapshotListWorkers         int
	snapshotListFetch           = snapshot.Fetch
)

var snapshotListCmd = &cobra.Command{
	Use:   "list",
	Short: "List EBS and RDS snapshots with estimated storage costs",
	Long: `Discover EBS and RDS snapshots older than a cutoff and estimate monthly storage cost.

Account selection matches finops account get-cost: --account-id, --account-alias, --ou, --tag, or --payer alone.
Linked member accounts are scanned using role assumption from the payer.
Accounts that cannot be assumed into, or that fail credentialed API calls during the scan,
are skipped and listed under "Skipped accounts" in the output.

Cost estimates use incremental EBS snapshot chains where possible and RDS regional excess shares.
When Cost Explorer data is available, summary shows attributed storage cost for listed snapshots
and account-wide billed EBS/RDS snapshot storage.
Per-snapshot $/MO allocates billed cost proportionally; — on EBS means no incremental blocks.

Required IAM permissions in each scanned account:
  ec2:DescribeRegions, ec2:DescribeSnapshots
  rds:DescribeDBInstances, rds:DescribeDBClusters, rds:DescribeDBSnapshots, rds:DescribeDBClusterSnapshots

Payer credentials also need sts:AssumeRole into the configured linked-account role and
ce:GetCostAndUsage with LINKED_ACCOUNT scope for billed cost lines.

Examples:
  finops snapshot list --account-alias rh-control
  finops snapshot list --account-alias rh-control --older-than-days 365 --format json
  finops snapshot list --payer rh-control
  finops snapshot list --payer rh-control --tag organization
  finops snapshot list --ou ou-abcd-12345678 --payer rh-control --types ebs`,
	Args: cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, _ []string) error {
		sel, err := parseCostTargetSelector(
			snapshotListAccount, snapshotListAccountAliases, snapshotListOU, snapshotListPayer,
			snapshotListTag,
			snapshotListSkipOrgCache, snapshotListRefreshOrgCache,
		)
		if err != nil {
			return err
		}
		if _, err := validateCostTargetSelector(sel); err != nil {
			return err
		}
		if _, err := output.ParseFormat(snapshotListFormat); err != nil {
			return err
		}
		if _, err := cost.ParseProvider(snapshotListProvider); err != nil {
			return err
		}
		if snapshotListOlderThanDays <= 0 {
			return fmt.Errorf("--older-than-days must be positive")
		}
		if snapshotListMinSizeGiB < 0 {
			return fmt.Errorf("--min-size-gib must be >= 0")
		}
		if _, err := snapshot.ParseTypes(snapshotListTypes); err != nil {
			return err
		}
		if _, err := snapshot.ParseRegions(snapshotListRegions); err != nil {
			return err
		}
		if err := validateWorkers(snapshotListWorkers); err != nil {
			return err
		}
		return validateOrgCacheFlags(snapshotListSkipOrgCache, snapshotListRefreshOrgCache)
	},
	RunE: runSnapshotList,
}

func init() {
	snapshotCmd.AddCommand(snapshotListCmd)
	bindAWSTargetFlags(snapshotListCmd, awsTargetFlagRefs{
		Account:         &snapshotListAccount,
		AccountAliases:  &snapshotListAccountAliases,
		OU:              &snapshotListOU,
		Payer:           &snapshotListPayer,
		Tag:             &snapshotListTag,
		SkipOrgCache:    &snapshotListSkipOrgCache,
		RefreshOrgCache: &snapshotListRefreshOrgCache,
	})
	snapshotListCmd.Flags().IntVar(&snapshotListOlderThanDays, "older-than-days", snapshot.DefaultOlderThanDays, "List snapshots older than this many days")
	snapshotListCmd.Flags().StringVar(&snapshotListTypes, "types", "ebs,rds", "Snapshot types to scan: ebs, rds (comma-separated)")
	snapshotListCmd.Flags().StringVar(&snapshotListRegions, "regions", "", "Limit scan to comma-separated AWS regions (default: all enabled regions)")
	snapshotListCmd.Flags().Float64Var(&snapshotListMinSizeGiB, "min-size-gib", 0, "Skip snapshots smaller than this size in GiB")
	snapshotListCmd.Flags().StringVar(&snapshotListFormat, "format", string(output.FormatPrettyPrint),
		"Output format: pretty-print, json, csv")
	addOutputFlag(snapshotListCmd, &snapshotListOutput)
	snapshotListCmd.Flags().StringVar(&snapshotListProvider, "provider", string(cost.ProviderAWS),
		"Cloud provider: aws or gcp")
	bindLinkedRoleFlag(snapshotListCmd, &snapshotListRole)
	snapshotListCmd.Flags().BoolVar(&snapshotListQuiet, "quiet", false, "Suppress progress messages on stderr")
	bindWorkersFlag(snapshotListCmd, &snapshotListWorkers, "")
}

func runSnapshotList(cmd *cobra.Command, _ []string) error {
	format, err := output.ParseFormat(snapshotListFormat)
	if err != nil {
		return err
	}
	provider, err := cost.ParseProvider(snapshotListProvider)
	if err != nil {
		return err
	}
	if provider != cost.ProviderAWS {
		return fmt.Errorf("only AWS is supported for snapshot list today")
	}
	types, err := snapshot.ParseTypes(snapshotListTypes)
	if err != nil {
		return err
	}
	regions, err := snapshot.ParseRegions(snapshotListRegions)
	if err != nil {
		return err
	}

	cfgPath, err := configstore.ResolvePath(awsFlags.ConfigPath)
	if err != nil {
		return err
	}
	cfg, err := configstore.Load(cfgPath)
	if err != nil {
		return err
	}

	status := progress.New(cmd.ErrOrStderr(), snapshotListQuiet)

	sel, err := parseCostTargetSelector(
		snapshotListAccount, snapshotListAccountAliases, snapshotListOU, snapshotListPayer,
		snapshotListTag,
		snapshotListSkipOrgCache, snapshotListRefreshOrgCache,
	)
	if err != nil {
		return err
	}

	// costTargets are the selected accounts (IDs + payer credential mapping).
	// scanTargets are the subset we can assume into for EC2/RDS API scans.
	costTargets, err := resolveCostTargets(
		cmd, cfg, &sel,
		awsFlags.ConfigPath, awsFlags.CredentialsFile, awsFlags.AuthMethod,
		status,
	)
	if err != nil {
		return err
	}

	out, closeOut, err := resolveCommandOutput(cmd, snapshotListOutput)
	if err != nil {
		return err
	}
	if closeOut != nil {
		defer closeOut()
	}

	if len(costTargets) == 0 {
		return output.WriteSnapshotListResult(out, format, snapshot.Result{
			Summary: snapshot.Summary{
				OlderThanDays:  snapshotListOlderThanDays,
				CostDisclaimer: "Estimates use volume or allocated size; actual EBS snapshot billing may be lower.",
			},
		})
	}

	status.Step("Ensuring AWS credentials…")
	awsCtx := awsCommandContext(cmd)
	if err := ensureSnapshotCredentials(cmd, cfg, costTargets, awsFlags.ConfigPath, awsFlags.CredentialsFile, awsFlags.AuthMethod); err != nil {
		return err
	}
	if len(costTargets) <= 1 {
		status.Step("Preparing account configuration…")
	}
	prepareBar := progress.NewBar(cmd.ErrOrStderr(), snapshotListQuiet, "Preparing account configuration…", len(costTargets))
	scanTargets, skippedAccounts, err := prepareSnapshotTargets(
		cmd, cfg, costTargets,
		awsFlags.CredentialsFile, awsFlags.ConfigPath, snapshotListRole,
		snapshotListWorkers,
		prepareBar,
	)
	if err != nil {
		return err
	}
	if len(scanTargets) == 0 {
		return output.WriteSnapshotListResult(out, format, snapshot.Result{
			Summary: snapshot.Summary{
				OlderThanDays:   snapshotListOlderThanDays,
				SkippedAccounts: skippedAccounts,
				CostDisclaimer:  "Estimates use volume or allocated size; actual EBS snapshot billing may be lower.",
			},
		})
	}

	if len(scanTargets) <= 1 {
		status.Step("Scanning account for snapshots…")
	}
	scanBar := progress.NewBar(cmd.ErrOrStderr(), snapshotListQuiet, "Scanning accounts for snapshots…", len(scanTargets))
	result, err := snapshotListFetch(awsCtx, snapshot.Query{
		Targets:         scanTargets,
		OlderThan:       time.Duration(snapshotListOlderThanDays) * 24 * time.Hour,
		Types:           types,
		Regions:         regions,
		MinSizeGiB:      snapshotListMinSizeGiB,
		AccountProgress: scanBar,
		Workers:         snapshotListWorkers,
	})
	// Finish before the next status line so interactive redraws end with a newline.
	if scanBar != nil {
		scanBar.Finish()
	}
	if err != nil {
		return err
	}
	result.Summary.SkippedAccounts = mergeSnapshotSkippedAccounts(skippedAccounts, result.Summary.SkippedAccounts)

	// Cost Explorer runs on payer credentials and only needs account IDs from
	// costTargets (not scanTargets), so skipped assume-role accounts stay in scope.
	status.Step("Fetching billed snapshot costs from Cost Explorer…")
	billed, err := fetchSnapshotBilledCosts(awsCtx, cfg, costTargets, awsFlags.CredentialsFile, time.Now().UTC(), snapshotListWorkers)
	if err != nil {
		status.Step(fmt.Sprintf("Warning: billed snapshot costs unavailable: %v", err))
	} else {
		result.Summary.BilledCosts = billed
	}

	return output.WriteSnapshotListResult(out, format, result)
}

func mergeSnapshotSkippedAccounts(prepare, scan []snapshot.AccountWarning) []snapshot.AccountWarning {
	if len(prepare) == 0 && len(scan) == 0 {
		return nil
	}
	out := make([]snapshot.AccountWarning, 0, len(prepare)+len(scan))
	out = append(out, prepare...)
	out = append(out, scan...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		return out[i].Message < out[j].Message
	})
	return out
}
