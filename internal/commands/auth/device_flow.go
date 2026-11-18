package auth

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/auth/oauth"
)

// promptAndPollForKindeTokens is the half of the Kinde device flow that login
// and switch-org share verbatim: show the verification URL and user code, try
// to open a browser, then poll until Kinde issues tokens, printing a dot per
// attempt.
//
// The halves they do NOT share stay with the callers, which is why this takes
// an already-requested deviceAuth rather than requesting one itself: login asks
// for no scopes and accepts a response without an id_token, while switch-org
// asks for openid/profile/email and requires the id_token, because the ops
// exchange checks its auth_time. Each also prints its own opening line. Folding
// any of that in here would change login's behavior.
func promptAndPollForKindeTokens(
	ctx context.Context,
	stdout, stderr io.Writer,
	kindeClient *oauth.KindeClient,
	deviceAuth *oauth.DeviceAuthResponse,
) (*oauth.KindeTokens, error) {
	verificationURL := deviceAuth.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = deviceAuth.VerificationURI
	}

	fmt.Fprintf(stdout, "\n%s\n", strings.Repeat("=", 60))
	fmt.Fprintf(stdout, "  To authenticate, open this URL in your browser:\n\n")
	fmt.Fprintf(stdout, "    %s\n", verificationURL)
	if deviceAuth.UserCode != "" {
		fmt.Fprintf(stdout, "\n  Verify this code matches: %s\n", deviceAuth.UserCode)
	}
	fmt.Fprintf(stdout, "%s\n\n", strings.Repeat("=", 60))

	if err := oauth.OpenBrowser(verificationURL); err != nil {
		fmt.Fprintf(stderr, "(Could not open browser automatically)\n")
	}

	fmt.Fprint(stdout, "Waiting for authentication")
	tokens, err := kindeClient.PollForToken(ctx, deviceAuth.DeviceCode, deviceAuth.Interval, deviceAuth.ExpiresIn, func() {
		fmt.Fprint(stdout, ".")
	})
	fmt.Fprintln(stdout) // newline after the dots

	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}
	return tokens, nil
}
