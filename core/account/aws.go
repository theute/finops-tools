package account

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/openshift-online/finops-tools/core/cost"
)

// ListTags returns AWS Organizations tags for accountID.
func ListTags(ctx context.Context, cfg aws.Config, accountID string) ([]Tag, error) {
	return listTagsWithClient(ctx, newOrganizationsClient(cfg), accountID)
}

// SetAccountTag adds or updates one AWS Organizations tag on accountID.
func SetAccountTag(ctx context.Context, cfg aws.Config, accountID, tagKey, tagValue string) error {
	return setAccountTagWithClient(ctx, newOrganizationsClient(cfg), accountID, tagKey, tagValue)
}

func listTagsWithClient(ctx context.Context, client OrganizationsAPI, accountID string) ([]Tag, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("account ID is required")
	}

	tags := make([]Tag, 0)
	var token *string
	for {
		var pageTags []Tag
		var nextToken *string
		err := retryOnThrottle(ctx, defaultThrottleRetries, func() error {
			var callErr error
			pageTags, nextToken, callErr = client.ListTagsForAccount(ctx, accountID, token)
			return callErr
		})
		if err != nil {
			return nil, err
		}
		tags = append(tags, pageTags...)
		if nextToken == nil || aws.ToString(nextToken) == "" {
			break
		}
		token = nextToken
	}

	slices.SortFunc(tags, func(a, b Tag) int {
		if cmp := strings.Compare(a.Key, b.Key); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Value, b.Value)
	})
	return tags, nil
}

func setAccountTagWithClient(ctx context.Context, client OrganizationsAPI, accountID, tagKey, tagValue string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("account ID is required")
	}
	tagKey = strings.TrimSpace(tagKey)
	if tagKey == "" {
		return fmt.Errorf("tag key is required")
	}
	tagValue = strings.TrimSpace(tagValue)
	if tagValue == "" {
		return fmt.Errorf("tag value is required")
	}

	return client.SetAccountTag(ctx, accountID, tagKey, tagValue)
}

// AccountName returns the AWS Organizations account name for accountID.
func AccountName(ctx context.Context, cfg aws.Config, accountID string) (string, error) {
	return accountNameWithClient(ctx, newOrganizationsClient(cfg), accountID)
}

func accountNameWithClient(ctx context.Context, client OrganizationsAPI, accountID string) (string, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", fmt.Errorf("account ID is required")
	}
	out, err := client.DescribeAccount(ctx, &organizations.DescribeAccountInput{
		AccountId: aws.String(accountID),
	})
	if err != nil {
		return "", err
	}
	return accountNameFromOrganizationAccount(out.Account, accountID)
}

// ListAccountNames returns a map of account ID to AWS Organizations account name.
func ListAccountNames(ctx context.Context, cfg aws.Config) (map[string]string, error) {
	return listAccountNamesWithClient(ctx, newOrganizationsClient(cfg))
}

func listAccountNamesWithClient(ctx context.Context, client OrganizationsAPI) (map[string]string, error) {
	accounts, err := listAllOrganizationAccounts(ctx, client)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(accounts))
	for _, acct := range accounts {
		if name, err := accountNameFromOrganizationAccount(&acct, aws.ToString(acct.Id)); err == nil {
			names[aws.ToString(acct.Id)] = name
		}
	}
	return names, nil
}

// ResolveAccountNames returns display names for the given account IDs.
// For small sets it uses DescribeAccount per ID; for larger sets it lists the organization once.
func ResolveAccountNames(ctx context.Context, cfg aws.Config, accountIDs []string) (map[string]string, error) {
	return resolveAccountNamesWithClient(ctx, newOrganizationsClient(cfg), accountIDs)
}

func resolveAccountNamesWithClient(ctx context.Context, client OrganizationsAPI, accountIDs []string) (map[string]string, error) {
	ids := cost.UniqueAccountIDs(accountIDs)
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	if len(ids) > accountNameListThreshold {
		all, err := listAccountNamesWithClient(ctx, client)
		if err != nil {
			return nil, err
		}
		out := make(map[string]string, len(ids))
		for _, id := range ids {
			if name, ok := all[id]; ok {
				out[id] = name
			}
		}
		return out, nil
	}

	out := make(map[string]string, len(ids))
	for _, id := range ids {
		name, err := accountNameWithClient(ctx, client, id)
		if err != nil {
			continue
		}
		out[id] = name
	}
	return out, nil
}

// FilterOrganizationAccountsByTag returns org accounts whose tags match the filter.
// tagKey is required. When tagValue is empty, any account with that key matches.
func FilterOrganizationAccountsByTag(ctx context.Context, cfg aws.Config, tagKey, tagValue string) ([]OrganizationAccount, error) {
	return FilterOrganizationAccountsByTagWithProgress(ctx, cfg, tagKey, tagValue, nil)
}

// FilterOrganizationAccountsByTagWithProgress is like FilterOrganizationAccountsByTag but emits optional progress steps.
func FilterOrganizationAccountsByTagWithProgress(ctx context.Context, cfg aws.Config, tagKey, tagValue string, progress TagFilterProgress) ([]OrganizationAccount, error) {
	scan, err := ScanOrganizationAccountTagsWithProgress(ctx, cfg, progress)
	if err != nil {
		return nil, err
	}
	return FilterOrganizationAccountsFromScan(scan, tagKey, tagValue, progress), nil
}

// ScanOrganizationAccountTagsWithProgress lists all organization accounts and their Organizations tags.
func ScanOrganizationAccountTagsWithProgress(ctx context.Context, cfg aws.Config, progress TagFilterProgress) ([]OrganizationAccountTags, error) {
	return scanOrganizationAccountTagsWithClient(ctx, newOrganizationsClient(cfg), progress)
}

// FilterOrganizationAccountsFromScan returns accounts in scan whose tags match the filter.
func FilterOrganizationAccountsFromScan(scan []OrganizationAccountTags, tagKey, tagValue string, progress TagFilterProgress) []OrganizationAccount {
	tagKey = strings.TrimSpace(tagKey)
	tagValue = strings.TrimSpace(tagValue)

	out := make([]OrganizationAccount, 0)
	for _, item := range scan {
		if accountTagsMatchFilter(item.Tags, tagKey, tagValue) {
			out = append(out, item.Account)
		}
	}
	tagFilterStep(progress, fmt.Sprintf("Matched %d account(s) with tag key %q", len(out), tagKey))
	return out
}

func filterOrganizationAccountsByTagWithClient(ctx context.Context, client OrganizationsAPI, tagKey, tagValue string, progress TagFilterProgress) ([]OrganizationAccount, error) {
	tagKey = strings.TrimSpace(tagKey)
	if tagKey == "" {
		return nil, fmt.Errorf("tag key is required")
	}

	scan, err := scanOrganizationAccountTagsWithClient(ctx, client, progress)
	if err != nil {
		return nil, err
	}
	return FilterOrganizationAccountsFromScan(scan, tagKey, tagValue, progress), nil
}

func scanOrganizationAccountTagsWithClient(ctx context.Context, client OrganizationsAPI, progress TagFilterProgress) ([]OrganizationAccountTags, error) {
	tagFilterStep(progress, "Listing organization accounts…")
	accounts, err := listOrganizationAccountsWithClient(ctx, client, "")
	if err != nil {
		return nil, err
	}
	tagFilterStep(progress, fmt.Sprintf("Found %d organization accounts; checking tags…", len(accounts)))

	out := make([]OrganizationAccountTags, 0, len(accounts))
	for i, acct := range accounts {
		reportTagCheckProgress(progress, i, len(accounts))
		tags, err := listTagsWithClient(ctx, client, acct.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, OrganizationAccountTags{
			Account: acct,
			Tags:    tags,
		})
	}
	return out, nil
}

func tagFilterStep(progress TagFilterProgress, message string) {
	if progress == nil {
		return
	}
	progress.Step(message)
}

func reportTagCheckProgress(progress TagFilterProgress, index, total int) {
	if progress == nil || total == 0 {
		return
	}
	if index == 0 || index == total-1 || (index+1)%25 == 0 {
		tagFilterStep(progress, fmt.Sprintf("Checking organization tags (%d/%d)…", index+1, total))
	}
}

func accountTagsMatchFilter(tags []Tag, tagKey, tagValue string) bool {
	for _, tag := range tags {
		if tag.Key != tagKey {
			continue
		}
		if tagValue == "" || tag.Value == tagValue {
			return true
		}
	}
	return false
}

func listOrganizationAccountsWithClient(ctx context.Context, client OrganizationsAPI, statusFilter string) ([]OrganizationAccount, error) {
	accounts, err := listAllOrganizationAccounts(ctx, client)
	if err != nil {
		return nil, err
	}
	out := make([]OrganizationAccount, 0, len(accounts))
	for _, acct := range accounts {
		if statusFilter != "" && string(acct.Status) != statusFilter {
			continue
		}
		name, err := accountNameFromOrganizationAccount(&acct, aws.ToString(acct.Id))
		if err != nil {
			continue
		}
		out = append(out, OrganizationAccount{
			ID:   strings.TrimSpace(aws.ToString(acct.Id)),
			Name: name,
		})
	}
	return out, nil
}

// listAllOrganizationAccounts pages through every ListAccounts result, deduping
// the ListAccounts NextToken loop shared by listAccountNamesWithClient and
// listOrganizationAccountsWithClient.
func listAllOrganizationAccounts(ctx context.Context, client OrganizationsAPI) ([]types.Account, error) {
	var all []types.Account
	var token *string
	for {
		out, err := client.ListAccounts(ctx, &organizations.ListAccountsInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		all = append(all, out.Accounts...)
		if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
			break
		}
		token = out.NextToken
	}
	return all, nil
}

// ListOrganizationAccounts returns all organization accounts.
func ListOrganizationAccounts(ctx context.Context, cfg aws.Config) ([]OrganizationAccount, error) {
	return listOrganizationAccountsWithClient(ctx, newOrganizationsClient(cfg), "")
}

// ListOrganizationMemberAccounts returns ACTIVE organization accounts excluding excludeAccountID (typically the payer).
func ListOrganizationMemberAccounts(ctx context.Context, cfg aws.Config, excludeAccountID string) ([]OrganizationAccount, error) {
	return listOrganizationMemberAccountsWithClient(ctx, newOrganizationsClient(cfg), excludeAccountID)
}

func listOrganizationMemberAccountsWithClient(ctx context.Context, client OrganizationsAPI, excludeAccountID string) ([]OrganizationAccount, error) {
	excludeAccountID = strings.TrimSpace(excludeAccountID)
	accounts, err := listOrganizationAccountsWithClient(ctx, client, string(types.AccountStatusActive))
	if err != nil {
		return nil, err
	}
	out := make([]OrganizationAccount, 0, len(accounts))
	for _, acct := range accounts {
		if excludeAccountID != "" && acct.ID == excludeAccountID {
			continue
		}
		out = append(out, acct)
	}
	if len(out) == 0 {
		return nil, errors.New("no active member accounts found in organization")
	}
	return out, nil
}

// ListOrganizationalUnits returns child OUs under parentID.
// When parentID is empty, the organization root is used.
func ListOrganizationalUnits(ctx context.Context, cfg aws.Config, parentID string) ([]OrganizationalUnit, error) {
	return listOrganizationalUnitsWithClient(ctx, newOrganizationsClient(cfg), parentID)
}

func listOrganizationalUnitsWithClient(ctx context.Context, client OrganizationsAPI, parentID string) ([]OrganizationalUnit, error) {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		rootID, err := firstRootID(ctx, client)
		if err != nil {
			return nil, err
		}
		parentID = rootID
	}

	var token *string
	out := make([]OrganizationalUnit, 0)
	for {
		resp, err := client.ListOrganizationalUnitsForParent(ctx, &organizations.ListOrganizationalUnitsForParentInput{
			ParentId:  aws.String(parentID),
			NextToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("list organizational units for parent %s: %w", parentID, err)
		}
		for _, ou := range resp.OrganizationalUnits {
			id := strings.TrimSpace(aws.ToString(ou.Id))
			name := strings.TrimSpace(aws.ToString(ou.Name))
			if id == "" {
				continue
			}
			out = append(out, OrganizationalUnit{ID: id, Name: name})
		}
		if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
			break
		}
		token = resp.NextToken
	}

	slices.SortFunc(out, func(a, b OrganizationalUnit) int {
		if cmp := strings.Compare(a.Name, b.Name); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// ListAccountsInOU returns active accounts in an OU, recursively by default.
func ListAccountsInOU(ctx context.Context, cfg aws.Config, ouID string, opts ListAccountsInOUOptions) ([]OrganizationAccount, error) {
	return listAccountsInOUWithClient(ctx, newOrganizationsClient(cfg), ouID, opts)
}

// ListAccountsUnderParent returns active accounts under an OU or organization root ID,
// with the same depth options as ListAccountsInOU.
func ListAccountsUnderParent(ctx context.Context, cfg aws.Config, parentID string, opts ListAccountsInOUOptions) ([]OrganizationAccount, error) {
	parentID = strings.TrimSpace(parentID)
	if err := ValidateParentID(parentID); err != nil {
		return nil, err
	}
	return listAccountsUnderParentWithClient(ctx, newOrganizationsClient(cfg), parentID, opts)
}

func listAccountsInOUWithClient(ctx context.Context, client OrganizationsAPI, ouID string, opts ListAccountsInOUOptions) ([]OrganizationAccount, error) {
	ouID = strings.TrimSpace(ouID)
	if err := ValidateOUID(ouID); err != nil {
		return nil, err
	}
	return listAccountsUnderParentWithClient(ctx, client, ouID, opts)
}

func listAccountsUnderParentWithClient(ctx context.Context, client OrganizationsAPI, parentID string, opts ListAccountsInOUOptions) ([]OrganizationAccount, error) {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return nil, fmt.Errorf("parent ID is required")
	}

	statusFilter := strings.TrimSpace(opts.Status)
	if statusFilter == "" {
		statusFilter = string(types.AccountStatusActive)
	}

	maxDepth, unbounded := effectiveOUMaxDepth(opts)

	seen := make(map[string]struct{})
	out := make([]OrganizationAccount, 0)

	collectAccounts := func(id string) error {
		accounts, err := listAccountsForParentWithClient(ctx, client, id, statusFilter)
		if err != nil {
			return err
		}
		for _, acct := range accounts {
			if _, ok := seen[acct.ID]; ok {
				continue
			}
			seen[acct.ID] = struct{}{}
			out = append(out, acct)
		}
		return nil
	}

	type queueItem struct {
		id    string
		depth int
	}
	queue := []queueItem{{id: parentID, depth: 0}}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if err := collectAccounts(item.id); err != nil {
			return nil, err
		}
		if !unbounded && item.depth >= maxDepth {
			continue
		}

		childOUs, err := listOrganizationalUnitsWithClient(ctx, client, item.id)
		if err != nil {
			return nil, err
		}
		for _, child := range childOUs {
			queue = append(queue, queueItem{id: child.ID, depth: item.depth + 1})
		}
	}

	slices.SortFunc(out, func(a, b OrganizationAccount) int {
		if cmp := strings.Compare(a.Name, b.Name); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func effectiveOUMaxDepth(opts ListAccountsInOUOptions) (maxDepth int, unbounded bool) {
	if opts.DirectOnly {
		return 0, false
	}
	if opts.MaxDepth == nil {
		return 0, true
	}
	if *opts.MaxDepth < 0 {
		return 0, true
	}
	return *opts.MaxDepth, false
}

// MapAccountsToChildOUs maps each account ID to the immediate child OU of rootID that
// contains it, or to rootID itself when the account is a direct member of rootID.
// rootID may be an OU ID (ou-…) or organization root ID (r-…).
// Prefer BuildOUAccountMapping for --group-by ou tree output.
func MapAccountsToChildOUs(ctx context.Context, cfg aws.Config, rootID string, accountIDs []string) (map[string]AccountOUBucket, error) {
	return mapAccountsToChildOUsWithClient(ctx, newOrganizationsClient(cfg), rootID, accountIDs)
}

// BuildOUAccountMapping walks the OU tree under rootID and returns:
//   - parents: each wanted account → its immediate parent OU (or root)
//   - hierarchy: all OU/root nodes under rootID in DFS pre-order (for tree display)
func BuildOUAccountMapping(ctx context.Context, cfg aws.Config, rootID string, accountIDs []string) (map[string]AccountOUBucket, []OUHierarchyNode, error) {
	return buildOUAccountMappingWithClient(ctx, newOrganizationsClient(cfg), rootID, accountIDs)
}

func buildOUAccountMappingWithClient(ctx context.Context, client OrganizationsAPI, rootID string, accountIDs []string) (map[string]AccountOUBucket, []OUHierarchyNode, error) {
	rootID = strings.TrimSpace(rootID)
	if err := ValidateParentID(rootID); err != nil {
		return nil, nil, err
	}

	wanted := make(map[string]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		wanted[id] = struct{}{}
	}

	rootName, err := parentDisplayName(ctx, client, rootID)
	if err != nil {
		return nil, nil, err
	}

	parents := make(map[string]AccountOUBucket, len(wanted))
	hierarchy := make([]OUHierarchyNode, 0)

	var walk func(id, name, parentID string, depth int) error
	walk = func(id, name, parentID string, depth int) error {
		hierarchy = append(hierarchy, OUHierarchyNode{
			ID:       id,
			Name:     name,
			ParentID: parentID,
			Depth:    depth,
		})
		bucket := AccountOUBucket{ID: id, Name: name}

		directAccounts, err := listAccountsForParentWithClient(ctx, client, id, string(types.AccountStatusActive))
		if err != nil {
			return err
		}
		for _, acct := range directAccounts {
			if _, ok := wanted[acct.ID]; ok {
				parents[acct.ID] = bucket
			}
		}

		childOUs, err := listOrganizationalUnitsWithClient(ctx, client, id)
		if err != nil {
			return err
		}
		for _, child := range childOUs {
			childName := child.Name
			if childName == "" {
				childName = child.ID
			}
			if err := walk(child.ID, childName, id, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(rootID, rootName, "", 0); err != nil {
		return nil, nil, err
	}
	return parents, hierarchy, nil
}

func mapAccountsToChildOUsWithClient(ctx context.Context, client OrganizationsAPI, rootID string, accountIDs []string) (map[string]AccountOUBucket, error) {
	rootID = strings.TrimSpace(rootID)
	if err := ValidateParentID(rootID); err != nil {
		return nil, err
	}

	wanted := make(map[string]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		wanted[id] = struct{}{}
	}
	if len(wanted) == 0 {
		return map[string]AccountOUBucket{}, nil
	}

	rootName, err := parentDisplayName(ctx, client, rootID)
	if err != nil {
		return nil, err
	}
	rootBucket := AccountOUBucket{ID: rootID, Name: rootName}

	out := make(map[string]AccountOUBucket, len(wanted))

	directAccounts, err := listAccountsForParentWithClient(ctx, client, rootID, string(types.AccountStatusActive))
	if err != nil {
		return nil, err
	}
	for _, acct := range directAccounts {
		if _, ok := wanted[acct.ID]; ok {
			out[acct.ID] = rootBucket
		}
	}

	childOUs, err := listOrganizationalUnitsWithClient(ctx, client, rootID)
	if err != nil {
		return nil, err
	}
	for _, child := range childOUs {
		childBucket := AccountOUBucket(child)
		accounts, err := listAccountsUnderParentWithClient(ctx, client, child.ID, ListAccountsInOUOptions{})
		if err != nil {
			return nil, fmt.Errorf("list accounts under OU %s: %w", child.ID, err)
		}
		for _, acct := range accounts {
			if _, ok := wanted[acct.ID]; !ok {
				continue
			}
			if _, already := out[acct.ID]; already {
				continue
			}
			out[acct.ID] = childBucket
		}
	}

	return out, nil
}

func parentDisplayName(ctx context.Context, client OrganizationsAPI, parentID string) (string, error) {
	if strings.HasPrefix(parentID, "r-") {
		var token *string
		for {
			resp, err := client.ListRoots(ctx, &organizations.ListRootsInput{NextToken: token})
			if err != nil {
				return "", fmt.Errorf("list organization roots: %w", err)
			}
			for _, root := range resp.Roots {
				if strings.TrimSpace(aws.ToString(root.Id)) == parentID {
					name := strings.TrimSpace(aws.ToString(root.Name))
					if name == "" {
						return parentID, nil
					}
					return name, nil
				}
			}
			if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
				break
			}
			token = resp.NextToken
		}
		return parentID, nil
	}
	// OU display names are resolved from parent listings when available; fall back to ID.
	return parentID, nil
}

// OrganizationRootID returns the first organization root ID for cfg.
func OrganizationRootID(ctx context.Context, cfg aws.Config) (string, error) {
	return firstRootID(ctx, newOrganizationsClient(cfg))
}

func listAccountsForParentWithClient(ctx context.Context, client OrganizationsAPI, parentID, statusFilter string) ([]OrganizationAccount, error) {
	var token *string
	out := make([]OrganizationAccount, 0)
	for {
		resp, err := client.ListAccountsForParent(ctx, &organizations.ListAccountsForParentInput{
			ParentId:  aws.String(parentID),
			NextToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("list accounts for parent %s: %w", parentID, err)
		}
		for _, acct := range resp.Accounts {
			if statusFilter != "" && string(acct.Status) != statusFilter {
				continue
			}
			name, err := accountNameFromOrganizationAccount(&acct, aws.ToString(acct.Id))
			if err != nil {
				continue
			}
			out = append(out, OrganizationAccount{
				ID:   strings.TrimSpace(aws.ToString(acct.Id)),
				Name: name,
			})
		}
		if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
			break
		}
		token = resp.NextToken
	}
	return out, nil
}

func firstRootID(ctx context.Context, client OrganizationsAPI) (string, error) {
	var token *string
	for {
		resp, err := client.ListRoots(ctx, &organizations.ListRootsInput{NextToken: token})
		if err != nil {
			return "", fmt.Errorf("list organization roots: %w", err)
		}
		for _, root := range resp.Roots {
			id := strings.TrimSpace(aws.ToString(root.Id))
			if id != "" {
				return id, nil
			}
		}
		if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
			break
		}
		token = resp.NextToken
	}
	return "", fmt.Errorf("list organization roots: no root found")
}

// DetectAccountKind classifies callerAccountID against organization management account.
func DetectAccountKind(ctx context.Context, cfg aws.Config, callerAccountID string) (AccountKind, error) {
	callerAccountID = strings.TrimSpace(callerAccountID)
	if callerAccountID == "" {
		return AccountKindUnknown, fmt.Errorf("caller account ID is required")
	}
	return detectAccountKindWithClient(ctx, newOrganizationsClient(cfg), callerAccountID)
}

func detectAccountKindWithClient(ctx context.Context, client OrganizationsAPI, callerAccountID string) (AccountKind, error) {
	out, err := client.DescribeOrganization(ctx, &organizations.DescribeOrganizationInput{})
	if err != nil {
		return AccountKindUnknown, fmt.Errorf("describe organization: %w", err)
	}

	managementAccountID := organizationManagementAccountID(out.Organization)
	if managementAccountID == "" {
		return AccountKindUnknown, fmt.Errorf("describe organization: missing management account ID")
	}
	if managementAccountID == callerAccountID {
		return AccountKindPayer, nil
	}
	return AccountKindLinked, nil
}

func accountNameFromOrganizationAccount(acct *types.Account, accountID string) (string, error) {
	if acct == nil {
		return "", fmt.Errorf("account %s not found", accountID)
	}
	name := strings.TrimSpace(aws.ToString(acct.Name))
	if name == "" {
		return "", fmt.Errorf("account %s has no name", accountID)
	}
	return name, nil
}

func organizationManagementAccountID(org *types.Organization) string {
	if org == nil {
		return ""
	}
	v := reflect.ValueOf(*org)
	for _, field := range []string{"ManagementAccountId", "MasterAccountId"} {
		accountID := stringFieldValue(v, field)
		if accountID != "" {
			return accountID
		}
	}
	return strings.TrimSpace(aws.ToString(org.Id))
}

func stringFieldValue(v reflect.Value, fieldName string) string {
	field := v.FieldByName(fieldName)
	if !field.IsValid() || field.Kind() != reflect.Ptr || field.IsNil() {
		return ""
	}
	str, ok := field.Interface().(*string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(aws.ToString(str))
}
