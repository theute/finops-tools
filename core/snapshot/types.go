// Package snapshot discovers stale AWS snapshot resources and estimates storage costs.
package snapshot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// Kind identifies a snapshot resource type.
type Kind string

const (
	KindEBSSnapshot         Kind = "ebs-snapshot"
	KindRDSSnapshot         Kind = "rds-snapshot"
	KindRDSClusterSnapshot  Kind = "rds-cluster-snapshot"
)

// CostBasis describes how estimated monthly cost was calculated.
const (
	CostBasisVolumeSizeEstimate = "volume_size_estimate"
	CostBasisEBSIncrementalChain = "ebs_incremental_chain"
	CostBasisEBSNoIncremental   = "ebs_no_incremental"
	CostBasisRDSWithinFreeTier  = "rds_within_free_tier"
	CostBasisRDSRegionalExcess  = "rds_regional_excess_share"
)

// DefaultOlderThanDays is the default --older-than-days value for snapshot list.
const DefaultOlderThanDays = 365

// ParseTypes parses a comma-separated --types flag (ebs, rds).
func ParseTypes(s string) ([]Kind, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []Kind{KindEBSSnapshot, KindRDSSnapshot, KindRDSClusterSnapshot}, nil
	}
	parts, err := splitCommaList(s)
	if err != nil {
		return nil, err
	}
	var kinds []Kind
	seen := make(map[Kind]struct{})
	for _, part := range parts {
		switch strings.ToLower(part) {
		case "ebs":
			addKind(&kinds, seen, KindEBSSnapshot)
		case "rds":
			addKind(&kinds, seen, KindRDSSnapshot)
			addKind(&kinds, seen, KindRDSClusterSnapshot)
		default:
			return nil, fmt.Errorf("unknown snapshot type %q (supported: ebs, rds)", part)
		}
	}
	return kinds, nil
}

func addKind(kinds *[]Kind, seen map[Kind]struct{}, k Kind) {
	if _, ok := seen[k]; ok {
		return
	}
	seen[k] = struct{}{}
	*kinds = append(*kinds, k)
}

func splitCommaList(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one type is required")
	}
	return out, nil
}

// AccountTarget holds credentials scoped to one AWS account for snapshot discovery.
type AccountTarget struct {
	AccountID    string
	DisplayName  string
	DisplayAlias string
	AWSConfig    aws.Config
	// ConfigLoader obtains fresh credentials before each account scan (linked accounts).
	ConfigLoader func(context.Context) (aws.Config, error) `json:"-"`
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

// AccountProgress reports completion of per-account snapshot scans.
type AccountProgress interface {
	Advance()
}

// Query describes a snapshot discovery request.
type Query struct {
	Targets         []AccountTarget
	OlderThan       time.Duration
	Types           []Kind
	Regions         []string
	MinSizeGiB      float64
	AccountProgress AccountProgress
	Now             time.Time
	// Workers bounds concurrent account scans (0 = default).
	Workers      int
	regionLister regionLister
	ebsLister    ebsLister
	rdsLister    rdsLister
}

// Record is one old snapshot resource.
type Record struct {
	AccountID               string            `json:"account_id"`
	Region                  string            `json:"region"`
	Kind                    Kind              `json:"kind"`
	ResourceID              string            `json:"resource_id"`
	SourceResourceID        string            `json:"source_resource_id"`
	CreatedAt               time.Time         `json:"created_at"`
	AgeDays                 int               `json:"age_days"`
	SizeGiB                 float64           `json:"size_gib"`
	StorageTier             string            `json:"storage_tier,omitempty"`
	SnapshotType            string            `json:"snapshot_type,omitempty"`
	Description             string            `json:"description,omitempty"`
	Tags                    map[string]string `json:"tags,omitempty"`
	EstimatedMonthlyCostUSD float64           `json:"estimated_monthly_cost_usd"`
	CostBasis               string            `json:"cost_basis"`
}

// KindSummary aggregates records by snapshot kind.
type KindSummary struct {
	Kind                    Kind    `json:"kind"`
	Count                   int     `json:"count"`
	EstimatedMonthlyCostUSD float64 `json:"estimated_monthly_cost_usd"`
}

// AccountSummary aggregates records by account.
type AccountSummary struct {
	AccountID               string  `json:"account_id"`
	Count                   int     `json:"count"`
	EstimatedMonthlyCostUSD float64 `json:"estimated_monthly_cost_usd"`
}

// Summary holds aggregate totals for a snapshot scan.
type Summary struct {
	TotalCount              int              `json:"total_count"`
	EstimatedMonthlyCostUSD float64          `json:"estimated_monthly_cost_usd"`
	RDSBackupRegionalExcessGiB          float64                      `json:"rds_backup_regional_excess_gib,omitempty"`
	RDSBackupEstimatedMonthlyRunRateUSD float64                      `json:"rds_backup_estimated_monthly_run_rate_usd,omitempty"`
	EBSEstimatedMonthlyRunRateUSD       float64                      `json:"ebs_estimated_monthly_run_rate_usd,omitempty"`
	BilledCosts                         []AccountBilledSnapshotCosts `json:"billed_costs,omitempty"`
	OlderThanDays           int              `json:"older_than_days"`
	ByKind                  []KindSummary    `json:"by_kind"`
	ByAccount               []AccountSummary `json:"by_account"`
	SkippedRegions          []RegionWarning  `json:"skipped_regions,omitempty"`
	SkippedAccounts         []AccountWarning `json:"skipped_accounts,omitempty"`
	CostDisclaimer          string           `json:"cost_disclaimer"`
}

// Result is the output of Fetch.
type Result struct {
	Records []Record `json:"records"`
	Summary Summary  `json:"summary"`
}
