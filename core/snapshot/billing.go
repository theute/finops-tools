package snapshot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/openshift-online/finops-tools/core/apilog"
	"github.com/openshift-online/finops-tools/core/cost"
	"github.com/openshift-online/finops-tools/core/parallel"
)

const snapshotBillingBatchSize = 100

// CostExplorerAPI is the subset of Cost Explorer used for billed snapshot costs.
type CostExplorerAPI interface {
	GetCostAndUsage(
		ctx context.Context,
		params *costexplorer.GetCostAndUsageInput,
		optFns ...func(*costexplorer.Options),
	) (*costexplorer.GetCostAndUsageOutput, error)
}

// BilledSnapshotPeriod is the Cost Explorer window used for billed snapshot lines.
type BilledSnapshotPeriod struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

// AccountBilledSnapshotCosts is actual billed snapshot storage from Cost Explorer.
type AccountBilledSnapshotCosts struct {
	AccountID           string               `json:"account_id"`
	Period              BilledSnapshotPeriod `json:"period"`
	EBSSnapshotUSD      float64              `json:"ebs_snapshot_usd"`
	EBSSnapshotGiBMonth float64              `json:"ebs_snapshot_gib_month,omitempty"`
	RDSBackupUSD        float64              `json:"rds_backup_usd"`
	RDSBackupGiBMonth   float64              `json:"rds_backup_usage_gib_month,omitempty"`
}

// LastCompleteMonthRange returns the Cost Explorer date range for the last full calendar month.
func LastCompleteMonthRange(now time.Time) (start, end time.Time) {
	now = now.UTC()
	firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end = firstOfThisMonth
	start = firstOfThisMonth.AddDate(0, -1, 0)
	return start, end
}

// FetchBilledSnapshotCosts queries Cost Explorer for billed EBS snapshot and RDS backup storage.
func FetchBilledSnapshotCosts(
	ctx context.Context,
	ce CostExplorerAPI,
	accountIDs []string,
	now time.Time,
	workers int,
) ([]AccountBilledSnapshotCosts, error) {
	if ce == nil {
		return nil, fmt.Errorf("cost explorer client is required")
	}
	start, end := LastCompleteMonthRange(now)
	period := BilledSnapshotPeriod{
		StartDate: cost.FormatDate(start.UTC()),
		EndDate:   cost.FormatDate(end.AddDate(0, 0, -1).UTC()),
	}

	accountIDs = cost.UniqueAccountIDs(accountIDs)
	if len(accountIDs) == 0 {
		return nil, nil
	}

	batches := batchAccountIDs(accountIDs, snapshotBillingBatchSize)
	batchResults := make([]map[string]AccountBilledSnapshotCosts, len(batches))
	err := parallel.ForEach(ctx, workers, len(batches), func(ctx context.Context, i int) error {
		partial, err := fetchBatchBilledSnapshotCosts(ctx, ce, batches[i], start, end, period)
		if err != nil {
			return err
		}
		batchResults[i] = partial
		return nil
	})
	if err != nil {
		return nil, err
	}

	byAccount := make(map[string]AccountBilledSnapshotCosts, len(accountIDs))
	for _, partial := range batchResults {
		for accountID, costs := range partial {
			byAccount[accountID] = costs
		}
	}

	out := make([]AccountBilledSnapshotCosts, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		if costs, ok := byAccount[accountID]; ok {
			out = append(out, costs)
			continue
		}
		out = append(out, AccountBilledSnapshotCosts{
			AccountID: accountID,
			Period:    period,
		})
	}
	return out, nil
}

func batchAccountIDs(ids []string, size int) [][]string {
	if size <= 0 || len(ids) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(ids)+size-1)/size)
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[i:end])
	}
	return out
}

func fetchBatchBilledSnapshotCosts(
	ctx context.Context,
	ce CostExplorerAPI,
	accountIDs []string,
	start, end time.Time,
	period BilledSnapshotPeriod,
) (map[string]AccountBilledSnapshotCosts, error) {
	results := make(map[string]AccountBilledSnapshotCosts, len(accountIDs))
	for _, accountID := range accountIDs {
		results[accountID] = AccountBilledSnapshotCosts{
			AccountID: accountID,
			Period:    period,
		}
	}

	var token *string
	for {
		out, err := ce.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &types.DateInterval{
				Start: aws.String(cost.FormatDate(start.UTC())),
				End:   aws.String(cost.FormatDate(end.UTC())),
			},
			Granularity: types.GranularityMonthly,
			Metrics:     []string{"UnblendedCost", "UsageQuantity"},
			GroupBy: []types.GroupDefinition{
				{
					Type: types.GroupDefinitionTypeDimension,
					Key:  aws.String("LINKED_ACCOUNT"),
				},
				{
					Type: types.GroupDefinitionTypeDimension,
					Key:  aws.String("USAGE_TYPE"),
				},
			},
			Filter:        linkedAccountsCEFilter(accountIDs),
			NextPageToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("cost explorer GetCostAndUsage: %w", err)
		}

		for _, row := range out.ResultsByTime {
			for _, group := range row.Groups {
				if len(group.Keys) < 2 {
					continue
				}
				accountID := strings.TrimSpace(group.Keys[0])
				usageType := group.Keys[1]
				costs, ok := results[accountID]
				if !ok {
					continue
				}
				if err := applyCEUsageTypeToCosts(&costs, usageType, group.Metrics); err != nil {
					return nil, err
				}
				results[accountID] = costs
			}
		}

		if out.NextPageToken == nil || aws.ToString(out.NextPageToken) == "" {
			break
		}
		token = out.NextPageToken
	}
	return results, nil
}

func linkedAccountsCEFilter(accountIDs []string) *types.Expression {
	if len(accountIDs) == 0 {
		return nil
	}
	if len(accountIDs) == 1 {
		return accountCEFilter(accountIDs[0])
	}
	return &types.Expression{
		Dimensions: &types.DimensionValues{
			Key:    types.DimensionLinkedAccount,
			Values: accountIDs,
		},
	}
}

func accountCEFilter(accountID string) *types.Expression {
	return &types.Expression{
		Dimensions: &types.DimensionValues{
			Key:    types.DimensionLinkedAccount,
			Values: []string{accountID},
		},
	}
}

func applyCEUsageTypeToCosts(costs *AccountBilledSnapshotCosts, usageType string, metrics map[string]types.MetricValue) error {
	cost, usage, err := parseCEMetrics(metrics)
	if err != nil {
		return err
	}
	if isEBSSnapshotUsageType(usageType) {
		costs.EBSSnapshotUSD += cost
		costs.EBSSnapshotGiBMonth += usage
	}
	if isRDSBackupUsageType(usageType) {
		costs.RDSBackupUSD += cost
		costs.RDSBackupGiBMonth += usage
	}
	return nil
}

func isEBSSnapshotUsageType(usageType string) bool {
	usageType = strings.ToLower(usageType)
	return strings.Contains(usageType, "ebs:snapshotusage") ||
		strings.Contains(usageType, "ebs:snapshotarchivestorage")
}

func isRDSBackupUsageType(usageType string) bool {
	return strings.Contains(strings.ToLower(usageType), "chargedbackupusage")
}

func parseCEMetrics(metrics map[string]types.MetricValue) (cost, usage float64, err error) {
	if m, ok := metrics["UnblendedCost"]; ok {
		cost, err = strconv.ParseFloat(aws.ToString(m.Amount), 64)
		if err != nil {
			return 0, 0, fmt.Errorf("parse UnblendedCost: %w", err)
		}
	}
	if m, ok := metrics["UsageQuantity"]; ok {
		usage, err = strconv.ParseFloat(aws.ToString(m.Amount), 64)
		if err != nil {
			return 0, 0, fmt.Errorf("parse UsageQuantity: %w", err)
		}
	}
	return cost, usage, nil
}

// NewCostExplorerClient returns a Cost Explorer client (API endpoint is us-east-1).
func NewCostExplorerClient(cfg aws.Config) CostExplorerAPI {
	return newCostExplorerClient(cfg)
}

func newCostExplorerClient(cfg aws.Config) CostExplorerAPI {
	if cfg.Region == "" {
		cfg.Region = cost.CostExplorerRegion
	}
	inner := costexplorer.NewFromConfig(cfg, func(o *costexplorer.Options) {
		o.Region = cost.CostExplorerRegion
	})
	return apilog.WrapGetCostAndUsage(inner)
}
