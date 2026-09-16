package snapshot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/openshift-online/finops-tools/core/apilog"
)

// RDSAPI is the subset of RDS used for snapshot discovery.
type RDSAPI interface {
	DescribeDBInstances(
		ctx context.Context,
		params *rds.DescribeDBInstancesInput,
		optFns ...func(*rds.Options),
	) (*rds.DescribeDBInstancesOutput, error)
	DescribeDBClusters(
		ctx context.Context,
		params *rds.DescribeDBClustersInput,
		optFns ...func(*rds.Options),
	) (*rds.DescribeDBClustersOutput, error)
	DescribeDBSnapshots(
		ctx context.Context,
		params *rds.DescribeDBSnapshotsInput,
		optFns ...func(*rds.Options),
	) (*rds.DescribeDBSnapshotsOutput, error)
	DescribeDBClusterSnapshots(
		ctx context.Context,
		params *rds.DescribeDBClusterSnapshotsInput,
		optFns ...func(*rds.Options),
	) (*rds.DescribeDBClusterSnapshotsOutput, error)
}

type rdsClientFactory func(aws.Config) RDSAPI

func newRDSClient(cfg aws.Config) RDSAPI {
	return apilog.WrapRDS(rds.NewFromConfig(cfg))
}

type rdsLister interface {
	ListRDSSnapshots(
		ctx context.Context,
		cfg aws.Config,
		region, accountID string,
		cutoff time.Time,
		minSizeGiB float64,
	) ([]Record, error)
	GetRDSRegionContext(
		ctx context.Context,
		cfg aws.Config,
		region string,
	) (RDSRegionContext, error)
}

type awsRDSLister struct{}

func (awsRDSLister) ListRDSSnapshots(
	ctx context.Context,
	cfg aws.Config,
	region, accountID string,
	cutoff time.Time,
	minSizeGiB float64,
) ([]Record, error) {
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	client := newRDSClient(regionalCfg)
	instanceRecords, err := listRDSSnapshotsWithClient(ctx, client, accountID, region, cutoff, minSizeGiB)
	if err != nil {
		return nil, err
	}
	clusterRecords, err := listRDSClusterSnapshotsWithClient(ctx, client, accountID, region, cutoff, minSizeGiB)
	if err != nil {
		return nil, err
	}
	return append(instanceRecords, clusterRecords...), nil
}

func (awsRDSLister) GetRDSRegionContext(
	ctx context.Context,
	cfg aws.Config,
	region string,
) (RDSRegionContext, error) {
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	client := newRDSClient(regionalCfg)
	return getRDSRegionContextWithClient(ctx, client, region)
}

func getRDSRegionContextWithClient(
	ctx context.Context,
	client RDSAPI,
	region string,
) (RDSRegionContext, error) {
	instances, err := listDBInstances(ctx, client, region)
	if err != nil {
		return RDSRegionContext{}, err
	}
	clusters, err := listDBClusters(ctx, client, region)
	if err != nil {
		return RDSRegionContext{}, err
	}
	dbSnapshots, err := listAllDBSnapshots(ctx, client, region)
	if err != nil {
		return RDSRegionContext{}, err
	}
	clusterSnapshots, err := listAllDBClusterSnapshots(ctx, client, region)
	if err != nil {
		return RDSRegionContext{}, err
	}
	liveInstances, liveClusters := liveSourceIDs(instances, clusters)
	regionCtx := RDSRegionContext{
		LiveInstanceIDs: liveInstances,
		LiveClusterIDs:  liveClusters,
	}
	regionCtx.FreePoolGiB = provisionedStorageGiB(instances, clusters)
	regionCtx.TotalBackupGiB = estimateBackupStorageGiB(dbSnapshots, clusterSnapshots)
	regionCtx.BillableBackupGiB = billableBackupGiB(dbSnapshots, clusterSnapshots, regionCtx)
	return regionCtx, nil
}

func billableBackupGiB(
	dbSnapshots []rdstypes.DBSnapshot,
	clusterSnapshots []rdstypes.DBClusterSnapshot,
	ctx RDSRegionContext,
) float64 {
	var total float64
	for _, snap := range dbSnapshots {
		rec := Record{
			Kind:             KindRDSSnapshot,
			SizeGiB:          float64(aws.ToInt32(snap.AllocatedStorage)),
			SnapshotType:     aws.ToString(snap.SnapshotType),
			SourceResourceID: aws.ToString(snap.DBInstanceIdentifier),
		}
		if rdsSnapshotIsBillable(rec, ctx) {
			total += rec.SizeGiB
		}
	}
	for _, snap := range clusterSnapshots {
		rec := Record{
			Kind:             KindRDSClusterSnapshot,
			SizeGiB:          float64(aws.ToInt32(snap.AllocatedStorage)),
			SnapshotType:     aws.ToString(snap.SnapshotType),
			SourceResourceID: aws.ToString(snap.DBClusterIdentifier),
		}
		if rdsSnapshotIsBillable(rec, ctx) {
			total += rec.SizeGiB
		}
	}
	return total
}

func listDBInstances(ctx context.Context, client RDSAPI, region string) ([]rdstypes.DBInstance, error) {
	var instances []rdstypes.DBInstance
	var token *string
	for {
		out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			Marker: token,
		})
		if err != nil {
			if isRDSRegionUnsupported(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("describe db instances in %s: %w", region, err)
		}
		instances = append(instances, out.DBInstances...)
		if out.Marker == nil || aws.ToString(out.Marker) == "" {
			break
		}
		token = out.Marker
	}
	return instances, nil
}

func listDBClusters(ctx context.Context, client RDSAPI, region string) ([]rdstypes.DBCluster, error) {
	var clusters []rdstypes.DBCluster
	var token *string
	for {
		out, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
			Marker: token,
		})
		if err != nil {
			if isRDSRegionUnsupported(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("describe db clusters in %s: %w", region, err)
		}
		clusters = append(clusters, out.DBClusters...)
		if out.Marker == nil || aws.ToString(out.Marker) == "" {
			break
		}
		token = out.Marker
	}
	return clusters, nil
}

func listAllDBSnapshots(ctx context.Context, client RDSAPI, region string) ([]rdstypes.DBSnapshot, error) {
	var snapshots []rdstypes.DBSnapshot
	var token *string
	for {
		out, err := client.DescribeDBSnapshots(ctx, &rds.DescribeDBSnapshotsInput{
			Marker: token,
		})
		if err != nil {
			if isRDSRegionUnsupported(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("describe rds snapshots in %s: %w", region, err)
		}
		snapshots = append(snapshots, out.DBSnapshots...)
		if out.Marker == nil || aws.ToString(out.Marker) == "" {
			break
		}
		token = out.Marker
	}
	return snapshots, nil
}

func listAllDBClusterSnapshots(ctx context.Context, client RDSAPI, region string) ([]rdstypes.DBClusterSnapshot, error) {
	var snapshots []rdstypes.DBClusterSnapshot
	var token *string
	for {
		out, err := client.DescribeDBClusterSnapshots(ctx, &rds.DescribeDBClusterSnapshotsInput{
			Marker: token,
		})
		if err != nil {
			if isRDSRegionUnsupported(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("describe rds cluster snapshots in %s: %w", region, err)
		}
		snapshots = append(snapshots, out.DBClusterSnapshots...)
		if out.Marker == nil || aws.ToString(out.Marker) == "" {
			break
		}
		token = out.Marker
	}
	return snapshots, nil
}

func provisionedStorageGiB(instances []rdstypes.DBInstance, clusters []rdstypes.DBCluster) float64 {
	var total float64
	for _, inst := range instances {
		if clusterID := strings.TrimSpace(aws.ToString(inst.DBClusterIdentifier)); clusterID != "" {
			continue
		}
		total += float64(aws.ToInt32(inst.AllocatedStorage))
	}
	for _, cluster := range clusters {
		total += float64(aws.ToInt32(cluster.AllocatedStorage))
	}
	return total
}

func liveSourceIDs(
	instances []rdstypes.DBInstance,
	clusters []rdstypes.DBCluster,
) (map[string]struct{}, map[string]struct{}) {
	liveInstances := make(map[string]struct{}, len(instances))
	for _, inst := range instances {
		if id := strings.TrimSpace(aws.ToString(inst.DBInstanceIdentifier)); id != "" {
			liveInstances[id] = struct{}{}
		}
	}
	liveClusters := make(map[string]struct{}, len(clusters))
	for _, cluster := range clusters {
		if id := strings.TrimSpace(aws.ToString(cluster.DBClusterIdentifier)); id != "" {
			liveClusters[id] = struct{}{}
		}
	}
	return liveInstances, liveClusters
}

type rdsSourceBackupEstimate struct {
	automatedMaxGiB float64
	manualSumGiB    float64
}

func estimateBackupStorageGiB(
	dbSnapshots []rdstypes.DBSnapshot,
	clusterSnapshots []rdstypes.DBClusterSnapshot,
) float64 {
	bySource := make(map[string]*rdsSourceBackupEstimate)
	for _, snap := range dbSnapshots {
		sourceKey := "instance:" + strings.TrimSpace(aws.ToString(snap.DBInstanceIdentifier))
		addSnapshotToBackupEstimate(
			bySource,
			sourceKey,
			float64(aws.ToInt32(snap.AllocatedStorage)),
			aws.ToString(snap.SnapshotType),
		)
	}
	for _, snap := range clusterSnapshots {
		sourceKey := "cluster:" + strings.TrimSpace(aws.ToString(snap.DBClusterIdentifier))
		addSnapshotToBackupEstimate(
			bySource,
			sourceKey,
			float64(aws.ToInt32(snap.AllocatedStorage)),
			aws.ToString(snap.SnapshotType),
		)
	}
	var total float64
	for _, estimate := range bySource {
		total += estimate.automatedMaxGiB + estimate.manualSumGiB
	}
	return total
}

func addSnapshotToBackupEstimate(
	bySource map[string]*rdsSourceBackupEstimate,
	sourceKey string,
	sizeGiB float64,
	snapshotType string,
) {
	if sourceKey == "instance:" || sourceKey == "cluster:" || sizeGiB <= 0 {
		return
	}
	estimate := bySource[sourceKey]
	if estimate == nil {
		estimate = &rdsSourceBackupEstimate{}
		bySource[sourceKey] = estimate
	}
	if strings.EqualFold(strings.TrimSpace(snapshotType), "manual") {
		estimate.manualSumGiB += sizeGiB
		return
	}
	if sizeGiB > estimate.automatedMaxGiB {
		estimate.automatedMaxGiB = sizeGiB
	}
}

func listRDSSnapshotsWithClient(
	ctx context.Context,
	client RDSAPI,
	accountID, region string,
	cutoff time.Time,
	minSizeGiB float64,
) ([]Record, error) {
	snaps, err := listAllDBSnapshots(ctx, client, region)
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, snap := range snaps {
		if rec, ok := rdsSnapshotRecord(snap, accountID, region, cutoff, minSizeGiB); ok {
			records = append(records, rec)
		}
	}
	return records, nil
}

func listRDSClusterSnapshotsWithClient(
	ctx context.Context,
	client RDSAPI,
	accountID, region string,
	cutoff time.Time,
	minSizeGiB float64,
) ([]Record, error) {
	snaps, err := listAllDBClusterSnapshots(ctx, client, region)
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, snap := range snaps {
		if rec, ok := rdsClusterSnapshotRecord(snap, accountID, region, cutoff, minSizeGiB); ok {
			records = append(records, rec)
		}
	}
	return records, nil
}

func rdsSnapshotRecord(
	snap rdstypes.DBSnapshot,
	accountID, region string,
	cutoff time.Time,
	minSizeGiB float64,
) (Record, bool) {
	created := aws.ToTime(snap.SnapshotCreateTime)
	if created.IsZero() || !created.Before(cutoff) {
		return Record{}, false
	}
	sizeGiB := float64(aws.ToInt32(snap.AllocatedStorage))
	if sizeGiB < minSizeGiB {
		return Record{}, false
	}
	snapType := strings.TrimSpace(aws.ToString(snap.SnapshotType))
	return Record{
		AccountID:               accountID,
		Region:                  region,
		Kind:                    KindRDSSnapshot,
		ResourceID:              aws.ToString(snap.DBSnapshotIdentifier),
		SourceResourceID:        aws.ToString(snap.DBInstanceIdentifier),
		CreatedAt:               created.UTC(),
		AgeDays:                 ageDays(created),
		SizeGiB:                 sizeGiB,
		SnapshotType:            snapType,
		CostBasis:               CostBasisRDSRegionalExcess,
	}, true
}

func rdsClusterSnapshotRecord(
	snap rdstypes.DBClusterSnapshot,
	accountID, region string,
	cutoff time.Time,
	minSizeGiB float64,
) (Record, bool) {
	created := aws.ToTime(snap.SnapshotCreateTime)
	if created.IsZero() || !created.Before(cutoff) {
		return Record{}, false
	}
	sizeGiB := float64(aws.ToInt32(snap.AllocatedStorage))
	if sizeGiB < minSizeGiB {
		return Record{}, false
	}
	snapType := strings.TrimSpace(aws.ToString(snap.SnapshotType))
	return Record{
		AccountID:               accountID,
		Region:                  region,
		Kind:                    KindRDSClusterSnapshot,
		ResourceID:              aws.ToString(snap.DBClusterSnapshotIdentifier),
		SourceResourceID:        aws.ToString(snap.DBClusterIdentifier),
		CreatedAt:               created.UTC(),
		AgeDays:                 ageDays(created),
		SizeGiB:                 sizeGiB,
		SnapshotType:            snapType,
		CostBasis:               CostBasisRDSRegionalExcess,
	}, true
}

func isRDSRegionUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not available in this region")
}
