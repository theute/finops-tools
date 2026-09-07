package inventory

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/openshift-online/finops-tools/core/apilog"
)

type RDSAPI interface {
	DescribeDBInstances(ctx context.Context, params *rds.DescribeDBInstancesInput, optFns ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error)
	DescribeDBClusters(ctx context.Context, params *rds.DescribeDBClustersInput, optFns ...func(*rds.Options)) (*rds.DescribeDBClustersOutput, error)
}

func newRDSClient(cfg aws.Config) RDSAPI {
	return apilog.WrapRDS(rds.NewFromConfig(cfg))
}

// listRDSResources returns instances and clusters independently.
// A failure in one Describe still returns the other plus a combined error.
func listRDSResources(ctx context.Context, client RDSAPI, region string) ([]RDSInstance, []RDSCluster, error) {
	var (
		errs     []string
		clusters []RDSCluster
	)

	instances, err := describeDBInstances(ctx, client, region)
	if err != nil {
		errs = append(errs, "instances: "+err.Error())
		instances = nil
	}
	clusters, err = describeDBClusters(ctx, client, region)
	if err != nil {
		errs = append(errs, "clusters: "+err.Error())
		clusters = nil
	}
	if len(errs) > 0 {
		return instances, clusters, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return instances, clusters, nil
}

func describeDBInstances(ctx context.Context, client RDSAPI, region string) ([]RDSInstance, error) {
	var out []RDSInstance
	var marker *string
	for {
		resp, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		for _, db := range resp.DBInstances {
			// Cluster members are listed under RDSClusters so Aurora/RDS Multi-AZ
			// workloads are not duplicated in the instances table.
			if aws.ToString(db.DBClusterIdentifier) != "" {
				continue
			}
			out = append(out, RDSInstance{
				InstanceID: aws.ToString(db.DBInstanceIdentifier),
				Engine:     aws.ToString(db.Engine),
				Class:      aws.ToString(db.DBInstanceClass),
				Status:     aws.ToString(db.DBInstanceStatus),
				MultiAZ:    aws.ToBool(db.MultiAZ),
				Region:     region,
			})
		}
		if resp.Marker == nil || *resp.Marker == "" {
			break
		}
		marker = resp.Marker
	}
	return out, nil
}

func describeDBClusters(ctx context.Context, client RDSAPI, region string) ([]RDSCluster, error) {
	var out []RDSCluster
	var marker *string
	for {
		resp, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		for _, cluster := range resp.DBClusters {
			out = append(out, RDSCluster{
				ClusterID: aws.ToString(cluster.DBClusterIdentifier),
				Engine:    aws.ToString(cluster.Engine),
				Status:    aws.ToString(cluster.Status),
				Region:    region,
			})
		}
		if resp.Marker == nil || *resp.Marker == "" {
			break
		}
		marker = resp.Marker
	}
	return out, nil
}
