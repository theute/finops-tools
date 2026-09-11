package gmailauth

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
	googlehttp "google.golang.org/api/transport/http"
)

var gmailAPIScopes = []string{
	gmail.GmailSendScope,
	gmail.GmailMetadataScope,
}

// gcloud requires cloud-platform when passing --scopes to application-default login.
var gcloudADCLoginScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	gmail.GmailSendScope,
	gmail.GmailMetadataScope,
}

// ADCLoginHint returns the gcloud command to authorize Gmail send access.
func ADCLoginHint() string {
	return strings.Join(ADCLoginArgs(), " ")
}

// ADCLoginArgs returns argv for gcloud auth application-default login with Gmail scopes.
// --disable-quota-project avoids writing the gcloud config project into ADC; finops
// passes the quota project (hcmfinops) on each Gmail API call instead.
func ADCLoginArgs() []string {
	return []string{
		"gcloud", "auth", "application-default", "login",
		"--disable-quota-project",
		"--scopes=" + strings.Join(gcloudADCLoginScopes, ","),
	}
}

// TryADC reports whether gcloud Application Default Credentials can send Gmail.
func TryADC(ctx context.Context, quotaProject string) (email string, err error) {
	sender, err := newSenderFromADC(ctx, QuotaProject(quotaProject))
	if err != nil {
		return "", err
	}
	return sender.FromAddress(), nil
}

func newSenderFromADC(ctx context.Context, quotaProject string) (*Sender, error) {
	quotaProject = QuotaProject(quotaProject)
	creds, err := google.FindDefaultCredentials(ctx, gmailAPIScopes...)
	if err != nil {
		return nil, err
	}
	client, _, err := googlehttp.NewClient(ctx,
		option.WithTokenSource(creds.TokenSource),
		option.WithQuotaProject(quotaProject),
	)
	if err != nil {
		return nil, fmt.Errorf("create gmail http client: %w", err)
	}
	svc, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create gmail service: %w", err)
	}
	profile, err := svc.Users.GetProfile("me").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gmail profile: %w", err)
	}
	from := strings.TrimSpace(profile.EmailAddress)
	if from == "" {
		return nil, fmt.Errorf("gmail profile returned empty email address")
	}
	return &Sender{service: svc, from: from}, nil
}
