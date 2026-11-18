package releases

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qclient "github.com/qlustered/qctl/internal/client"
)

// ErrServerUnreachable marks a version request the server did not answer.
var ErrServerUnreachable = errors.New("server unreachable")

// VersionTimeout is FetchServerVersion's deadline when --request-timeout is
// not in effect.
const VersionTimeout = 5 * time.Second

// ListTimeout bounds one ListRemoteTags call.
const ListTimeout = 15 * time.Second

// FetchServerVersion returns the version server reports at /api/version. A
// transport failure, a 429 or a 5xx wraps ErrServerUnreachable.
func FetchServerVersion(ctx context.Context, server string) (string, error) {
	c, err := api.NewClientWithResponses(server, api.WithHTTPClient(qclient.NewDoer(VersionTimeout, nil)))
	if err != nil {
		return "", fmt.Errorf("failed to create API client: %w", err)
	}
	resp, err := c.GetVersionWithResponse(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: version request failed: %w", ErrServerUnreachable, err)
	}
	status := resp.StatusCode()
	switch {
	case status == http.StatusTooManyRequests || status >= 500:
		return "", fmt.Errorf("%w: version request returned status %d", ErrServerUnreachable, status)
	case status != http.StatusOK:
		return "", fmt.Errorf("version request returned status %d", status)
	case resp.JSON200 == nil:
		return "", errors.New("version response is empty")
	}
	return resp.JSON200.Version, nil
}

// ListRemoteTags runs git ls-remote against repo and returns its release tags
// whose MinorKey is minor, oldest first. Git's terminal credential prompt is disabled.
func ListRemoteTags(ctx context.Context, repo, minor string) ([]Version, error) {
	if _, err := ParseVersion(minor + ".0"); err != nil {
		return nil, fmt.Errorf("invalid minor %q", minor)
	}
	ctx, cancel := context.WithTimeout(ctx, ListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--tags", "--refs", "--", repo, "refs/tags/"+minor+".*")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git ls-remote: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("git ls-remote: %w", err)
	}
	var tags []Version
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		_, ref, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}
		v, err := ParseVersion(strings.TrimPrefix(ref, "refs/tags/"))
		if err != nil || !strings.HasPrefix(ref, "refs/tags/") || v.MinorKey() != minor {
			continue
		}
		tags = append(tags, v)
	}
	slices.SortFunc(tags, Version.Compare)
	return tags, sc.Err()
}
