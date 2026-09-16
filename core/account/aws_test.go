package account

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/openshift-online/finops-tools/core/cost"
)

type fakeOrganizations struct {
	organizationsMigrateStub
	accounts map[string]string
}

func (f fakeOrganizations) DescribeAccount(
	_ context.Context,
	params *organizations.DescribeAccountInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeAccountOutput, error) {
	id := aws.ToString(params.AccountId)
	name, ok := f.accounts[id]
	if !ok {
		return nil, errors.New("AccountNotFoundException")
	}
	return &organizations.DescribeAccountOutput{
		Account: &types.Account{Id: params.AccountId, Name: aws.String(name)},
	}, nil
}

func (f fakeOrganizations) ListAccounts(
	_ context.Context,
	_ *organizations.ListAccountsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsOutput, error) {
	var accounts []types.Account
	for id, name := range f.accounts {
		accounts = append(accounts, types.Account{
			Id:   aws.String(id),
			Name: aws.String(name),
		})
	}
	return &organizations.ListAccountsOutput{Accounts: accounts}, nil
}

func (f fakeOrganizations) ListTagsForResource(
	_ context.Context,
	_ *organizations.ListTagsForResourceInput,
	_ ...func(*organizations.Options),
) (*organizations.ListTagsForResourceOutput, error) {
	return &organizations.ListTagsForResourceOutput{}, nil
}

func (f fakeOrganizations) ListTagsForAccount(
	_ context.Context,
	_ string,
	_ *string,
) ([]Tag, *string, error) {
	return nil, nil, nil
}

func (f fakeOrganizations) SetAccountTag(
	_ context.Context,
	_, _, _ string,
) error {
	return nil
}

func (f fakeOrganizations) DescribeOrganization(
	_ context.Context,
	_ *organizations.DescribeOrganizationInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeOrganizationOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizations) ListRoots(
	_ context.Context,
	_ *organizations.ListRootsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListRootsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizations) ListOrganizationalUnitsForParent(
	_ context.Context,
	_ *organizations.ListOrganizationalUnitsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListOrganizationalUnitsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizations) ListAccountsForParent(
	_ context.Context,
	_ *organizations.ListAccountsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

type fakeOrganizationsTags struct {
	organizationsMigrateStub
	pages []*organizations.ListTagsForResourceOutput
	err   error
	call  int
}

func (f *fakeOrganizationsTags) ListTagsForResource(
	_ context.Context,
	_ *organizations.ListTagsForResourceInput,
	_ ...func(*organizations.Options),
) (*organizations.ListTagsForResourceOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.call >= len(f.pages) {
		return &organizations.ListTagsForResourceOutput{}, nil
	}
	out := f.pages[f.call]
	f.call++
	return out, nil
}

func (f *fakeOrganizationsTags) DescribeAccount(
	_ context.Context,
	_ *organizations.DescribeAccountInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeAccountOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTags) ListAccounts(
	_ context.Context,
	_ *organizations.ListAccountsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTags) DescribeOrganization(
	_ context.Context,
	_ *organizations.DescribeOrganizationInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeOrganizationOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTags) ListRoots(
	_ context.Context,
	_ *organizations.ListRootsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListRootsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTags) ListOrganizationalUnitsForParent(
	_ context.Context,
	_ *organizations.ListOrganizationalUnitsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListOrganizationalUnitsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTags) ListAccountsForParent(
	_ context.Context,
	_ *organizations.ListAccountsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTags) ListTagsForAccount(
	ctx context.Context,
	_ string,
	token *string,
) ([]Tag, *string, error) {
	out, err := f.ListTagsForResource(ctx, &organizations.ListTagsForResourceInput{NextToken: token})
	if err != nil {
		return nil, nil, err
	}
	page := make([]Tag, 0, len(out.Tags))
	for _, tag := range out.Tags {
		page = append(page, Tag{
			Key:   aws.ToString(tag.Key),
			Value: aws.ToString(tag.Value),
		})
	}
	return page, out.NextToken, nil
}

func (f *fakeOrganizationsTags) SetAccountTag(
	_ context.Context,
	_, _, _ string,
) error {
	return errors.New("not implemented")
}

type fakeDescribeOrganizationClient struct {
	organizationsMigrateStub
	output *organizations.DescribeOrganizationOutput
	err    error
}

func (f fakeDescribeOrganizationClient) DescribeOrganization(
	_ context.Context,
	_ *organizations.DescribeOrganizationInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeOrganizationOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.output, nil
}

func (f fakeDescribeOrganizationClient) ListRoots(
	_ context.Context,
	_ *organizations.ListRootsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListRootsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeDescribeOrganizationClient) ListOrganizationalUnitsForParent(
	_ context.Context,
	_ *organizations.ListOrganizationalUnitsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListOrganizationalUnitsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeDescribeOrganizationClient) ListAccountsForParent(
	_ context.Context,
	_ *organizations.ListAccountsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeDescribeOrganizationClient) DescribeAccount(
	_ context.Context,
	_ *organizations.DescribeAccountInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeAccountOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeDescribeOrganizationClient) ListAccounts(
	_ context.Context,
	_ *organizations.ListAccountsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeDescribeOrganizationClient) ListTagsForResource(
	_ context.Context,
	_ *organizations.ListTagsForResourceInput,
	_ ...func(*organizations.Options),
) (*organizations.ListTagsForResourceOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeDescribeOrganizationClient) ListTagsForAccount(
	_ context.Context,
	_ string,
	_ *string,
) ([]Tag, *string, error) {
	return nil, nil, errors.New("not implemented")
}

func (f fakeDescribeOrganizationClient) SetAccountTag(
	_ context.Context,
	_, _, _ string,
) error {
	return errors.New("not implemented")
}

type fakeOrganizationsTagMutator struct {
	organizationsMigrateStub
	lastAccountID string
	lastTagKey    string
	lastTagValue  string
	err           error
}

func (f *fakeOrganizationsTagMutator) SetAccountTag(
	_ context.Context,
	accountID, tagKey, tagValue string,
) error {
	f.lastAccountID = accountID
	f.lastTagKey = tagKey
	f.lastTagValue = tagValue
	if f.err != nil {
		return f.err
	}
	return nil
}

func (f *fakeOrganizationsTagMutator) DescribeAccount(
	_ context.Context,
	_ *organizations.DescribeAccountInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeAccountOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTagMutator) ListAccounts(
	_ context.Context,
	_ *organizations.ListAccountsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTagMutator) ListTagsForResource(
	_ context.Context,
	_ *organizations.ListTagsForResourceInput,
	_ ...func(*organizations.Options),
) (*organizations.ListTagsForResourceOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTagMutator) ListTagsForAccount(
	_ context.Context,
	_ string,
	_ *string,
) ([]Tag, *string, error) {
	return nil, nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTagMutator) DescribeOrganization(
	_ context.Context,
	_ *organizations.DescribeOrganizationInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeOrganizationOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTagMutator) ListRoots(
	_ context.Context,
	_ *organizations.ListRootsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListRootsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTagMutator) ListOrganizationalUnitsForParent(
	_ context.Context,
	_ *organizations.ListOrganizationalUnitsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListOrganizationalUnitsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOrganizationsTagMutator) ListAccountsForParent(
	_ context.Context,
	_ *organizations.ListAccountsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

type fakeOrganizationsWithStatus struct {
	organizationsMigrateStub
	accounts map[string]struct {
		name   string
		status types.AccountStatus
	}
}

func (f fakeOrganizationsWithStatus) DescribeAccount(
	_ context.Context,
	params *organizations.DescribeAccountInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeAccountOutput, error) {
	id := aws.ToString(params.AccountId)
	entry, ok := f.accounts[id]
	if !ok {
		return nil, errors.New("AccountNotFoundException")
	}
	return &organizations.DescribeAccountOutput{
		Account: &types.Account{Id: params.AccountId, Name: aws.String(entry.name)},
	}, nil
}

func (f fakeOrganizationsWithStatus) ListAccounts(
	_ context.Context,
	_ *organizations.ListAccountsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsOutput, error) {
	var accounts []types.Account
	for id, entry := range f.accounts {
		accounts = append(accounts, types.Account{
			Id:     aws.String(id),
			Name:   aws.String(entry.name),
			Status: entry.status,
		})
	}
	return &organizations.ListAccountsOutput{Accounts: accounts}, nil
}

func (f fakeOrganizationsWithStatus) ListTagsForResource(
	_ context.Context,
	_ *organizations.ListTagsForResourceInput,
	_ ...func(*organizations.Options),
) (*organizations.ListTagsForResourceOutput, error) {
	return &organizations.ListTagsForResourceOutput{}, nil
}

func (f fakeOrganizationsWithStatus) ListTagsForAccount(
	_ context.Context,
	_ string,
	_ *string,
) ([]Tag, *string, error) {
	return nil, nil, nil
}

func (f fakeOrganizationsWithStatus) SetAccountTag(
	_ context.Context,
	_, _, _ string,
) error {
	return errors.New("not implemented")
}

func (f fakeOrganizationsWithStatus) DescribeOrganization(
	_ context.Context,
	_ *organizations.DescribeOrganizationInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeOrganizationOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizationsWithStatus) ListRoots(
	_ context.Context,
	_ *organizations.ListRootsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListRootsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizationsWithStatus) ListOrganizationalUnitsForParent(
	_ context.Context,
	_ *organizations.ListOrganizationalUnitsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListOrganizationalUnitsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizationsWithStatus) ListAccountsForParent(
	_ context.Context,
	_ *organizations.ListAccountsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func TestListOrganizationMemberAccountsWithClient(t *testing.T) {
	client := fakeOrganizationsWithStatus{
		accounts: map[string]struct {
			name   string
			status types.AccountStatus
		}{
			"123456789012": {name: "Payer", status: types.AccountStatusActive},
			"111111111111": {name: "Member One", status: types.AccountStatusActive},
			"222222222222": {name: "Member Two", status: types.AccountStatusActive},
			"333333333333": {name: "Suspended", status: types.AccountStatusSuspended},
		},
	}

	accounts, err := listOrganizationMemberAccountsWithClient(context.Background(), client, "123456789012")
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %+v", accounts)
	}
	ids := map[string]struct{}{}
	for _, acct := range accounts {
		ids[acct.ID] = struct{}{}
	}
	for _, want := range []string{"111111111111", "222222222222"} {
		if _, ok := ids[want]; !ok {
			t.Fatalf("missing account %s in %+v", want, accounts)
		}
	}
}

func TestListOrganizationMemberAccountsWithClientNoMembers(t *testing.T) {
	client := fakeOrganizationsWithStatus{
		accounts: map[string]struct {
			name   string
			status types.AccountStatus
		}{
			"123456789012": {name: "Payer", status: types.AccountStatusActive},
		},
	}
	_, err := listOrganizationMemberAccountsWithClient(context.Background(), client, "123456789012")
	if err == nil {
		t.Fatal("expected error when no member accounts remain")
	}
}

func TestAccountName(t *testing.T) {
	client := fakeOrganizations{accounts: map[string]string{
		"111111111111": "Quay Production",
	}}
	name, err := accountNameWithClient(context.Background(), client, "111111111111")
	if err != nil || name != "Quay Production" {
		t.Fatalf("name = %q err = %v", name, err)
	}
	_, err = accountNameWithClient(context.Background(), client, "999999999999")
	if err == nil {
		t.Fatal("expected error for missing account")
	}
}

func TestListAccountNames(t *testing.T) {
	client := fakeOrganizations{accounts: map[string]string{
		"111111111111": "Quay Production",
		"222222222222": "Staging",
	}}
	names, err := listAccountNamesWithClient(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if names["111111111111"] != "Quay Production" || names["222222222222"] != "Staging" {
		t.Fatalf("names = %+v", names)
	}
}

func TestResolveAccountNamesDescribePerID(t *testing.T) {
	client := fakeOrganizations{accounts: map[string]string{
		"111111111111": "Quay Production",
		"222222222222": "Staging",
	}}
	names, err := resolveAccountNamesWithClient(context.Background(), client, []string{"111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	if names["111111111111"] != "Quay Production" {
		t.Fatalf("names = %+v", names)
	}
}

func TestResolveAccountNamesUniqueIDs(t *testing.T) {
	ids := cost.UniqueAccountIDs([]string{" 111 ", "111", ""})
	if len(ids) != 1 || ids[0] != "111" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestListTagsWithClient(t *testing.T) {
	client := &fakeOrganizationsTags{
		pages: []*organizations.ListTagsForResourceOutput{
			{
				Tags: []types.Tag{
					{Key: aws.String("owner"), Value: aws.String("team-b")},
					{Key: aws.String("env"), Value: aws.String("prod")},
				},
				NextToken: aws.String("page-2"),
			},
			{
				Tags: []types.Tag{
					{Key: aws.String("owner"), Value: aws.String("team-a")},
				},
			},
		},
	}

	tags, err := listTagsWithClient(context.Background(), client, "123456789012")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tags))
	}
	if tags[0].Key != "env" || tags[0].Value != "prod" {
		t.Fatalf("unexpected first tag: %+v", tags[0])
	}
	if tags[1].Key != "owner" || tags[1].Value != "team-a" {
		t.Fatalf("unexpected second tag: %+v", tags[1])
	}
	if tags[2].Key != "owner" || tags[2].Value != "team-b" {
		t.Fatalf("unexpected third tag: %+v", tags[2])
	}
}

func TestListTagsWithClientValidationAndErrors(t *testing.T) {
	client := &fakeOrganizationsTags{}
	if _, err := listTagsWithClient(context.Background(), client, " "); err == nil {
		t.Fatal("expected account ID validation error")
	}

	wantErr := errors.New("boom")
	client.err = wantErr
	_, err := listTagsWithClient(context.Background(), client, "123456789012")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error %v, got %v", wantErr, err)
	}
}

func TestSetAccountTagWithClient(t *testing.T) {
	client := &fakeOrganizationsTagMutator{}
	err := setAccountTagWithClient(context.Background(), client, "123456789012", "owner", "team-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := client.lastAccountID; got != "123456789012" {
		t.Fatalf("resource id = %q", got)
	}
	if got := client.lastTagKey; got != "owner" {
		t.Fatalf("tag key = %q", got)
	}
	if got := client.lastTagValue; got != "team-a" {
		t.Fatalf("tag value = %q", got)
	}
}

func TestSetAccountTagWithClientValidationAndErrors(t *testing.T) {
	client := &fakeOrganizationsTagMutator{}
	for _, tc := range []struct {
		name      string
		accountID string
		tagKey    string
		tagValue  string
	}{
		{name: "missing account id", accountID: " ", tagKey: "owner", tagValue: "team-a"},
		{name: "missing tag key", accountID: "123456789012", tagKey: " ", tagValue: "team-a"},
		{name: "missing tag value", accountID: "123456789012", tagKey: "owner", tagValue: " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := setAccountTagWithClient(context.Background(), client, tc.accountID, tc.tagKey, tc.tagValue); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	wantErr := errors.New("boom")
	client.err = wantErr
	err := setAccountTagWithClient(context.Background(), client, "123456789012", "owner", "team-a")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error %v, got %v", wantErr, err)
	}
}

func TestDetectAccountKindWithClientPayer(t *testing.T) {
	client := fakeDescribeOrganizationClient{
		output: &organizations.DescribeOrganizationOutput{
			Organization: organizationWithManagementAccountID("123456789012"),
		},
	}
	kind, err := detectAccountKindWithClient(context.Background(), client, "123456789012")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind != AccountKindPayer {
		t.Fatalf("kind = %q, want %q", kind, AccountKindPayer)
	}
}

func TestDetectAccountKindWithClientLinked(t *testing.T) {
	client := fakeDescribeOrganizationClient{
		output: &organizations.DescribeOrganizationOutput{
			Organization: organizationWithManagementAccountID("999999999999"),
		},
	}
	kind, err := detectAccountKindWithClient(context.Background(), client, "123456789012")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind != AccountKindLinked {
		t.Fatalf("kind = %q, want %q", kind, AccountKindLinked)
	}
}

func TestDetectAccountKindWithClientUnknownOnDescribeFailure(t *testing.T) {
	client := fakeDescribeOrganizationClient{err: errors.New("access denied")}
	kind, err := detectAccountKindWithClient(context.Background(), client, "123456789012")
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != AccountKindUnknown {
		t.Fatalf("kind = %q, want %q", kind, AccountKindUnknown)
	}
}

func TestOrganizationManagementAccountIDFallsBackToOrganizationID(t *testing.T) {
	org := &types.Organization{Id: aws.String("123456789012")}
	if got := organizationManagementAccountID(org); got != "123456789012" {
		t.Fatalf("got %q want %q", got, "123456789012")
	}
}

func organizationWithManagementAccountID(accountID string) *types.Organization {
	org := &types.Organization{}
	v := reflect.ValueOf(org).Elem()
	for _, fieldName := range []string{"ManagementAccountId", "MasterAccountId"} {
		field := v.FieldByName(fieldName)
		if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Ptr || field.Type().Elem().Kind() != reflect.String {
			continue
		}
		id := accountID
		field.Set(reflect.ValueOf(&id))
		return org
	}
	org.Id = aws.String(accountID)
	return org
}

type fakeOUHierarchy struct {
	organizationsMigrateStub
	roots            []string
	childOUs         map[string][]OrganizationalUnit
	accountsByParent map[string][]types.Account
}

func (f fakeOUHierarchy) DescribeAccount(
	_ context.Context,
	params *organizations.DescribeAccountInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeAccountOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOUHierarchy) ListAccounts(
	_ context.Context,
	_ *organizations.ListAccountsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOUHierarchy) ListTagsForAccount(
	_ context.Context,
	_ string,
	_ *string,
) ([]Tag, *string, error) {
	return nil, nil, errors.New("not implemented")
}

func (f fakeOUHierarchy) SetAccountTag(
	_ context.Context,
	_, _, _ string,
) error {
	return errors.New("not implemented")
}

func (f fakeOUHierarchy) DescribeOrganization(
	_ context.Context,
	_ *organizations.DescribeOrganizationInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeOrganizationOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOUHierarchy) ListRoots(
	_ context.Context,
	_ *organizations.ListRootsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListRootsOutput, error) {
	roots := make([]types.Root, 0, len(f.roots))
	for _, id := range f.roots {
		roots = append(roots, types.Root{Id: aws.String(id), Name: aws.String("Root")})
	}
	return &organizations.ListRootsOutput{Roots: roots}, nil
}

func (f fakeOUHierarchy) ListOrganizationalUnitsForParent(
	_ context.Context,
	params *organizations.ListOrganizationalUnitsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListOrganizationalUnitsForParentOutput, error) {
	parentID := aws.ToString(params.ParentId)
	children := f.childOUs[parentID]
	out := make([]types.OrganizationalUnit, 0, len(children))
	for _, ou := range children {
		out = append(out, types.OrganizationalUnit{
			Id:   aws.String(ou.ID),
			Name: aws.String(ou.Name),
		})
	}
	return &organizations.ListOrganizationalUnitsForParentOutput{OrganizationalUnits: out}, nil
}

func (f fakeOUHierarchy) ListAccountsForParent(
	_ context.Context,
	params *organizations.ListAccountsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsForParentOutput, error) {
	parentID := aws.ToString(params.ParentId)
	return &organizations.ListAccountsForParentOutput{Accounts: f.accountsByParent[parentID]}, nil
}

func testOUHierarchy() fakeOUHierarchy {
	return fakeOUHierarchy{
		roots: []string{"r-root"},
		childOUs: map[string][]OrganizationalUnit{
			"r-root": {
				{ID: "ou-root-prod0000", Name: "Production"},
				{ID: "ou-root-sandbox0", Name: "Sandbox"},
			},
			"ou-root-prod0000": {
				{ID: "ou-prod-teama000", Name: "Team A"},
			},
		},
		accountsByParent: map[string][]types.Account{
			"ou-root-prod0000": {
				{Id: aws.String("111111111111"), Name: aws.String("Prod One"), Status: types.AccountStatusActive},
				{Id: aws.String("222222222222"), Name: aws.String("Prod Two"), Status: types.AccountStatusActive},
			},
			"ou-prod-teama000": {
				{Id: aws.String("333333333333"), Name: aws.String("Team A One"), Status: types.AccountStatusActive},
			},
			"ou-root-sandbox0": {
				{Id: aws.String("444444444444"), Name: aws.String("Sandbox One"), Status: types.AccountStatusActive},
				{Id: aws.String("555555555555"), Name: aws.String("Suspended"), Status: types.AccountStatusSuspended},
			},
		},
	}
}

type fakeOrganizationsFilterByTag struct {
	organizationsMigrateStub
	accounts map[string]string
	tags     map[string][]Tag
}

func (f fakeOrganizationsFilterByTag) DescribeAccount(
	_ context.Context,
	params *organizations.DescribeAccountInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeAccountOutput, error) {
	id := aws.ToString(params.AccountId)
	name, ok := f.accounts[id]
	if !ok {
		return nil, errors.New("AccountNotFoundException")
	}
	return &organizations.DescribeAccountOutput{
		Account: &types.Account{Id: params.AccountId, Name: aws.String(name)},
	}, nil
}

func (f fakeOrganizationsFilterByTag) ListAccounts(
	_ context.Context,
	_ *organizations.ListAccountsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsOutput, error) {
	var accounts []types.Account
	for id, name := range f.accounts {
		accounts = append(accounts, types.Account{
			Id:   aws.String(id),
			Name: aws.String(name),
		})
	}
	return &organizations.ListAccountsOutput{Accounts: accounts}, nil
}

func (f fakeOrganizationsFilterByTag) ListTagsForAccount(
	_ context.Context,
	accountID string,
	_ *string,
) ([]Tag, *string, error) {
	return f.tags[accountID], nil, nil
}

func (f fakeOrganizationsFilterByTag) SetAccountTag(
	_ context.Context,
	_, _, _ string,
) error {
	return errors.New("not implemented")
}

func (f fakeOrganizationsFilterByTag) DescribeOrganization(
	_ context.Context,
	_ *organizations.DescribeOrganizationInput,
	_ ...func(*organizations.Options),
) (*organizations.DescribeOrganizationOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizationsFilterByTag) ListRoots(
	_ context.Context,
	_ *organizations.ListRootsInput,
	_ ...func(*organizations.Options),
) (*organizations.ListRootsOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizationsFilterByTag) ListOrganizationalUnitsForParent(
	_ context.Context,
	_ *organizations.ListOrganizationalUnitsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListOrganizationalUnitsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func (f fakeOrganizationsFilterByTag) ListAccountsForParent(
	_ context.Context,
	_ *organizations.ListAccountsForParentInput,
	_ ...func(*organizations.Options),
) (*organizations.ListAccountsForParentOutput, error) {
	return nil, errors.New("not implemented")
}

func TestValidateOUID(t *testing.T) {
	if err := ValidateOUID("ou-abcd-12345678"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateOUID("ou-abcd-1234"); err == nil {
		t.Fatal("expected error for short OU suffix")
	}
	if err := ValidateOUID(""); err == nil {
		t.Fatal("expected error for empty OU ID")
	}
	if err := ValidateOUID("r-root"); err == nil {
		t.Fatal("expected error for root ID")
	}
}

func TestListOrganizationalUnitsWithClient(t *testing.T) {
	client := testOUHierarchy()
	ous, err := listOrganizationalUnitsWithClient(context.Background(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ous) != 2 || ous[0].Name != "Production" || ous[1].Name != "Sandbox" {
		t.Fatalf("ous = %+v", ous)
	}

	childOUs, err := listOrganizationalUnitsWithClient(context.Background(), client, "ou-root-prod0000")
	if err != nil {
		t.Fatal(err)
	}
	if len(childOUs) != 1 || childOUs[0].ID != "ou-prod-teama000" {
		t.Fatalf("childOUs = %+v", childOUs)
	}
}

func TestListAccountsInOUDirectOnly(t *testing.T) {
	client := testOUHierarchy()
	accounts, err := listAccountsInOUWithClient(context.Background(), client, "ou-root-prod0000", ListAccountsInOUOptions{DirectOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestListAccountsInOURecursive(t *testing.T) {
	client := testOUHierarchy()
	accounts, err := listAccountsInOUWithClient(context.Background(), client, "ou-root-prod0000", ListAccountsInOUOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("accounts = %+v", accounts)
	}
	ids := map[string]struct{}{}
	for _, acct := range accounts {
		ids[acct.ID] = struct{}{}
	}
	for _, want := range []string{"111111111111", "222222222222", "333333333333"} {
		if _, ok := ids[want]; !ok {
			t.Fatalf("missing account %s in %+v", want, accounts)
		}
	}
}

func TestListAccountsInOUSkipsSuspended(t *testing.T) {
	client := testOUHierarchy()
	accounts, err := listAccountsInOUWithClient(context.Background(), client, "ou-root-sandbox0", ListAccountsInOUOptions{DirectOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].ID != "444444444444" {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestListAccountsInOUMaxDepthChildren(t *testing.T) {
	client := testOUHierarchy()
	// Depth 1 under ou-root-prod0000: direct accounts + ou-prod-teama000 accounts (no deeper OUs anyway).
	accounts, err := listAccountsInOUWithClient(context.Background(), client, "ou-root-prod0000", ListAccountsInOUOptions{
		MaxDepth: OUDepthPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("accounts = %+v", accounts)
	}

	// Depth 0 via MaxDepth matches DirectOnly.
	direct, err := listAccountsInOUWithClient(context.Background(), client, "ou-root-prod0000", ListAccountsInOUOptions{
		MaxDepth: OUDepthPtr(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 2 {
		t.Fatalf("direct = %+v", direct)
	}
}

func TestBuildOUAccountMapping(t *testing.T) {
	client := testOUHierarchy()
	ids := []string{"111111111111", "222222222222", "333333333333", "444444444444"}
	parents, hierarchy, err := buildOUAccountMappingWithClient(context.Background(), client, "r-root", ids)
	if err != nil {
		t.Fatal(err)
	}
	if parents["111111111111"].ID != "ou-root-prod0000" {
		t.Fatalf("111 parent = %+v", parents["111111111111"])
	}
	if parents["333333333333"].ID != "ou-prod-teama000" || parents["333333333333"].Name != "Team A" {
		t.Fatalf("333 should map to immediate parent Team A, got %+v", parents["333333333333"])
	}
	if parents["444444444444"].ID != "ou-root-sandbox0" {
		t.Fatalf("444 parent = %+v", parents["444444444444"])
	}
	if len(hierarchy) < 4 {
		t.Fatalf("hierarchy = %+v", hierarchy)
	}
	if hierarchy[0].ID != "r-root" || hierarchy[0].Depth != 0 {
		t.Fatalf("root node = %+v", hierarchy[0])
	}
	foundTeam := false
	for _, n := range hierarchy {
		if n.ID == "ou-prod-teama000" && n.Depth == 2 && n.ParentID == "ou-root-prod0000" {
			foundTeam = true
		}
	}
	if !foundTeam {
		t.Fatalf("missing Team A in hierarchy: %+v", hierarchy)
	}
}

func TestMapAccountsToChildOUs(t *testing.T) {
	client := testOUHierarchy()
	ids := []string{"111111111111", "222222222222", "333333333333", "444444444444"}
	got, err := mapAccountsToChildOUsWithClient(context.Background(), client, "r-root", ids)
	if err != nil {
		t.Fatal(err)
	}
	if got["111111111111"].ID != "ou-root-prod0000" || got["111111111111"].Name != "Production" {
		t.Fatalf("111 = %+v", got["111111111111"])
	}
	if got["333333333333"].ID != "ou-root-prod0000" {
		t.Fatalf("333 should roll up to Production child OU, got %+v", got["333333333333"])
	}
	if got["444444444444"].ID != "ou-root-sandbox0" || got["444444444444"].Name != "Sandbox" {
		t.Fatalf("444 = %+v", got["444444444444"])
	}
}

func TestMapAccountsToChildOUsDirectOnOU(t *testing.T) {
	client := testOUHierarchy()
	got, err := mapAccountsToChildOUsWithClient(context.Background(), client, "ou-root-prod0000", []string{
		"111111111111", "333333333333",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["111111111111"].ID != "ou-root-prod0000" {
		t.Fatalf("direct member should map to selection root, got %+v", got["111111111111"])
	}
	if got["333333333333"].ID != "ou-prod-teama000" || got["333333333333"].Name != "Team A" {
		t.Fatalf("333 = %+v", got["333333333333"])
	}
}

func TestFilterOrganizationAccountsByTagKeyOnly(t *testing.T) {
	client := fakeOrganizationsFilterByTag{
		accounts: map[string]string{
			"111111111111": "Prod",
			"222222222222": "Stage",
			"333333333333": "Dev",
		},
		tags: map[string][]Tag{
			"111111111111": {{Key: "env", Value: "prod"}},
			"222222222222": {{Key: "env", Value: "stage"}},
			"333333333333": {{Key: "owner", Value: "team-a"}},
		},
	}

	matches, err := filterOrganizationAccountsByTagWithClient(context.Background(), client, "env", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(matches), matches)
	}
}

func TestFilterOrganizationAccountsByTagExactValue(t *testing.T) {
	client := fakeOrganizationsFilterByTag{
		accounts: map[string]string{
			"111111111111": "Prod",
			"222222222222": "Stage",
		},
		tags: map[string][]Tag{
			"111111111111": {{Key: "env", Value: "prod"}},
			"222222222222": {{Key: "env", Value: "stage"}},
		},
	}

	matches, err := filterOrganizationAccountsByTagWithClient(context.Background(), client, "env", "prod", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ID != "111111111111" {
		t.Fatalf("matches = %+v", matches)
	}
}

func TestFilterOrganizationAccountsByTagDuplicateKeys(t *testing.T) {
	client := fakeOrganizationsFilterByTag{
		accounts: map[string]string{
			"111111111111": "Shared",
		},
		tags: map[string][]Tag{
			"111111111111": {
				{Key: "owner", Value: "team-a"},
				{Key: "owner", Value: "team-b"},
			},
		},
	}

	matches, err := filterOrganizationAccountsByTagWithClient(context.Background(), client, "owner", "team-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
}

func TestFilterOrganizationAccountsByTagNoMatch(t *testing.T) {
	client := fakeOrganizationsFilterByTag{
		accounts: map[string]string{"111111111111": "Prod"},
		tags:     map[string][]Tag{"111111111111": {{Key: "env", Value: "prod"}}},
	}

	matches, err := filterOrganizationAccountsByTagWithClient(context.Background(), client, "env", "stage", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(matches))
	}
}

func TestFilterOrganizationAccountsFromScan(t *testing.T) {
	scan := []OrganizationAccountTags{
		{
			Account: OrganizationAccount{ID: "111111111111", Name: "Prod"},
			Tags:    []Tag{{Key: "env", Value: "prod"}},
		},
		{
			Account: OrganizationAccount{ID: "222222222222", Name: "Stage"},
			Tags:    []Tag{{Key: "env", Value: "stage"}},
		},
	}
	matches := FilterOrganizationAccountsFromScan(scan, "env", "prod", nil)
	if len(matches) != 1 || matches[0].ID != "111111111111" {
		t.Fatalf("matches = %+v", matches)
	}
}

func TestFilterOrganizationAccountsByTagValidation(t *testing.T) {
	client := fakeOrganizationsFilterByTag{}
	if _, err := filterOrganizationAccountsByTagWithClient(context.Background(), client, " ", "", nil); err == nil {
		t.Fatal("expected tag key validation error")
	}
}

func TestFilterOrganizationAccountsByTagProgress(t *testing.T) {
	client := fakeOrganizationsFilterByTag{
		accounts: map[string]string{
			"111111111111": "Prod",
			"222222222222": "Stage",
		},
		tags: map[string][]Tag{
			"111111111111": {{Key: "env", Value: "prod"}},
			"222222222222": {{Key: "env", Value: "stage"}},
		},
	}
	rec := &recordingTagFilterProgress{}
	_, err := filterOrganizationAccountsByTagWithClient(context.Background(), client, "env", "", rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.steps) < 3 {
		t.Fatalf("expected at least 3 progress steps, got %d: %v", len(rec.steps), rec.steps)
	}
}

type recordingTagFilterProgress struct {
	steps []string
}

func (r *recordingTagFilterProgress) Step(message string) {
	r.steps = append(r.steps, message)
}
