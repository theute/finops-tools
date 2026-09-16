package snapshot

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/openshift-online/finops-tools/core/cost"
)

func awsConfigWithDefaultRegion(cfg aws.Config) aws.Config {
	if cfg.Region != "" {
		return cfg
	}
	out := cfg.Copy()
	out.Region = cost.CostExplorerRegion
	return out
}
