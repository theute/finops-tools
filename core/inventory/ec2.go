package inventory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type regionLister interface {
	ListEnabledRegions(ctx context.Context, cfg aws.Config, requested []string) ([]string, error)
}

type ec2RegionLister struct{}

func (ec2RegionLister) ListEnabledRegions(ctx context.Context, cfg aws.Config, requested []string) ([]string, error) {
	return listEnabledRegionsWithClient(ctx, newEC2Client(awsConfigWithDefaultRegion(cfg)), requested)
}

func awsConfigWithDefaultRegion(cfg aws.Config) aws.Config {
	if cfg.Region == "" {
		out := cfg.Copy()
		out.Region = "us-east-1"
		return out
	}
	return cfg
}

type EC2API interface {
	DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DescribeAddresses(ctx context.Context, params *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error)
	DescribeNatGateways(ctx context.Context, params *ec2.DescribeNatGatewaysInput, optFns ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error)
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
}

func newEC2Client(cfg aws.Config) EC2API {
	return ec2.NewFromConfig(cfg)
}

func listEnabledRegionsWithClient(ctx context.Context, client EC2API, requested []string) ([]string, error) {
	if len(requested) > 0 {
		out := make([]string, 0, len(requested))
		seen := make(map[string]struct{})
		for _, region := range requested {
			region = strings.TrimSpace(region)
			if region == "" {
				continue
			}
			if _, ok := seen[region]; ok {
				continue
			}
			seen[region] = struct{}{}
			out = append(out, region)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("at least one region is required")
		}
		sort.Strings(out)
		return out, nil
	}

	out, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: aws.Bool(false),
	})
	if err != nil {
		return nil, err
	}
	regions := make([]string, 0, len(out.Regions))
	for _, region := range out.Regions {
		if name := strings.TrimSpace(aws.ToString(region.RegionName)); name != "" {
			regions = append(regions, name)
		}
	}
	sort.Strings(regions)
	return regions, nil
}

// listEC2Resources returns independently listed EC2 resource types.
// A failure in one Describe still returns the others plus a combined error.
func listEC2Resources(ctx context.Context, client EC2API, region string) (
	instances []EC2Instance,
	unattached []EBSVolume,
	eips []ElasticIP,
	nats []NATGateway,
	vpcs []VPC,
	err error,
) {
	var errs []string

	instances, err = describeInstances(ctx, client, region)
	if err != nil {
		errs = append(errs, "instances: "+err.Error())
		instances = nil
	}
	unattached, err = describeUnattachedVolumes(ctx, client, region)
	if err != nil {
		errs = append(errs, "volumes: "+err.Error())
		unattached = nil
	}
	eips, err = describeUnassociatedEIPs(ctx, client, region)
	if err != nil {
		errs = append(errs, "addresses: "+err.Error())
		eips = nil
	}
	nats, err = describeNATGateways(ctx, client, region)
	if err != nil {
		errs = append(errs, "nat-gateways: "+err.Error())
		nats = nil
	}
	vpcs, err = describeVPCs(ctx, client, region)
	if err != nil {
		errs = append(errs, "vpcs: "+err.Error())
		vpcs = nil
	}

	if len(errs) > 0 {
		return instances, unattached, eips, nats, vpcs, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return instances, unattached, eips, nats, vpcs, nil
}

func describeInstances(ctx context.Context, client EC2API, region string) ([]EC2Instance, error) {
	var out []EC2Instance
	var token *string
	for {
		resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, res := range resp.Reservations {
			for _, inst := range res.Instances {
				out = append(out, EC2Instance{
					InstanceID: aws.ToString(inst.InstanceId),
					Name:       tagValue(inst.Tags, "Name"),
					Type:       string(inst.InstanceType),
					State:      string(inst.State.Name),
					Region:     region,
					LaunchTime: aws.ToTime(inst.LaunchTime),
				})
			}
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		token = resp.NextToken
	}
	return out, nil
}

func describeUnattachedVolumes(ctx context.Context, client EC2API, region string) ([]EBSVolume, error) {
	var out []EBSVolume
	var token *string
	for {
		resp, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			Filters: []ec2types.Filter{
				{Name: aws.String("status"), Values: []string{"available"}},
			},
			NextToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, vol := range resp.Volumes {
			out = append(out, EBSVolume{
				VolumeID: aws.ToString(vol.VolumeId),
				SizeGiB:  aws.ToInt32(vol.Size),
				Region:   region,
				State:    string(vol.State),
			})
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		token = resp.NextToken
	}
	return out, nil
}

func describeUnassociatedEIPs(ctx context.Context, client EC2API, region string) ([]ElasticIP, error) {
	resp, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		return nil, err
	}
	var out []ElasticIP
	for _, addr := range resp.Addresses {
		if addr.AssociationId != nil {
			continue
		}
		out = append(out, ElasticIP{
			PublicIP: aws.ToString(addr.PublicIp),
			Region:   region,
		})
	}
	return out, nil
}

func describeNATGateways(ctx context.Context, client EC2API, region string) ([]NATGateway, error) {
	var out []NATGateway
	var token *string
	for {
		resp, err := client.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, gw := range resp.NatGateways {
			if gw.State == ec2types.NatGatewayStateDeleted {
				continue
			}
			out = append(out, NATGateway{
				GatewayID: aws.ToString(gw.NatGatewayId),
				State:     string(gw.State),
				Region:    region,
			})
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		token = resp.NextToken
	}
	return out, nil
}

func describeVPCs(ctx context.Context, client EC2API, region string) ([]VPC, error) {
	var out []VPC
	var token *string
	for {
		resp, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, vpc := range resp.Vpcs {
			out = append(out, VPC{
				VPCID:     aws.ToString(vpc.VpcId),
				Region:    region,
				IsDefault: aws.ToBool(vpc.IsDefault),
			})
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		token = resp.NextToken
	}
	return out, nil
}

func tagValue(tags []ec2types.Tag, key string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}
