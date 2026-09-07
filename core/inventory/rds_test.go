package inventory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

type fakeRDS struct {
	instances []rdstypes.DBInstance
	clusters  []rdstypes.DBCluster
	instErr   error
	clustErr  error
}

func (f fakeRDS) DescribeDBInstances(
	context.Context,
	*rds.DescribeDBInstancesInput,
	...func(*rds.Options),
) (*rds.DescribeDBInstancesOutput, error) {
	if f.instErr != nil {
		return nil, f.instErr
	}
	return &rds.DescribeDBInstancesOutput{DBInstances: f.instances}, nil
}

func (f fakeRDS) DescribeDBClusters(
	context.Context,
	*rds.DescribeDBClustersInput,
	...func(*rds.Options),
) (*rds.DescribeDBClustersOutput, error) {
	if f.clustErr != nil {
		return nil, f.clustErr
	}
	return &rds.DescribeDBClustersOutput{DBClusters: f.clusters}, nil
}

func TestListRDSResourcesOmitsClusterMembers(t *testing.T) {
	t.Parallel()
	instances, clusters, err := listRDSResources(context.Background(), fakeRDS{
		instances: []rdstypes.DBInstance{
			{
				DBInstanceIdentifier: aws.String("standalone"),
				Engine:               aws.String("postgres"),
				DBInstanceClass:      aws.String("db.t3.micro"),
				DBInstanceStatus:     aws.String("available"),
			},
			{
				DBInstanceIdentifier: aws.String("aurora-instance-1"),
				DBClusterIdentifier:  aws.String("aurora-cluster"),
				Engine:               aws.String("aurora-postgresql"),
				DBInstanceClass:      aws.String("db.r6g.large"),
				DBInstanceStatus:     aws.String("available"),
			},
		},
		clusters: []rdstypes.DBCluster{
			{
				DBClusterIdentifier: aws.String("aurora-cluster"),
				Engine:              aws.String("aurora-postgresql"),
				Status:              aws.String("available"),
			},
		},
	}, "us-east-1")
	if err != nil {
		t.Fatalf("listRDSResources() error = %v", err)
	}
	if len(instances) != 1 || instances[0].InstanceID != "standalone" {
		t.Fatalf("instances = %+v, want standalone only", instances)
	}
	if len(clusters) != 1 || clusters[0].ClusterID != "aurora-cluster" {
		t.Fatalf("clusters = %+v", clusters)
	}
}

func TestListRDSResourcesKeepsInstancesWhenClustersFail(t *testing.T) {
	t.Parallel()
	instances, clusters, err := listRDSResources(context.Background(), fakeRDS{
		instances: []rdstypes.DBInstance{{
			DBInstanceIdentifier: aws.String("standalone"),
			Engine:               aws.String("postgres"),
			DBInstanceClass:      aws.String("db.t3.micro"),
			DBInstanceStatus:     aws.String("available"),
		}},
		clustErr: fmt.Errorf("clusters denied"),
	}, "us-east-1")
	if err == nil || !strings.Contains(err.Error(), "clusters") {
		t.Fatalf("error = %v, want clusters", err)
	}
	if len(instances) != 1 || instances[0].InstanceID != "standalone" {
		t.Fatalf("instances = %+v", instances)
	}
	if clusters != nil {
		t.Fatalf("clusters = %+v, want nil", clusters)
	}
}
