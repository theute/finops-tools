// Package hcphierarchy resolves the ROSA HCP SC → MC → Worker cluster chain using a
// Snowflake mart self-join. All data is returned as in-memory structs; nothing is
// written to disk. Downstream consumers call ClusterIDs(), MCAccountTargets(), and
// SCAccountTargets() on the returned Report to feed Telesense and Cost Explorer.
package hcphierarchy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/openshift-online/finops-tools/core/cost"
	"github.com/openshift-online/finops-tools/core/snowflake"
	"github.com/openshift-online/finops-tools/core/sqlrows"
)

const defaultMartView = "HCMFINOPS_DB.MARTS.OCM_CLOUDABILITY_MAPPING"

// SnowflakeQueryer executes a SQL query and returns iterable rows.
// *sql.DB satisfies this interface via a thin adapter; inject a mock in tests.
// Call Close when finished to release the underlying connection pool.
type SnowflakeQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (SnowflakeRows, error)
	Close() error
}

// SnowflakeRows mirrors database/sql.Rows; kept as an alias for backward compatibility.
type SnowflakeRows = sqlrows.Rows

// HierarchyRow is one resolved worker cluster with its MC and SC account context.
type HierarchyRow struct {
	CustomerOCMID        string
	CustomerClusterID    string // UUID — matches TELESENSE OCP_USAGE.CLUSTER_ID
	CustomerClusterName  string
	CustomerAccountID    string
	CustomerState        string
	CustomerRegion       string
	OrganizationName     string
	Classification       string
	ClassificationReason string
	HierarchyComplete    bool
	HierarchySource      string

	MCName      string
	MCFleetID   string
	MCAccountID string // AWS account ID for the Management Cluster tier
	MCRegion    string
	MCStatus    string

	SCName      string
	SCFleetID   string
	SCAccountID string // AWS account ID for the Service Cluster tier
	SCRegion    string
	SCStatus    string

	FleetSector        string
	BillingModel       string
	ServiceLevel       string
	SubscriptionStatus string
}

// Report holds the fully resolved hierarchy returned by Build().
// All downstream consumers read from this struct — nothing is persisted to disk.
type Report struct {
	GeneratedAt time.Time
	MartView    string
	Rows        []HierarchyRow
}

// ClusterIDs returns the customer_cluster_id UUIDs for all worker rows.
// These map directly to TELESENSE_DB.OPENSHIFT_MARTS.OCP_USAGE.CLUSTER_ID and
// are passed as-is to telesense.FetchVCPU — no intermediate file is written.
func (r Report) ClusterIDs() []string {
	ids := make([]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		if row.CustomerClusterID != "" {
			ids = append(ids, row.CustomerClusterID)
		}
	}
	return ids
}

// MCAccountTargets returns cost.AccountTarget entries for every distinct MC
// (account ID, region) pair. Pass directly to cost.Fetch() for Management Cluster cost attribution.
func (r Report) MCAccountTargets(cfg aws.Config) []cost.AccountTarget {
	return accountTargets(r.Rows, func(row HierarchyRow) (string, string) {
		return row.MCAccountID, row.MCRegion
	}, cfg)
}

// SCAccountTargets returns cost.AccountTarget entries for every distinct SC
// (account ID, region) pair. Pass directly to cost.Fetch() for Service Cluster cost attribution.
func (r Report) SCAccountTargets(cfg aws.Config) []cost.AccountTarget {
	return accountTargets(r.Rows, func(row HierarchyRow) (string, string) {
		return row.SCAccountID, row.SCRegion
	}, cfg)
}

// Build resolves the HCP hierarchy with a single Snowflake self-join.
// The returned Report is entirely in-memory; no files are written.
// Pass martView = "" to use the default RHSANDBOX mart location.
func Build(ctx context.Context, sf SnowflakeQueryer, martView string) (Report, error) {
	if martView == "" {
		martView = defaultMartView
	}

	sql, err := buildMartSQL(martView)
	if err != nil {
		return Report{}, err
	}
	rows, err := sf.QueryContext(ctx, sql)
	if err != nil {
		return Report{}, fmt.Errorf("hcp hierarchy mart query: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var result []HierarchyRow
	for rows.Next() {
		var (
			hierarchyComplete                                           *bool
			customerOCMID, customerClusterID, customerClusterName       *string
			customerAccountID, customerState, customerRegion            *string
			organizationName, classification, classificationReason      *string
			hierarchySource                                             *string
			mcName, mcFleetID, mcAccountID, mcRegion, mcStatus          *string
			scName, scFleetID, scAccountID, scRegion, scStatus          *string
			fleetSector, billingModel, serviceLevel, subscriptionStatus *string
		)
		if err := rows.Scan(
			&customerOCMID, &customerClusterID, &customerClusterName,
			&customerAccountID, &customerState, &customerRegion,
			&organizationName, &classification, &classificationReason,
			&hierarchyComplete, &hierarchySource,
			&mcName, &mcFleetID, &mcAccountID, &mcRegion, &mcStatus,
			&scName, &scFleetID, &scAccountID, &scRegion, &scStatus,
			&fleetSector, &billingModel, &serviceLevel, &subscriptionStatus,
		); err != nil {
			return Report{}, fmt.Errorf("scan hierarchy row: %w", err)
		}
		r := HierarchyRow{
			CustomerOCMID:        stringOrEmpty(customerOCMID),
			CustomerClusterID:    stringOrEmpty(customerClusterID),
			CustomerClusterName:  stringOrEmpty(customerClusterName),
			CustomerAccountID:    stringOrEmpty(customerAccountID),
			CustomerState:        stringOrEmpty(customerState),
			CustomerRegion:       stringOrEmpty(customerRegion),
			OrganizationName:     stringOrEmpty(organizationName),
			Classification:       stringOrEmpty(classification),
			ClassificationReason: stringOrEmpty(classificationReason),
			HierarchySource:      stringOrEmpty(hierarchySource),
			MCName:               stringOrEmpty(mcName),
			MCFleetID:            stringOrEmpty(mcFleetID),
			MCAccountID:          stringOrEmpty(mcAccountID),
			MCRegion:             stringOrEmpty(mcRegion),
			MCStatus:             stringOrEmpty(mcStatus),
			SCName:               stringOrEmpty(scName),
			SCFleetID:            stringOrEmpty(scFleetID),
			SCAccountID:          stringOrEmpty(scAccountID),
			SCRegion:             stringOrEmpty(scRegion),
			SCStatus:             stringOrEmpty(scStatus),
			FleetSector:          stringOrEmpty(fleetSector),
			BillingModel:         stringOrEmpty(billingModel),
			ServiceLevel:         stringOrEmpty(serviceLevel),
			SubscriptionStatus:   stringOrEmpty(subscriptionStatus),
		}
		if hierarchyComplete != nil {
			r.HierarchyComplete = *hierarchyComplete
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return Report{}, fmt.Errorf("iterate hierarchy rows: %w", err)
	}

	return Report{
		GeneratedAt: time.Now().UTC(),
		MartView:    martView,
		Rows:        result,
	}, nil
}

// buildMartSQL generates the self-join SQL for the given mart view.
// The mart is joined to itself twice: once on MC fleet ID, once on SC fleet ID.
func buildMartSQL(martView string) (string, error) {
	if err := snowflake.ValidateQualifiedIdentifier(martView, 3); err != nil {
		return "", fmt.Errorf("invalid mart view: %w", err)
	}
	return strings.TrimSpace(fmt.Sprintf(`
SELECT
    w.CLUSTER_ID                    AS customer_ocm_id,
    w.EXTERNAL_ID                   AS customer_cluster_id,
    w.CLUSTER_NAME                  AS customer_cluster_name,
    w.AWS_ACCOUNT_ID                AS customer_account_id,
    w.STATE                         AS customer_state,
    w.REGION                        AS customer_region,
    w.ORGANIZATION_NAME,
    w.CLASSIFICATION,
    w.CLASSIFICATION_REASON,
    w.HIERARCHY_COMPLETE,
    w.HIERARCHY_SOURCE,
    w.HCP_MANAGEMENT_CLUSTER_NAME   AS mc_name,
    w.HCP_MC_FLEET_ID               AS mc_fleet_id,
    mc.AWS_ACCOUNT_ID               AS mc_account_id,
    mc.REGION                       AS mc_region,
    mc.STATE                        AS mc_status,
    w.HCP_SERVICE_CLUSTER_NAME      AS sc_name,
    w.HCP_SC_FLEET_ID               AS sc_fleet_id,
    sc.AWS_ACCOUNT_ID               AS sc_account_id,
    sc.REGION                       AS sc_region,
    sc.STATE                        AS sc_status,
    w.HCP_SECTOR                    AS fleet_sector,
    w.BILLING_MODEL                 AS billing_model,
    w.SERVICE_LEVEL,
    w.SUBSCRIPTION_STATUS
FROM %s w
LEFT JOIN %s mc ON w.HCP_MC_FLEET_ID = mc.MC_FLEET_ID
LEFT JOIN %s sc ON w.HCP_SC_FLEET_ID = sc.SC_FLEET_ID
WHERE w.CLUSTER_ROLE   = 'worker'
  AND w.CLOUD_PROVIDER = 'aws'`, martView, martView, martView)), nil
}

func stringOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// accountTargets deduplicates (account ID, region) pairs from rows and returns AccountTargets.
func accountTargets(
	rows []HierarchyRow,
	pick func(HierarchyRow) (accountID, region string),
	cfg aws.Config,
) []cost.AccountTarget {
	seen := map[string]bool{}
	var targets []cost.AccountTarget
	for _, row := range rows {
		id, region := pick(row)
		if id == "" {
			continue
		}
		key := id + "\x00" + region
		if seen[key] {
			continue
		}
		seen[key] = true
		c := cfg.Copy()
		c.Region = region
		targets = append(targets, cost.AccountTarget{AccountID: id, AWSConfig: c})
	}
	return targets
}
