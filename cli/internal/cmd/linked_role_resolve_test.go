package cmd

import (
	"path/filepath"
	"testing"

	"github.com/openshift-online/finops-tools/cli/internal/configstore"
	"github.com/spf13/cobra"
)

func TestSnapshotAndNotifyOwnerShareLinkedRoleFlag(t *testing.T) {
	snap := snapshotListCmd.Flags().Lookup("role")
	notify := accountNotifyOwnerCmd.Flags().Lookup("role")
	if snap == nil || notify == nil {
		t.Fatal("missing --role flag")
	}
	if snap.Usage != linkedRoleFlagHelp {
		t.Fatalf("snapshot --role help = %q", snap.Usage)
	}
	if notify.Usage != linkedRoleFlagHelp {
		t.Fatalf("notify-owner --role help = %q", notify.Usage)
	}
}

func TestResolveTargetLinkedRoleARNFlagOverridesAliasRole(t *testing.T) {
	cfg, path := testLinkedRoleConfig(t)
	cmd := testRoleFlagCmd()
	if err := cmd.Flags().Set("role", "InventoryRole"); err != nil {
		t.Fatal(err)
	}
	role, err := cmd.Flags().GetString("role")
	if err != nil {
		t.Fatal(err)
	}

	arn, err := resolveTargetLinkedRoleARN(cmd, cfg, path, "111111111111", "member", role)
	if err != nil {
		t.Fatal(err)
	}
	want := "arn:aws:iam::111111111111:role/InventoryRole"
	if arn != want {
		t.Fatalf("got %q want %q", arn, want)
	}
}

func TestResolveTargetLinkedRoleARNUsesAliasRoleWithoutFlag(t *testing.T) {
	cfg, path := testLinkedRoleConfig(t)
	cmd := testRoleFlagCmd()

	arn, err := resolveTargetLinkedRoleARN(cmd, cfg, path, "111111111111", "member", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "arn:aws:iam::111111111111:role/OldRole"
	if arn != want {
		t.Fatalf("got %q want %q", arn, want)
	}
}

func TestResolveTargetLinkedRoleARNFallsBackWithoutAlias(t *testing.T) {
	cfg, path := testLinkedRoleConfig(t)
	cmd := testRoleFlagCmd()

	arn, err := resolveTargetLinkedRoleARN(cmd, cfg, path, "222222222222", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "arn:aws:iam::222222222222:role/OrganizationAccountAccessRole"
	if arn != want {
		t.Fatalf("got %q want %q", arn, want)
	}
}

func testRoleFlagCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	var role string
	bindLinkedRoleFlag(cmd, &role)
	return cmd
}

func testLinkedRoleConfig(t *testing.T) (configstore.File, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := configstore.RegisterAWSAccount(path, "123456789012", "payer"); err != nil {
		t.Fatal(err)
	}
	if err := configstore.RegisterAWSLinkedAccount(path, "111111111111", "member", "payer", "OldRole"); err != nil {
		t.Fatal(err)
	}
	cfg, err := configstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, path
}
