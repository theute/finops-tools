package inventory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/openshift-online/finops-tools/core/parallel"
)

const defaultRegionConcurrency = 5

// Scan discovers resources across accounts and regions.
// Per-account failures (for example listing regions or loading credentials) are
// recorded on that account's Warnings and do not abort the rest of the batch.
func Scan(ctx context.Context, q Query) (Result, error) {
	q = q.withDefaults()
	if len(q.Targets) == 0 {
		return Result{}, fmt.Errorf("at least one account target is required")
	}

	accounts := make([]AccountInventory, len(q.Targets))
	err := parallel.ForEach(ctx, q.Workers, len(q.Targets), func(ctx context.Context, i int) error {
		target := q.Targets[i]
		reportScanProgress(q.OnProgress, target, i+1, len(q.Targets))
		inv, err := q.scanner.scanAccount(ctx, q, target)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			// Isolate account-level failures so one bad assume-role or region list
			// does not cancel inventory (and emails) for the rest of the batch.
			accounts[i] = AccountInventory{
				AccountID: strings.TrimSpace(target.AccountID),
				Warnings:  []string{err.Error()},
			}
			return nil
		}
		accounts[i] = inv
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Accounts: accounts}, nil
}

func reportScanProgress(onProgress func(string), target AccountTarget, index, total int) {
	if onProgress == nil || !parallel.ShouldReportProgress(index, total) {
		return
	}
	label := strings.TrimSpace(target.AccountID)
	if name := strings.TrimSpace(target.DisplayName); name != "" && name != label {
		label = fmt.Sprintf("%s (%s)", name, label)
	} else if alias := strings.TrimSpace(target.DisplayAlias); alias != "" && alias != label {
		label = fmt.Sprintf("%s (%s)", alias, label)
	}
	onProgress(fmt.Sprintf("Scanning inventory for %s [%d/%d]…", label, index, total))
}

func (q Query) withDefaults() Query {
	if q.Now.IsZero() {
		q.Now = time.Now().UTC()
	}
	if q.regionLister == nil {
		q.regionLister = ec2RegionLister{}
	}
	if q.scanner == nil {
		q.scanner = defaultAccountScanner{}
	}
	return q
}

type accountScanner interface {
	scanAccount(ctx context.Context, q Query, target AccountTarget) (AccountInventory, error)
}

type defaultAccountScanner struct{}

func (defaultAccountScanner) scanAccount(ctx context.Context, q Query, target AccountTarget) (AccountInventory, error) {
	cfg, err := loadTargetConfig(ctx, target)
	if err != nil {
		return AccountInventory{}, err
	}

	regions, err := q.regionLister.ListEnabledRegions(ctx, cfg, q.Regions)
	if err != nil {
		return AccountInventory{}, fmt.Errorf("list regions: %w", err)
	}

	inv := AccountInventory{AccountID: strings.TrimSpace(target.AccountID)}
	var (
		mu       sync.Mutex
		warnings []RegionWarning
	)

	err = parallel.ForEach(ctx, defaultRegionConcurrency, len(regions), func(ctx context.Context, ri int) error {
		region := regions[ri]
		regionCfg := awsConfigForRegion(cfg, region)
		scanRegionalResources(ctx, regionCfg, region, target.AccountID, &inv, &mu, &warnings)
		return nil
	})
	if err != nil {
		return AccountInventory{}, err
	}

	// Route53 and S3 are global; scan once with us-east-1 config.
	// Failures go to Warnings instead of aborting the account scan.
	globalCfg := awsConfigForRegion(cfg, "us-east-1")
	scanGlobalResources(ctx, globalCfg, &inv)

	sortInventory(&inv)
	inv.SkippedRegions = sortRegionWarnings(warnings)
	return inv, nil
}

func loadTargetConfig(ctx context.Context, target AccountTarget) (aws.Config, error) {
	if target.ConfigLoader != nil {
		return target.ConfigLoader(ctx)
	}
	return target.AWSConfig, nil
}

func awsConfigForRegion(cfg aws.Config, region string) aws.Config {
	out := cfg.Copy()
	out.Region = region
	return out
}

var (
	listRegionalEC2    = listEC2Resources
	listRegionalRDS    = listRDSResources
	listRegionalLBs    = listLoadBalancers
	listRegionalLambda = listLambdaFunctions
	listGlobalRoute53  = listHostedZones
	listGlobalS3       = listS3Buckets
)

// scanRegionalResources lists EC2, RDS, ELB, and Lambda in one region.
// A failure for one service is recorded as a RegionWarning; other services in
// that region are still collected.
func scanRegionalResources(
	ctx context.Context,
	cfg aws.Config,
	region, accountID string,
	inv *AccountInventory,
	mu *sync.Mutex,
	warnings *[]RegionWarning,
) {
	record := func(service string, err error) {
		if err == nil {
			return
		}
		mu.Lock()
		*warnings = append(*warnings, RegionWarning{
			AccountID: accountID,
			Region:    region,
			Message:   service + ": " + err.Error(),
		})
		mu.Unlock()
	}

	ec2Inst, volumes, eips, nats, vpcs, err := listRegionalEC2(ctx, newEC2Client(cfg), region)
	if err != nil {
		record("ec2", err)
	}
	// Keep partial EC2 results when only some Describes failed.
	if len(ec2Inst) > 0 || len(volumes) > 0 || len(eips) > 0 || len(nats) > 0 || len(vpcs) > 0 {
		mu.Lock()
		inv.EC2Instances = append(inv.EC2Instances, ec2Inst...)
		inv.UnattachedEBS = append(inv.UnattachedEBS, volumes...)
		inv.ElasticIPs = append(inv.ElasticIPs, eips...)
		inv.NATGateways = append(inv.NATGateways, nats...)
		inv.VPCs = append(inv.VPCs, vpcs...)
		mu.Unlock()
	}

	rdsInst, rdsClusters, err := listRegionalRDS(ctx, newRDSClient(cfg), region)
	if err != nil {
		record("rds", err)
	}
	// Keep partial RDS results when only instances or only clusters failed.
	if len(rdsInst) > 0 || len(rdsClusters) > 0 {
		mu.Lock()
		inv.RDSInstances = append(inv.RDSInstances, rdsInst...)
		inv.RDSClusters = append(inv.RDSClusters, rdsClusters...)
		mu.Unlock()
	}

	lbs, err := listRegionalLBs(ctx, newELBV2Client(cfg), newELBClient(cfg), region)
	if err != nil {
		record("elb", err)
	}
	// Keep partial ELB results when only classic or only v2 failed.
	if len(lbs) > 0 {
		mu.Lock()
		inv.LoadBalancers = append(inv.LoadBalancers, lbs...)
		mu.Unlock()
	}

	lambdas, err := listRegionalLambda(ctx, newLambdaClient(cfg), region)
	if err != nil {
		record("lambda", err)
	} else {
		mu.Lock()
		inv.LambdaFunctions = append(inv.LambdaFunctions, lambdas...)
		mu.Unlock()
	}
}

func scanGlobalResources(ctx context.Context, cfg aws.Config, inv *AccountInventory) {
	if zones, err := listGlobalRoute53(ctx, newRoute53Client(cfg)); err != nil {
		inv.Warnings = append(inv.Warnings, "route53: "+err.Error())
	} else {
		inv.HostedZones = zones
	}
	buckets, err := listGlobalS3(ctx, newS3Client(cfg))
	if err != nil {
		inv.Warnings = append(inv.Warnings, "s3: "+err.Error())
	}
	// Keep buckets whose names we listed even when some GetBucketLocation calls failed.
	if len(buckets) > 0 {
		inv.S3Buckets = buckets
	}
}

func sortInventory(inv *AccountInventory) {
	sort.Slice(inv.EC2Instances, func(i, j int) bool {
		return inv.EC2Instances[i].InstanceID < inv.EC2Instances[j].InstanceID
	})
	sort.Slice(inv.RDSInstances, func(i, j int) bool {
		return inv.RDSInstances[i].InstanceID < inv.RDSInstances[j].InstanceID
	})
	sort.Slice(inv.RDSClusters, func(i, j int) bool {
		return inv.RDSClusters[i].ClusterID < inv.RDSClusters[j].ClusterID
	})
	sort.Slice(inv.HostedZones, func(i, j int) bool {
		return inv.HostedZones[i].Name < inv.HostedZones[j].Name
	})
	sort.Slice(inv.UnattachedEBS, func(i, j int) bool {
		return inv.UnattachedEBS[i].VolumeID < inv.UnattachedEBS[j].VolumeID
	})
	sort.Slice(inv.ElasticIPs, func(i, j int) bool {
		return inv.ElasticIPs[i].PublicIP < inv.ElasticIPs[j].PublicIP
	})
	sort.Slice(inv.LoadBalancers, func(i, j int) bool {
		return inv.LoadBalancers[i].Name < inv.LoadBalancers[j].Name
	})
	sort.Slice(inv.NATGateways, func(i, j int) bool {
		return inv.NATGateways[i].GatewayID < inv.NATGateways[j].GatewayID
	})
	sort.Slice(inv.S3Buckets, func(i, j int) bool {
		return inv.S3Buckets[i].Name < inv.S3Buckets[j].Name
	})
	sort.Slice(inv.LambdaFunctions, func(i, j int) bool {
		return inv.LambdaFunctions[i].Name < inv.LambdaFunctions[j].Name
	})
	sort.Slice(inv.VPCs, func(i, j int) bool {
		return inv.VPCs[i].VPCID < inv.VPCs[j].VPCID
	})
}

func sortRegionWarnings(warnings []RegionWarning) []RegionWarning {
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].Region == warnings[j].Region {
			return warnings[i].Message < warnings[j].Message
		}
		return warnings[i].Region < warnings[j].Region
	})
	return warnings
}
