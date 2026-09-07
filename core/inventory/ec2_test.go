package inventory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type fakeEC2 struct {
	instances []ec2types.Instance
	volumes   []ec2types.Volume
	addresses []ec2types.Address
	nats      []ec2types.NatGateway
	vpcs      []ec2types.Vpc

	instancesErr error
	volumesErr   error
	addressesErr error
	natsErr      error
	vpcsErr      error
}

func (f fakeEC2) DescribeRegions(context.Context, *ec2.DescribeRegionsInput, ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	return &ec2.DescribeRegionsOutput{}, nil
}

func (f fakeEC2) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if f.instancesErr != nil {
		return nil, f.instancesErr
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: f.instances}},
	}, nil
}

func (f fakeEC2) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	if f.volumesErr != nil {
		return nil, f.volumesErr
	}
	return &ec2.DescribeVolumesOutput{Volumes: f.volumes}, nil
}

func (f fakeEC2) DescribeAddresses(context.Context, *ec2.DescribeAddressesInput, ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	if f.addressesErr != nil {
		return nil, f.addressesErr
	}
	return &ec2.DescribeAddressesOutput{Addresses: f.addresses}, nil
}

func (f fakeEC2) DescribeNatGateways(context.Context, *ec2.DescribeNatGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	if f.natsErr != nil {
		return nil, f.natsErr
	}
	return &ec2.DescribeNatGatewaysOutput{NatGateways: f.nats}, nil
}

func (f fakeEC2) DescribeVpcs(context.Context, *ec2.DescribeVpcsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	if f.vpcsErr != nil {
		return nil, f.vpcsErr
	}
	return &ec2.DescribeVpcsOutput{Vpcs: f.vpcs}, nil
}

func TestListEC2ResourcesKeepsEarlierResultsWhenNATFails(t *testing.T) {
	t.Parallel()
	instances, volumes, eips, nats, vpcs, err := listEC2Resources(context.Background(), fakeEC2{
		instances: []ec2types.Instance{{
			InstanceId:   aws.String("i-1"),
			InstanceType: ec2types.InstanceTypeT3Micro,
			State:        &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
		}},
		volumes: []ec2types.Volume{{
			VolumeId: aws.String("vol-1"),
			Size:     aws.Int32(8),
			State:    ec2types.VolumeStateAvailable,
		}},
		addresses: []ec2types.Address{{PublicIp: aws.String("1.1.1.1")}},
		vpcs:      []ec2types.Vpc{{VpcId: aws.String("vpc-1")}},
		natsErr:   fmt.Errorf("nat denied"),
	}, "us-east-1")
	if err == nil || !strings.Contains(err.Error(), "nat-gateways") {
		t.Fatalf("error = %v, want nat-gateways", err)
	}
	if len(instances) != 1 || instances[0].InstanceID != "i-1" {
		t.Fatalf("instances = %+v", instances)
	}
	if len(volumes) != 1 || volumes[0].VolumeID != "vol-1" {
		t.Fatalf("volumes = %+v", volumes)
	}
	if len(eips) != 1 || eips[0].PublicIP != "1.1.1.1" {
		t.Fatalf("eips = %+v", eips)
	}
	if nats != nil {
		t.Fatalf("nats = %+v, want nil", nats)
	}
	if len(vpcs) != 1 || vpcs[0].VPCID != "vpc-1" {
		t.Fatalf("vpcs = %+v", vpcs)
	}
}

func TestListEC2ResourcesKeepsLaterResultsWhenInstancesFail(t *testing.T) {
	t.Parallel()
	instances, volumes, _, nats, _, err := listEC2Resources(context.Background(), fakeEC2{
		instancesErr: fmt.Errorf("instances denied"),
		volumes: []ec2types.Volume{{
			VolumeId: aws.String("vol-1"),
			Size:     aws.Int32(8),
			State:    ec2types.VolumeStateAvailable,
		}},
		nats: []ec2types.NatGateway{{
			NatGatewayId: aws.String("nat-1"),
			State:        ec2types.NatGatewayStateAvailable,
		}},
	}, "us-east-1")
	if err == nil || !strings.Contains(err.Error(), "instances") {
		t.Fatalf("error = %v, want instances", err)
	}
	if instances != nil {
		t.Fatalf("instances = %+v, want nil", instances)
	}
	if len(volumes) != 1 || volumes[0].VolumeID != "vol-1" {
		t.Fatalf("volumes = %+v", volumes)
	}
	if len(nats) != 1 || nats[0].GatewayID != "nat-1" {
		t.Fatalf("nats = %+v", nats)
	}
}
