package releases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/version"
)

// SourceRepoEnv names the environment variable that overrides DefaultSourceRepo.
const SourceRepoEnv = "QCTL_SOURCE_REPO"

// DefaultSourceRepo is the git repository release tags are listed from.
const DefaultSourceRepo = "https://github.com/qlustered/qctl.git"

// DefaultTagTTL is how long a listing of a minor's tags is reused.
const DefaultTagTTL = time.Hour

// DefaultEmptyTagTTL is how long a listing that found no tags is reused.
const DefaultEmptyTagTTL = 5 * time.Minute

// SourceRepo returns $QCTL_SOURCE_REPO, or DefaultSourceRepo when it is unset
// or empty.
func SourceRepo() string {
	if repo := os.Getenv(SourceRepoEnv); repo != "" {
		return repo
	}
	return DefaultSourceRepo
}

// Outcome is how Resolve decided.
type Outcome string

const (
	// OutcomeSelected means Decision.Selected names the release to run.
	OutcomeSelected Outcome = "selected"
	// OutcomeNoRelease means no qctl release exists for the server's minor.
	OutcomeNoRelease Outcome = "no-release"
	// OutcomeUnknown means the server answered without a parseable version.
	OutcomeUnknown Outcome = "unknown"
	// OutcomeUnreachable means the server did not answer and no earlier
	// check against it selected a release.
	OutcomeUnreachable Outcome = "unreachable"
)

// TagSource says where the tags Decision.Selected was chosen from came from.
type TagSource string

const (
	// TagSourceRemote is a fresh listing of the source repo.
	TagSourceRemote TagSource = "remote"
	// TagSourceCache is a listing younger than the resolver's TTL.
	TagSourceCache TagSource = "cache"
	// TagSourceInstalled is the installed releases, used when listing failed.
	TagSourceInstalled TagSource = "installed"
	// TagSourceObservation is the release the last check against the server
	// selected, used when the server is unreachable.
	TagSourceObservation TagSource = "observation"
)

// Decision is the result of Resolve.
type Decision struct {
	Server  string
	Outcome Outcome
	// AtlasVersion is the version the server reported, or the last observed
	// one when TagSource is TagSourceObservation.
	AtlasVersion string
	// Minor is the MAJOR.MINOR key the release must match, empty when unknown.
	Minor string
	// Selected is the chosen release's tag, empty unless Outcome is
	// OutcomeSelected.
	Selected string
	// TagSource is set when Outcome is OutcomeSelected or OutcomeNoRelease.
	TagSource TagSource
	// IsRunning reports whether the running binary is the Selected release.
	IsRunning bool
	// FetchErr is the version request's error, set when the server did not
	// answer or answered without a parseable version.
	FetchErr error
	// ListErr is the tag listing's error, set when TagSource is
	// TagSourceInstalled.
	ListErr error
	// IndexErr is set when the observation could not be recorded; the rest
	// of the decision stands.
	IndexErr error
}

// Resolver chooses the release for a server. Build one with NewResolver; every
// field may be replaced before the first Resolve.
type Resolver struct {
	Layout Layout
	// Repo is the source repository passed to ListTags.
	Repo string
	// FetchVersion returns the server's reported version. An error wrapping
	// ErrServerUnreachable means the server did not answer.
	FetchVersion func(ctx context.Context, server string) (string, error)
	// ListTags returns the repo's release tags whose MinorKey is minor.
	ListTags func(ctx context.Context, repo, minor string) ([]Version, error)
	Now      func() time.Time
	// TagTTL is how long a stored listing is reused.
	TagTTL time.Duration
	// EmptyTagTTL is how long a stored listing that found no tags is reused.
	EmptyTagTTL time.Duration
	// Running is the running binary's embedded version.
	Running string
}

// NewResolver returns a resolver that uses layout, SourceRepo, HTTP, git and
// the system clock.
func NewResolver(layout Layout) *Resolver {
	return &Resolver{
		Layout:       layout,
		Repo:         SourceRepo(),
		FetchVersion: FetchServerVersion,
		ListTags:     ListRemoteTags,
		Now:          time.Now,
		TagTTL:       DefaultTagTTL,
		EmptyTagTTL:  DefaultEmptyTagTTL,
		Running:      version.Version,
	}
}

// Resolve chooses the qctl release for server and records what the server
// reported in the index. The error is non-nil only for an invalid server URL
// or an index that cannot be read.
func (r *Resolver) Resolve(ctx context.Context, server string) (Decision, error) {
	d := Decision{Server: server}
	key, err := config.NormalizeEndpointKey(server)
	if err != nil {
		return d, err
	}
	ix, err := r.Layout.ReadIndex()
	if err != nil {
		return d, err
	}

	reported, err := r.FetchVersion(ctx, server)
	if err != nil {
		d.FetchErr = err
		if !errors.Is(err, ErrServerUnreachable) {
			d.Outcome = OutcomeUnknown
			d.IndexErr = r.record(key, reported)
			return d, nil
		}
		obs := ix.Servers[key]
		if obs == nil || obs.QctlVersion == "" {
			d.Outcome = OutcomeUnreachable
			return d, nil
		}
		d.Outcome, d.TagSource = OutcomeSelected, TagSourceObservation
		d.AtlasVersion, d.Selected = obs.AtlasVersion, obs.QctlVersion
		if v, perr := ParseVersion(obs.QctlVersion); perr == nil {
			d.Minor = v.MinorKey()
		}
		d.IsRunning = d.Selected == r.Running
		return d, nil
	}

	d.AtlasVersion = reported
	atlas, err := ParseVersion(reported)
	if err != nil {
		d.Outcome, d.FetchErr = OutcomeUnknown, err
		d.IndexErr = r.record(key, reported)
		return d, nil
	}
	d.Minor = atlas.MinorKey()

	tags, cache := r.candidates(ctx, ix, &d)
	if best, ok := newest(tags, d.Minor); ok {
		d.Outcome, d.Selected = OutcomeSelected, best.String()
		d.IsRunning = d.Selected == r.Running
	} else {
		d.Outcome = OutcomeNoRelease
	}
	d.IndexErr = r.update(func(ix *Index) error {
		ix.Servers[key] = &Observation{AtlasVersion: reported, QctlVersion: d.Selected, ObservedAt: r.Now()}
		if cache != nil {
			ix.RemoteTags[d.Minor] = cache
		}
		return nil
	})
	return d, nil
}

// candidates returns the tags to choose from for d.Minor and sets
// d.TagSource. The returned cache is a fresh listing to store, or nil.
func (r *Resolver) candidates(ctx context.Context, ix *Index, d *Decision) ([]Version, *TagCache) {
	now := r.Now()
	if c := ix.RemoteTags[d.Minor]; c != nil && now.Sub(c.FetchedAt) >= 0 && now.Sub(c.FetchedAt) < r.tagTTL(c) {
		d.TagSource = TagSourceCache
		return parseTags(c.Tags), nil
	}
	listed, err := r.ListTags(ctx, r.Repo, d.Minor)
	if err == nil {
		d.TagSource = TagSourceRemote
		c := &TagCache{Tags: make([]string, 0, len(listed)), FetchedAt: now}
		for _, v := range listed {
			c.Tags = append(c.Tags, v.String())
		}
		return listed, c
	}
	d.TagSource, d.ListErr = TagSourceInstalled, err
	return r.installed(ix), nil
}

// tagTTL returns how long c is reused.
func (r *Resolver) tagTTL(c *TagCache) time.Duration {
	if len(c.Tags) == 0 {
		return r.EmptyTagTTL
	}
	return r.TagTTL
}

// installed returns the releases the index records for this platform whose
// binary is present.
func (r *Resolver) installed(ix *Index) []Version {
	var out []Version
	for tag, inst := range ix.Installed {
		v, err := ParseVersion(tag)
		if err != nil || inst == nil || inst.OS != runtime.GOOS || inst.Arch != runtime.GOARCH {
			continue
		}
		if fi, err := os.Stat(r.Layout.BinaryPath(tag)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, v)
	}
	return out
}

// record stores an observation of key that selected no release.
func (r *Resolver) record(key, atlasVersion string) error {
	return r.update(func(ix *Index) error {
		ix.Servers[key] = &Observation{AtlasVersion: atlasVersion, ObservedAt: r.Now()}
		return nil
	})
}

func (r *Resolver) update(fn func(*Index) error) error {
	if err := r.Layout.UpdateIndex(fn); err != nil {
		return fmt.Errorf("failed to record the release check: %w", err)
	}
	return nil
}

// parseTags returns the tags that parse, skipping the rest.
func parseTags(tags []string) []Version {
	out := make([]Version, 0, len(tags))
	for _, t := range tags {
		if v, err := ParseVersion(t); err == nil {
			out = append(out, v)
		}
	}
	return out
}
