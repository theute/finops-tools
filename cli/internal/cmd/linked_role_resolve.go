// linked_role_resolve.go resolves the IAM role name and ARN for linked-account access from flags and config.
package cmd

import (
	"strings"

	"github.com/openshift-online/finops-tools/cli/internal/awsrole"
	"github.com/openshift-online/finops-tools/cli/internal/configstore"
	"github.com/spf13/cobra"
)

// linkedRoleFlagHelp is the --role flag text for commands that assume into linked accounts.
const linkedRoleFlagHelp = "Linked-account IAM role name (overrides the alias role when set; default: alias role, then config defaults.aws.linked_role)"

func bindLinkedRoleFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "role", "", linkedRoleFlagHelp)
}

// resolveLinkedRoleName returns the IAM role name: --role when set, else config default, else built-in default.
func resolveLinkedRoleName(cmd *cobra.Command, configPath, flagValue string) (string, error) {
	if cmd.Flags().Changed("role") {
		name := strings.TrimSpace(flagValue)
		if name == "" {
			return "", nil
		}
		if strings.HasPrefix(name, "arn:") {
			return awsrole.NameFromARN(name), nil
		}
		if err := awsrole.ValidateName(name); err != nil {
			return "", err
		}
		return name, nil
	}
	path, err := configstore.ResolvePath(configPath)
	if err != nil {
		return "", err
	}
	cfg, err := configstore.Load(path)
	if err != nil {
		return "", err
	}
	return cfg.AWSLinkedRoleName(), nil
}

// resolveLinkedRoleARN builds the role ARN for a linked account.
func resolveLinkedRoleARN(cmd *cobra.Command, configPath, linkedAccountID, flagRole string) (string, error) {
	roleName, err := resolveLinkedRoleName(cmd, configPath, flagRole)
	if err != nil {
		return "", err
	}
	return awsrole.LinkedRoleARN(linkedAccountID, roleName)
}

// resolveTargetLinkedRoleARN returns the IAM role ARN for a linked-account scan target.
// Snapshot list and notify-owner share this so --role vs alias-role cannot drift.
// Priority: explicit --role, then the alias's stored role, then config / built-in default.
func resolveTargetLinkedRoleARN(cmd *cobra.Command, cfg configstore.File, configPath, accountID, displayAlias, flagRole string) (string, error) {
	if cmd.Flags().Changed("role") {
		return resolveLinkedRoleARN(cmd, configPath, accountID, flagRole)
	}
	if alias := strings.TrimSpace(displayAlias); alias != "" {
		if linked, ok := cfg.LinkedAccountForAlias(alias); ok {
			return cfg.LinkedRoleARNForAccount(linked.AccountID, linked.RoleName())
		}
	}
	return resolveLinkedRoleARN(cmd, configPath, accountID, flagRole)
}
