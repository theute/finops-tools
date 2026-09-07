package inventory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type fakeELBv2 struct {
	names []string
	err   error
}

func (f fakeELBv2) DescribeLoadBalancers(
	context.Context,
	*elasticloadbalancingv2.DescribeLoadBalancersInput,
	...func(*elasticloadbalancingv2.Options),
) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := &elasticloadbalancingv2.DescribeLoadBalancersOutput{}
	for _, name := range f.names {
		n := name
		out.LoadBalancers = append(out.LoadBalancers, elbv2types.LoadBalancer{
			LoadBalancerName: &n,
			Type:             elbv2types.LoadBalancerTypeEnumApplication,
		})
	}
	return out, nil
}

type fakeClassicELB struct {
	names []string
	err   error
}

func (f fakeClassicELB) DescribeLoadBalancers(
	context.Context,
	*elasticloadbalancing.DescribeLoadBalancersInput,
	...func(*elasticloadbalancing.Options),
) (*elasticloadbalancing.DescribeLoadBalancersOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := &elasticloadbalancing.DescribeLoadBalancersOutput{}
	for _, name := range f.names {
		n := name
		out.LoadBalancerDescriptions = append(out.LoadBalancerDescriptions, elbtypes.LoadBalancerDescription{
			LoadBalancerName: &n,
		})
	}
	return out, nil
}

func TestListLoadBalancersKeepsV2WhenClassicFails(t *testing.T) {
	t.Parallel()
	got, err := listLoadBalancers(context.Background(), fakeELBv2{names: []string{"alb-1"}}, fakeClassicELB{err: fmt.Errorf("classic denied")}, "us-east-1")
	if err == nil {
		t.Fatal("expected classic error")
	}
	if len(got) != 1 || got[0].Name != "alb-1" {
		t.Fatalf("got %+v, want alb-1", got)
	}
}

func TestListLoadBalancersKeepsClassicWhenV2Fails(t *testing.T) {
	t.Parallel()
	got, err := listLoadBalancers(context.Background(), fakeELBv2{err: fmt.Errorf("v2 denied")}, fakeClassicELB{names: []string{"classic-1"}}, "us-east-1")
	if err == nil {
		t.Fatal("expected v2 error")
	}
	if len(got) != 1 || got[0].Name != "classic-1" {
		t.Fatalf("got %+v, want classic-1", got)
	}
}

type fakeS3 struct {
	buckets       []s3types.Bucket
	listErr       error
	locations     map[string]s3types.BucketLocationConstraint
	locationErrs  map[string]error
	locationCalls []string
}

func (f *fakeS3) ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &s3.ListBucketsOutput{Buckets: f.buckets}, nil
}

func (f *fakeS3) GetBucketLocation(_ context.Context, params *s3.GetBucketLocationInput, _ ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	name := aws.ToString(params.Bucket)
	f.locationCalls = append(f.locationCalls, name)
	if err := f.locationErrs[name]; err != nil {
		return nil, err
	}
	return &s3.GetBucketLocationOutput{LocationConstraint: f.locations[name]}, nil
}

func TestListS3BucketsEmptyConstraintIsUSEast1(t *testing.T) {
	t.Parallel()
	got, err := listS3Buckets(context.Background(), &fakeS3{
		buckets: []s3types.Bucket{{Name: aws.String("east")}},
	})
	if err != nil {
		t.Fatalf("listS3Buckets() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "east" || got[0].Region != "us-east-1" {
		t.Fatalf("got %+v, want east in us-east-1", got)
	}
}

func TestListS3BucketsUsesLocationConstraint(t *testing.T) {
	t.Parallel()
	got, err := listS3Buckets(context.Background(), &fakeS3{
		buckets: []s3types.Bucket{{Name: aws.String("eu-legacy")}, {Name: aws.String("west")}},
		locations: map[string]s3types.BucketLocationConstraint{
			"eu-legacy": s3types.BucketLocationConstraintEu,
			"west":      s3types.BucketLocationConstraintUsWest2,
		},
	})
	if err != nil {
		t.Fatalf("listS3Buckets() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d buckets, want 2", len(got))
	}
	if got[0].Region != "eu-west-1" || got[1].Region != "us-west-2" {
		t.Fatalf("regions = %+v", got)
	}
}

func TestListS3BucketsKeepsBucketWhenLocationFails(t *testing.T) {
	t.Parallel()
	got, err := listS3Buckets(context.Background(), &fakeS3{
		buckets: []s3types.Bucket{
			{Name: aws.String("ok"), BucketRegion: aws.String("eu-central-1")},
			{Name: aws.String("denied")},
		},
		locationErrs: map[string]error{"denied": fmt.Errorf("access denied")},
	})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error = %v, want denied", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want both buckets", got)
	}
	if got[0].Region != "eu-central-1" {
		t.Fatalf("listed region = %q", got[0].Region)
	}
	if got[1].Name != "denied" || got[1].Region != "" {
		t.Fatalf("failed bucket = %+v, want empty region", got[1])
	}
}

func TestListS3BucketsSkipsGetBucketLocationWhenListed(t *testing.T) {
	t.Parallel()
	fake := &fakeS3{
		buckets:      []s3types.Bucket{{Name: aws.String("ok"), BucketRegion: aws.String("ap-southeast-1")}},
		locationErrs: map[string]error{"ok": fmt.Errorf("should not be called")},
	}
	got, err := listS3Buckets(context.Background(), fake)
	if err != nil {
		t.Fatalf("listS3Buckets() error = %v", err)
	}
	if len(got) != 1 || got[0].Region != "ap-southeast-1" {
		t.Fatalf("got %+v", got)
	}
	if len(fake.locationCalls) != 0 {
		t.Fatalf("GetBucketLocation calls = %v, want none", fake.locationCalls)
	}
}
