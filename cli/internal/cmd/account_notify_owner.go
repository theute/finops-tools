// account_notify_owner.go implements "finops account notify-owner".
package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/openshift-online/finops-tools/cli/internal/configstore"
	"github.com/openshift-online/finops-tools/cli/internal/notify"
	"github.com/openshift-online/finops-tools/cli/internal/output"
	"github.com/openshift-online/finops-tools/cli/internal/progress"
	"github.com/openshift-online/finops-tools/core/accountreview"
	"github.com/openshift-online/finops-tools/core/cost"
	"github.com/spf13/cobra"
)

var (
	notifyOwnerAccount           string
	notifyOwnerAccountAliases    string
	notifyOwnerFormat            string
	notifyOwnerGroupBy           string
	notifyOwnerMonths            int
	notifyOwnerExcludeRecentDays int
	notifyOwnerOU                string
	notifyOwnerPayer             string
	notifyOwnerQuiet             bool
	notifyOwnerRedirectPrefix    string
	notifyOwnerRole              string
	notifyOwnerSend              bool
	notifyOwnerSkipOrgCache      bool
	notifyOwnerRefreshOrgCache   bool
	notifyOwnerTag               string
	notifyOwnerWorkers           int
	notifyOwnerYes               bool
	accountReviewBuild           = accountreview.Build
)

var accountNotifyOwnerCmd = &cobra.Command{
	Use:   "notify-owner",
	Short: "Email account owners with cost and inventory for delete/keep decisions",
	Long: `Gather AWS account cost trends and resource inventory, then notify the owner.

The owner email is derived from the Organizations account tag (default key: owner),
appending @redhat.com when the tag value has no @ sign.

Account selection matches finops account get-cost: --account-id, --account-alias,
--ou, --tag, or --payer alone. Linked member accounts are scanned using role
assumption from the payer.

By default the command builds reports and prints a delivery plan without sending
email. To send via Gmail, pass --send with either --yes (owner addresses) or
--redirect-prefix (PREFIX+<owner>@redhat.com test delivery).

A Cost Explorer, inventory, or owner-tag failure on one account is recorded for
that account and does not stop the rest of the run.

Gmail uses gcloud Application Default Credentials (finops does not modify ADC). Verify access:
  finops config gmail login

Examples:
  finops account notify-owner --account-alias my-linked
  finops account notify-owner --payer rh-control --account-id 111111111111 --send --redirect-prefix finops
  finops account notify-owner --payer rh-control --ou ou-abcd-12345678 --group-by owner --send --yes
  finops account notify-owner --payer rh-control --tag owner --format json`,
	Args: cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, _ []string) error {
		sel, err := parseCostTargetSelector(
			notifyOwnerAccount, notifyOwnerAccountAliases, notifyOwnerOU, notifyOwnerPayer,
			notifyOwnerTag,
			notifyOwnerSkipOrgCache, notifyOwnerRefreshOrgCache,
		)
		if err != nil {
			return err
		}
		if _, err := validateCostTargetSelector(sel); err != nil {
			return err
		}
		if _, err := accountreview.ParseGroupBy(notifyOwnerGroupBy); err != nil {
			return err
		}
		if _, err := parseNotifySummaryFormat(notifyOwnerFormat); err != nil {
			return err
		}
		if notifyOwnerMonths <= 0 {
			return fmt.Errorf("--months must be positive")
		}
		if notifyOwnerExcludeRecentDays < 0 {
			return fmt.Errorf("--exclude-recent-days must be >= 0")
		}
		if err := validateWorkers(notifyOwnerWorkers); err != nil {
			return err
		}
		if err := validateOrgCacheFlags(notifyOwnerSkipOrgCache, notifyOwnerRefreshOrgCache); err != nil {
			return err
		}
		return validateNotifyOwnerSendFlags(notifyOwnerSend, notifyOwnerYes, notifyOwnerRedirectPrefix)
	},
	RunE: runAccountNotifyOwner,
}

func init() {
	accountCmd.AddCommand(accountNotifyOwnerCmd)
	bindAWSTargetFlags(accountNotifyOwnerCmd, awsTargetFlagRefs{
		Account:         &notifyOwnerAccount,
		AccountAliases:  &notifyOwnerAccountAliases,
		OU:              &notifyOwnerOU,
		Payer:           &notifyOwnerPayer,
		Tag:             &notifyOwnerTag,
		SkipOrgCache:    &notifyOwnerSkipOrgCache,
		RefreshOrgCache: &notifyOwnerRefreshOrgCache,
	})
	accountNotifyOwnerCmd.Flags().BoolVar(&notifyOwnerSend, "send", false, "Send emails via Gmail (default: plan only)")
	accountNotifyOwnerCmd.Flags().BoolVar(&notifyOwnerYes, "yes", false, "With --send, deliver to resolved owner emails")
	accountNotifyOwnerCmd.Flags().StringVar(&notifyOwnerRedirectPrefix, "redirect-prefix", "",
		"With --send, deliver to PREFIX+<owner>@redhat.com instead of owner addresses")
	accountNotifyOwnerCmd.Flags().StringVar(&notifyOwnerGroupBy, "group-by", string(accountreview.GroupByAccount), "Group emails by account or owner")
	accountNotifyOwnerCmd.Flags().IntVar(&notifyOwnerMonths, "months", 6, "Number of calendar months of cost history to include")
	accountNotifyOwnerCmd.Flags().IntVar(&notifyOwnerExcludeRecentDays, "exclude-recent-days", 0,
		"Omit the last N UTC days from the cost end anchor (AWS CE lag; default 0, or defaults.cost.exclude_recent_days)")
	accountNotifyOwnerCmd.Flags().StringVar(&notifyOwnerFormat, "format", string(output.FormatPrettyPrint), "Summary output format: pretty-print, json")
	bindLinkedRoleFlag(accountNotifyOwnerCmd, &notifyOwnerRole)
	accountNotifyOwnerCmd.Flags().BoolVar(&notifyOwnerQuiet, "quiet", false, "Suppress progress messages on stderr")
	bindWorkersFlag(accountNotifyOwnerCmd, &notifyOwnerWorkers, "")
}

// parseNotifySummaryFormat accepts only formats WriteNotifySummary can render.
// output.ParseFormat also allows csv, which this command rejects before AWS work.
func parseNotifySummaryFormat(s string) (output.Format, error) {
	format, err := output.ParseFormat(s)
	if err != nil {
		return "", err
	}
	switch format {
	case output.FormatPrettyPrint, output.FormatJSON:
		return format, nil
	default:
		return "", fmt.Errorf("notify summary supports pretty-print and json only")
	}
}

// validateNotifyOwnerSendFlags requires an explicit delivery choice with --send
// so a dry-run cannot accidentally email owners.
func validateNotifyOwnerSendFlags(send, yes bool, redirectPrefix string) error {
	redirectPrefix = strings.TrimSpace(redirectPrefix)
	if redirectPrefix != "" {
		if err := notify.ValidateRedirectPrefix(redirectPrefix); err != nil {
			return err
		}
	}
	if yes && !send {
		return fmt.Errorf("--yes requires --send")
	}
	if yes && redirectPrefix != "" {
		return fmt.Errorf("cannot use --yes with --redirect-prefix")
	}
	if !send {
		return nil
	}
	if yes || redirectPrefix != "" {
		return nil
	}
	return fmt.Errorf("refusing to send: pass --yes for owner addresses or --redirect-prefix PREFIX for test delivery")
}

func runAccountNotifyOwner(cmd *cobra.Command, _ []string) error {
	format, err := parseNotifySummaryFormat(notifyOwnerFormat)
	if err != nil {
		return err
	}
	groupBy, err := accountreview.ParseGroupBy(notifyOwnerGroupBy)
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
	if err := applyExcludeRecentDaysDefault(cmd, cfg, &notifyOwnerExcludeRecentDays); err != nil {
		return err
	}

	status := progress.New(cmd.ErrOrStderr(), notifyOwnerQuiet)
	sel, err := parseCostTargetSelector(
		notifyOwnerAccount, notifyOwnerAccountAliases, notifyOwnerOU, notifyOwnerPayer,
		notifyOwnerTag,
		notifyOwnerSkipOrgCache, notifyOwnerRefreshOrgCache,
	)
	if err != nil {
		return err
	}

	costTargets, err := resolveCostTargets(
		cmd, cfg, &sel,
		awsFlags.ConfigPath, awsFlags.CredentialsFile, awsFlags.AuthMethod,
		status,
	)
	if err != nil {
		return err
	}

	summaryOut := cmd.OutOrStdout()

	deliveryResults := skippedFromEmptyTargets(costTargets)
	if len(costTargets) == 0 {
		return writeNotifyDeliverySummary(summaryOut, format, deliveryResults)
	}

	status.Step("Ensuring AWS credentials…")
	awsCtx := awsCommandContext(cmd)
	if err := ensureNotifyCredentials(cmd, cfg, costTargets, awsFlags.ConfigPath, awsFlags.CredentialsFile, awsFlags.AuthMethod); err != nil {
		return err
	}

	prepareCostBar := progress.NewBar(cmd.ErrOrStderr(), notifyOwnerQuiet, "Preparing account configuration…", len(costTargets))
	costTargets, err = prepareCostTargets(awsCtx, cfg, costTargets, awsFlags.CredentialsFile, prepareCostBar)
	if err != nil {
		return err
	}

	prepareInvBar := progress.NewBar(cmd.ErrOrStderr(), notifyOwnerQuiet, "Preparing inventory access…", len(costTargets))
	invTargets, skippedPrep, err := prepareNotifyTargets(
		cmd, cfg, costTargets,
		awsFlags.CredentialsFile, awsFlags.ConfigPath, notifyOwnerRole,
		notifyOwnerWorkers,
		prepareInvBar,
	)
	if err != nil {
		return err
	}
	for _, skip := range skippedPrep {
		deliveryResults = append(deliveryResults, accountreview.DeliveryResult{
			AccountID: skip.AccountID,
			Status:    accountreview.StatusSkipped,
			Reason:    skip.Message,
		})
	}
	skippedIDs := make(map[string]struct{}, len(skippedPrep))
	for _, skip := range skippedPrep {
		skippedIDs[skip.AccountID] = struct{}{}
	}

	status.Step("Resolving OU paths…")
	ouPaths := map[string]string{}
	buckets, hierarchy, _, ouErr := resolveAccountOUBuckets(awsCtx, cfg, sel, costTargets, awsFlags.CredentialsFile)
	if ouErr != nil {
		status.Step("Continuing without OU paths: " + ouErr.Error())
	} else {
		ouPaths = cost.FormatAccountOUPaths(buckets, hierarchy)
	}

	status.Step("Building account review reports…")
	buildResult, err := accountReviewBuild(awsCtx, accountreview.BuildInput{
		CostTargets:       costTargets,
		InventoryTargets:  invTargets,
		Months:            notifyOwnerMonths,
		ExcludeRecentDays: notifyOwnerExcludeRecentDays,
		Workers:           notifyOwnerWorkers,
		OUPaths:           ouPaths,
		Progress:          status,
	})
	if err != nil {
		return err
	}

	groups, ownerFailures := accountreview.GroupReports(filterSkippedReports(buildResult.Reports, skippedIDs), groupBy)
	deliveryResults = append(deliveryResults, ownerFailures...)

	var messages []notify.Message
	for _, group := range groups {
		var msg notify.Message
		if len(group.Reports) == 1 {
			msg = notify.RenderAccountEmail(group.Reports[0])
		} else {
			msg = notify.RenderOwnerGroupEmail(group)
		}
		messages = append(messages, msg)
	}

	redirectPrefix := strings.TrimSpace(notifyOwnerRedirectPrefix)
	if redirectPrefix != "" {
		messages, err = notify.ApplyRedirectPrefix(messages, redirectPrefix)
		if err != nil {
			return err
		}
	} else {
		messages = notify.ApplyOwnerRecipients(messages)
	}

	messageMeta := make([]struct {
		accountIDs []string
		ownerEmail string
	}, len(groups))
	for i, group := range groups {
		accountIDs := make([]string, len(group.Reports))
		for j, r := range group.Reports {
			accountIDs[j] = r.AccountID
		}
		messageMeta[i] = struct {
			accountIDs []string
			ownerEmail string
		}{accountIDs: accountIDs, ownerEmail: group.OwnerEmail}
	}

	if !notifyOwnerSend {
		for i, meta := range messageMeta {
			deliveryResults = append(deliveryResults, accountreview.DeliveryResult{
				AccountIDs: meta.accountIDs,
				OwnerEmail: messages[i].IntendedTo,
				Status:     accountreview.StatusPlanned,
				Reason:     plannedDeliveryReason(messages[i]),
			})
		}
	} else {
		status.Step("Sending owner notification emails…")
		sender, err := notify.NewGmailSender(cmd.Context())
		if err != nil {
			return err
		}
		for i, msg := range messages {
			meta := messageMeta[i]
			if err := sender.Send(cmd.Context(), msg); err != nil {
				deliveryResults = append(deliveryResults, accountreview.DeliveryResult{
					AccountIDs: meta.accountIDs,
					OwnerEmail: msg.IntendedTo,
					Status:     accountreview.StatusSendFailed,
					Reason:     err.Error(),
				})
				continue
			}
			deliveryResults = append(deliveryResults, accountreview.DeliveryResult{
				AccountIDs: meta.accountIDs,
				OwnerEmail: msg.IntendedTo,
				Status:     accountreview.StatusSent,
				Reason:     sentDeliveryReason(msg),
			})
		}
	}

	return notifyOwnerAfterSummary(writeNotifyDeliverySummary(summaryOut, format, deliveryResults), deliveryResults)
}

func plannedDeliveryReason(msg notify.Message) string {
	if msg.DeliveryTo != "" && msg.IntendedTo != "" && msg.DeliveryTo != msg.IntendedTo {
		return fmt.Sprintf("not sent; would deliver to %s (owner: %s)", msg.DeliveryTo, msg.IntendedTo)
	}
	if msg.IntendedTo != "" {
		return fmt.Sprintf("not sent; would deliver to %s", msg.IntendedTo)
	}
	return "not sent (pass --send --yes or --send --redirect-prefix)"
}

func sentDeliveryReason(msg notify.Message) string {
	if msg.DeliveryTo != "" && msg.IntendedTo != "" && msg.DeliveryTo != msg.IntendedTo {
		return fmt.Sprintf("sent to %s", msg.DeliveryTo)
	}
	return ""
}

// filterSkippedReports drops accounts whose inventory assume-role failed so they
// are not emailed after already being recorded as StatusSkipped.
func filterSkippedReports(reports []accountreview.AccountReport, skipped map[string]struct{}) []accountreview.AccountReport {
	if len(skipped) == 0 {
		return reports
	}
	out := make([]accountreview.AccountReport, 0, len(reports))
	for _, r := range reports {
		if _, ok := skipped[r.AccountID]; ok {
			continue
		}
		out = append(out, r)
	}
	return out
}

func skippedFromEmptyTargets(targets []cost.AccountTarget) []accountreview.DeliveryResult {
	if len(targets) > 0 {
		return nil
	}
	return []accountreview.DeliveryResult{{
		Status: accountreview.StatusSkipped,
		Reason: "no accounts matched the selection",
	}}
}

// notifyOwnerAfterSummary returns the summary-write error if set; otherwise a
// non-nil error when any result is StatusSendFailed so the command exits
// non-zero after the summary has already been written.
func notifyOwnerAfterSummary(summaryErr error, results []accountreview.DeliveryResult) error {
	if summaryErr != nil {
		return summaryErr
	}
	n := 0
	for _, r := range results {
		if r.Status == accountreview.StatusSendFailed {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	if n == 1 {
		return fmt.Errorf("1 notification failed to send")
	}
	return fmt.Errorf("%d notifications failed to send", n)
}

func writeNotifyDeliverySummary(w io.Writer, format output.Format, results []accountreview.DeliveryResult) error {
	planned := 0
	sent := 0
	failed := 0
	skipped := 0
	for _, r := range results {
		switch r.Status {
		case accountreview.StatusPlanned:
			planned++
		case accountreview.StatusSent:
			sent++
		case accountreview.StatusSkipped:
			skipped++
		case accountreview.StatusOwnerNotFound, accountreview.StatusInvalidOwner, accountreview.StatusSendFailed:
			failed++
		}
	}
	return output.WriteNotifySummary(w, format, output.NotifySummary{
		Planned: planned,
		Sent:    sent,
		Failed:  failed,
		Skipped: skipped,
		Results: results,
	})
}
