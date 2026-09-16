package account

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/openshift-online/finops-tools/core/progress"
)

const (
	organizationsRegion      = "us-east-1"
	accountNameListThreshold = 50
)

// Tag is one AWS Organizations account tag.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// OrganizationAccount is one AWS Organizations account directory entry.
type OrganizationAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OrganizationalUnit is one AWS Organizations OU directory entry.
type OrganizationalUnit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListAccountsInOUOptions configures ListAccountsInOU.
type ListAccountsInOUOptions struct {
	// MaxDepth limits how deep to walk child OUs from the starting OU.
	// nil (default) means unbounded descendants. 0 = accounts directly in the OU only;
	// 1 = the OU plus immediate child OUs; and so on. Negative values mean unbounded.
	MaxDepth *int
	// DirectOnly lists accounts directly in ouID only (not descendant OUs).
	// Deprecated: prefer MaxDepth pointing at 0; when true, overrides MaxDepth.
	DirectOnly bool
	// Status filters accounts by Organizations status (default ACTIVE).
	Status string
}

// AccountOUBucket is the OU rollup bucket for an account under a selection root.
type AccountOUBucket struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OUHierarchyNode is one node in an OU tree under a selection root (DFS pre-order).
type OUHierarchyNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`
	Depth    int    `json:"depth"`
}

// OUDepthPtr returns a pointer to depth for ListAccountsInOUOptions.MaxDepth.
func OUDepthPtr(depth int) *int {
	return &depth
}

// OrganizationAccountTags is one organization account and its Organizations tags.
type OrganizationAccountTags struct {
	Account OrganizationAccount `json:"account"`
	Tags    []Tag               `json:"tags"`
}

// TagFilterProgress reports long-running steps while filtering accounts by tag.
type TagFilterProgress = progress.Reporter

// AccountKind describes whether a validated account session is payer or linked.
type AccountKind string

const (
	AccountKindPayer   AccountKind = "payer"
	AccountKindLinked  AccountKind = "linked"
	AccountKindUnknown AccountKind = "unknown"
)

// Query identifies a target account and the credentials used to query it.
type Query struct {
	AccountID string
	AWSConfig aws.Config
}
