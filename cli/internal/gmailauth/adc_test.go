package gmailauth

import (
	"strings"
	"testing"
)

func TestADCLoginArgsIncludesCloudPlatformScope(t *testing.T) {
	args := ADCLoginArgs()
	if len(args) < 2 {
		t.Fatalf("ADCLoginArgs() = %v", args)
	}
	scopes := args[len(args)-1]
	if !strings.Contains(scopes, "https://www.googleapis.com/auth/cloud-platform") {
		t.Fatalf("ADCLoginArgs() scopes missing cloud-platform: %s", scopes)
	}
	if !strings.Contains(scopes, "gmail.send") || !strings.Contains(scopes, "gmail.metadata") {
		t.Fatalf("ADCLoginArgs() scopes missing gmail: %s", scopes)
	}
	if strings.Contains(scopes, "gmail.readonly") {
		t.Fatalf("ADCLoginArgs() requested gmail.readonly: %s", scopes)
	}
	foundDisableQuota := false
	for _, arg := range args {
		if arg == "--disable-quota-project" {
			foundDisableQuota = true
			break
		}
	}
	if !foundDisableQuota {
		t.Fatalf("ADCLoginArgs() missing --disable-quota-project: %v", args)
	}
}
