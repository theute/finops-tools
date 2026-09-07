package inventory

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Route53API interface {
	ListHostedZones(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error)
	ListResourceRecordSets(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
}

type ELBV2API interface {
	DescribeLoadBalancers(ctx context.Context, params *elasticloadbalancingv2.DescribeLoadBalancersInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error)
}

type ELBAPI interface {
	DescribeLoadBalancers(ctx context.Context, params *elasticloadbalancing.DescribeLoadBalancersInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DescribeLoadBalancersOutput, error)
}

type LambdaAPI interface {
	ListFunctions(ctx context.Context, params *lambda.ListFunctionsInput, optFns ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error)
}

type S3API interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketLocation(ctx context.Context, params *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
}

func newRoute53Client(cfg aws.Config) Route53API {
	return route53.NewFromConfig(cfg)
}

func newELBV2Client(cfg aws.Config) ELBV2API {
	return elasticloadbalancingv2.NewFromConfig(cfg)
}

func newELBClient(cfg aws.Config) ELBAPI {
	return elasticloadbalancing.NewFromConfig(cfg)
}

func newLambdaClient(cfg aws.Config) LambdaAPI {
	return lambda.NewFromConfig(cfg)
}

func newS3Client(cfg aws.Config) S3API {
	return s3.NewFromConfig(cfg)
}

func listHostedZones(ctx context.Context, client Route53API) ([]HostedZone, error) {
	var out []HostedZone
	var marker *string
	for {
		resp, err := client.ListHostedZones(ctx, &route53.ListHostedZonesInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		for _, zone := range resp.HostedZones {
			recordCount := aws.ToInt64(zone.ResourceRecordSetCount)
			out = append(out, HostedZone{
				ZoneID:      aws.ToString(zone.Id),
				Name:        aws.ToString(zone.Name),
				PrivateZone: zone.Config != nil && zone.Config.PrivateZone,
				RecordCount: recordCount,
			})
		}
		if !resp.IsTruncated {
			break
		}
		marker = resp.NextMarker
	}
	return out, nil
}

// listLoadBalancers returns ALB/NLB and Classic ELB results independently.
// A failure in one API still returns the other's load balancers plus a combined error.
func listLoadBalancers(ctx context.Context, v2 ELBV2API, classic ELBAPI, region string) ([]LoadBalancer, error) {
	var (
		out  []LoadBalancer
		errs []string
	)

	v2LBs, err := listELBv2LoadBalancers(ctx, v2, region)
	if err != nil {
		errs = append(errs, "elbv2: "+err.Error())
	} else {
		out = append(out, v2LBs...)
	}

	classicLBs, err := listClassicLoadBalancers(ctx, classic, region)
	if err != nil {
		errs = append(errs, "classic: "+err.Error())
	} else {
		out = append(out, classicLBs...)
	}

	if len(errs) > 0 {
		return out, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return out, nil
}

func listELBv2LoadBalancers(ctx context.Context, v2 ELBV2API, region string) ([]LoadBalancer, error) {
	var out []LoadBalancer
	var marker *string
	for {
		resp, err := v2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		for _, lb := range resp.LoadBalancers {
			state := ""
			if lb.State != nil {
				state = string(lb.State.Code)
			}
			out = append(out, LoadBalancer{
				Name:   aws.ToString(lb.LoadBalancerName),
				Type:   string(lb.Type),
				State:  state,
				Region: region,
			})
		}
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		marker = resp.NextMarker
	}
	return out, nil
}

func listClassicLoadBalancers(ctx context.Context, classic ELBAPI, region string) ([]LoadBalancer, error) {
	var out []LoadBalancer
	classicMarker := ""
	for {
		input := &elasticloadbalancing.DescribeLoadBalancersInput{}
		if classicMarker != "" {
			input.Marker = aws.String(classicMarker)
		}
		resp, err := classic.DescribeLoadBalancers(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, lb := range resp.LoadBalancerDescriptions {
			out = append(out, LoadBalancer{
				Name:   aws.ToString(lb.LoadBalancerName),
				Type:   "classic",
				State:  "",
				Region: region,
			})
		}
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		classicMarker = aws.ToString(resp.NextMarker)
	}
	return out, nil
}

func listLambdaFunctions(ctx context.Context, client LambdaAPI, region string) ([]LambdaFunction, error) {
	var out []LambdaFunction
	var marker *string
	for {
		resp, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		for _, fn := range resp.Functions {
			out = append(out, LambdaFunction{
				Name:   aws.ToString(fn.FunctionName),
				Region: region,
			})
		}
		if resp.NextMarker == nil || *resp.NextMarker == "" {
			break
		}
		marker = resp.NextMarker
	}
	return out, nil
}

func listS3Buckets(ctx context.Context, client S3API) ([]S3Bucket, error) {
	resp, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}
	out := make([]S3Bucket, 0, len(resp.Buckets))
	var errs []string
	for _, bucket := range resp.Buckets {
		name := aws.ToString(bucket.Name)
		region, locErr := resolveS3BucketRegion(ctx, client, name, aws.ToString(bucket.BucketRegion))
		if locErr != nil {
			errs = append(errs, name+": "+locErr.Error())
		}
		out = append(out, S3Bucket{Name: name, Region: region})
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return out, nil
}

// resolveS3BucketRegion prefers the region from ListBuckets when present.
// GetBucketLocation returns an empty constraint for us-east-1; a failed lookup
// must not inherit that default.
func resolveS3BucketRegion(ctx context.Context, client S3API, name, listedRegion string) (string, error) {
	if listedRegion != "" {
		return listedRegion, nil
	}
	loc, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(name)})
	if err != nil {
		return "", err
	}
	switch string(loc.LocationConstraint) {
	case "":
		return "us-east-1", nil
	case "EU":
		return "eu-west-1", nil
	default:
		return string(loc.LocationConstraint), nil
	}
}
