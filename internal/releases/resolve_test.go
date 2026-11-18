package releases

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/config"
)

var t0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// versionServer answers /api/version with status and body.
func versionServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// deadServer returns the URL of a server that is no longer listening.
func deadServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

type fakeLister struct {
	tags  []string
	err   error
	calls int
}

func (f *fakeLister) list(_ context.Context, repo, minor string) ([]Version, error) {
	f.calls++
	if repo != "repo" {
		return nil, errors.New("wrong repo " + repo)
	}
	if f.err != nil {
		return nil, f.err
	}
	var out []Version
	for _, v := range parseTags(f.tags) {
		if v.MinorKey() == minor {
			out = append(out, v)
		}
	}
	return out, nil
}

func newTestResolver(t *testing.T, l *fakeLister) (*Resolver, *time.Time) {
	t.Helper()
	now := t0
	r := NewResolver(testLayout(t))
	r.Repo = "repo"
	r.ListTags = l.list
	r.Now = func() time.Time { return now }
	r.Running = "dev"
	return r, &now
}

func observation(t *testing.T, r *Resolver, server string) *Observation {
	t.Helper()
	key, err := config.NormalizeEndpointKey(server)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := r.Layout.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	return ix.Servers[key]
}

// install records tag as installed for this platform, with a binary present
// when withBinary is set.
func install(t *testing.T, l Layout, tag, goos string, withBinary bool) {
	t.Helper()
	if withBinary {
		if err := os.MkdirAll(l.VersionDir(tag), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(l.BinaryPath(tag), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	err := l.UpdateIndex(func(ix *Index) error {
		ix.Installed[tag] = &Installed{OS: goos, Arch: runtime.GOARCH, Commit: "c", InstalledAt: t0}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveOutcomes(t *testing.T) {
	ctx := context.Background()

	t.Run("newest patch in the server's minor", func(t *testing.T) {
		lister := &fakeLister{tags: []string{"2.134.9", "2.135.0", "2.135.2", "2.135.1", "2.136.0"}}
		r, _ := newTestResolver(t, lister)
		server := versionServer(t, 200, `{"version":"2.135.7"}`)
		d, err := r.Resolve(ctx, server)
		if err != nil {
			t.Fatal(err)
		}
		if d.Outcome != OutcomeSelected || d.Selected != "2.135.2" || d.Minor != "2.135" ||
			d.AtlasVersion != "2.135.7" || d.TagSource != TagSourceRemote || d.IsRunning || d.IndexErr != nil {
			t.Fatalf("decision %+v", d)
		}
		obs := observation(t, r, server)
		if obs == nil || obs.AtlasVersion != "2.135.7" || obs.QctlVersion != "2.135.2" || !obs.ObservedAt.Equal(t0) {
			t.Fatalf("observation %+v", obs)
		}
	})

	t.Run("running binary is the selection", func(t *testing.T) {
		r, _ := newTestResolver(t, &fakeLister{tags: []string{"2.135.0"}})
		r.Running = "2.135.0"
		d, err := r.Resolve(ctx, versionServer(t, 200, `{"version":"2.135.0"}`))
		if err != nil || d.Selected != "2.135.0" || !d.IsRunning {
			t.Fatalf("decision %+v, %v", d, err)
		}
	})

	t.Run("no release for the minor", func(t *testing.T) {
		r, _ := newTestResolver(t, &fakeLister{tags: []string{"2.134.0", "2.136.0"}})
		server := versionServer(t, 200, `{"version":"2.135.0"}`)
		d, err := r.Resolve(ctx, server)
		if err != nil || d.Outcome != OutcomeNoRelease || d.Selected != "" || d.Minor != "2.135" {
			t.Fatalf("decision %+v, %v", d, err)
		}
		if obs := observation(t, r, server); obs == nil || obs.AtlasVersion != "2.135.0" || obs.QctlVersion != "" {
			t.Fatalf("observation %+v", obs)
		}
	})

	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"unparseable version", `{"version":"2.135.0-local"}`, 200},
		{"empty version", `{"version":""}`, 200},
		{"no version endpoint", `{}`, 404},
	} {
		t.Run("unknown: "+tt.name, func(t *testing.T) {
			lister := &fakeLister{tags: []string{"2.135.0"}}
			r, _ := newTestResolver(t, lister)
			server := versionServer(t, tt.status, tt.body)
			d, err := r.Resolve(ctx, server)
			if err != nil || d.Outcome != OutcomeUnknown || d.Selected != "" || d.FetchErr == nil || lister.calls != 0 {
				t.Fatalf("decision %+v, %v, lister calls %d", d, err, lister.calls)
			}
			if obs := observation(t, r, server); obs == nil || obs.QctlVersion != "" {
				t.Fatalf("observation %+v", obs)
			}
		})
	}

	for _, tt := range []struct {
		name   string
		server func(t *testing.T) string
	}{
		{"connection refused", deadServer},
		{"503", func(t *testing.T) string { return versionServer(t, 503, `{}`) }},
	} {
		t.Run("unreachable without an observation: "+tt.name, func(t *testing.T) {
			r, _ := newTestResolver(t, &fakeLister{tags: []string{"2.135.0"}})
			d, err := r.Resolve(ctx, tt.server(t))
			if err != nil || d.Outcome != OutcomeUnreachable || !errors.Is(d.FetchErr, ErrServerUnreachable) {
				t.Fatalf("decision %+v, %v", d, err)
			}
		})
	}

	t.Run("unreachable uses the last observation", func(t *testing.T) {
		lister := &fakeLister{}
		r, _ := newTestResolver(t, lister)
		server := deadServer(t)
		key, _ := config.NormalizeEndpointKey(server)
		_ = r.Layout.UpdateIndex(func(ix *Index) error {
			ix.Servers[key] = &Observation{AtlasVersion: "2.135.4", QctlVersion: "2.135.1", ObservedAt: t0}
			return nil
		})
		r.Running = "2.135.1"
		d, err := r.Resolve(ctx, server)
		if err != nil || d.Outcome != OutcomeSelected || d.Selected != "2.135.1" || d.TagSource != TagSourceObservation ||
			d.AtlasVersion != "2.135.4" || d.Minor != "2.135" || !d.IsRunning || lister.calls != 0 {
			t.Fatalf("decision %+v, %v", d, err)
		}
	})

	t.Run("an observation without a release is not reused", func(t *testing.T) {
		r, _ := newTestResolver(t, &fakeLister{})
		server := deadServer(t)
		key, _ := config.NormalizeEndpointKey(server)
		_ = r.Layout.UpdateIndex(func(ix *Index) error {
			ix.Servers[key] = &Observation{AtlasVersion: "2.135.4", ObservedAt: t0}
			return nil
		})
		if d, err := r.Resolve(ctx, server); err != nil || d.Outcome != OutcomeUnreachable {
			t.Fatalf("decision %+v, %v", d, err)
		}
	})

	t.Run("listing failure falls back to installed releases", func(t *testing.T) {
		lister := &fakeLister{err: errors.New("offline")}
		r, _ := newTestResolver(t, lister)
		install(t, r.Layout, "2.135.1", runtime.GOOS, true)
		install(t, r.Layout, "2.135.5", runtime.GOOS, false)    // binary missing
		install(t, r.Layout, "2.135.6", "plan9-not-this", true) // other platform
		install(t, r.Layout, "2.136.0", runtime.GOOS, true)     // other minor
		d, err := r.Resolve(ctx, versionServer(t, 200, `{"version":"2.135.0"}`))
		if err != nil || d.Outcome != OutcomeSelected || d.Selected != "2.135.1" || d.TagSource != TagSourceInstalled || d.ListErr == nil {
			t.Fatalf("decision %+v, %v", d, err)
		}
	})

	t.Run("listing failure with nothing installed", func(t *testing.T) {
		r, _ := newTestResolver(t, &fakeLister{err: errors.New("offline")})
		d, err := r.Resolve(ctx, versionServer(t, 200, `{"version":"2.135.0"}`))
		if err != nil || d.Outcome != OutcomeNoRelease || d.TagSource != TagSourceInstalled || d.ListErr == nil {
			t.Fatalf("decision %+v, %v", d, err)
		}
	})

	t.Run("invalid server URL", func(t *testing.T) {
		r, _ := newTestResolver(t, &fakeLister{})
		if _, err := r.Resolve(ctx, "http://bad host"); err == nil {
			t.Fatal("no error for an invalid server URL")
		}
	})

	t.Run("corrupt index", func(t *testing.T) {
		r, _ := newTestResolver(t, &fakeLister{})
		_ = os.MkdirAll(r.Layout.Root, 0o700)
		_ = os.WriteFile(r.Layout.IndexPath(), []byte("nope"), 0o600)
		if _, err := r.Resolve(ctx, versionServer(t, 200, `{"version":"2.135.0"}`)); err == nil {
			t.Fatal("no error for a corrupt index")
		}
	})

	t.Run("unwritable index keeps the decision", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		r, _ := newTestResolver(t, &fakeLister{tags: []string{"2.135.0"}})
		r.Layout.Root = filepath.Join(t.TempDir(), "ro", "versions")
		if err := os.MkdirAll(filepath.Dir(r.Layout.Root), 0o500); err != nil {
			t.Fatal(err)
		}
		d, err := r.Resolve(ctx, versionServer(t, 200, `{"version":"2.135.0"}`))
		if err != nil || d.Selected != "2.135.0" || d.IndexErr == nil {
			t.Fatalf("decision %+v, %v", d, err)
		}
	})
}

func TestResolveTagCacheTTL(t *testing.T) {
	ctx := context.Background()
	lister := &fakeLister{tags: []string{"2.135.0"}}
	r, now := newTestResolver(t, lister)
	server := versionServer(t, 200, `{"version":"2.135.0"}`)

	resolve := func() Decision {
		t.Helper()
		d, err := r.Resolve(ctx, server)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	if d := resolve(); d.TagSource != TagSourceRemote || lister.calls != 1 {
		t.Fatalf("first: %+v, calls %d", d, lister.calls)
	}

	// A tag published within the TTL is not seen yet.
	lister.tags = append(lister.tags, "2.135.1")
	*now = t0.Add(DefaultTagTTL - time.Second)
	if d := resolve(); d.TagSource != TagSourceCache || d.Selected != "2.135.0" || lister.calls != 1 {
		t.Fatalf("within TTL: %+v, calls %d", d, lister.calls)
	}

	*now = t0.Add(DefaultTagTTL)
	if d := resolve(); d.TagSource != TagSourceRemote || d.Selected != "2.135.1" || lister.calls != 2 {
		t.Fatalf("after TTL: %+v, calls %d", d, lister.calls)
	}

	// A listing stamped in the future (clock moved back) is not trusted.
	*now = t0
	if d := resolve(); d.TagSource != TagSourceRemote || lister.calls != 3 {
		t.Fatalf("clock moved back: %+v, calls %d", d, lister.calls)
	}

	ix, _ := r.Layout.ReadIndex()
	if c := ix.RemoteTags["2.135"]; c == nil || len(c.Tags) != 2 || !c.FetchedAt.Equal(t0) {
		t.Fatalf("stored cache %+v", c)
	}
}

func TestResolveEmptyTagListingExpiresSooner(t *testing.T) {
	ctx := context.Background()
	lister := &fakeLister{}
	r, now := newTestResolver(t, lister)
	server := versionServer(t, 200, `{"version":"2.136.0"}`)

	resolve := func() Decision {
		t.Helper()
		d, err := r.Resolve(ctx, server)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	if d := resolve(); d.Outcome != OutcomeNoRelease || lister.calls != 1 {
		t.Fatalf("first: %+v, calls %d", d, lister.calls)
	}

	// The tag is pushed; an empty listing is still reused within its TTL.
	lister.tags = []string{"2.136.0"}
	*now = t0.Add(DefaultEmptyTagTTL - time.Second)
	if d := resolve(); d.Outcome != OutcomeNoRelease || d.TagSource != TagSourceCache || lister.calls != 1 {
		t.Fatalf("within empty TTL: %+v, calls %d", d, lister.calls)
	}

	*now = t0.Add(DefaultEmptyTagTTL)
	if d := resolve(); d.Outcome != OutcomeSelected || d.Selected != "2.136.0" || lister.calls != 2 {
		t.Fatalf("after empty TTL: %+v, calls %d", d, lister.calls)
	}
}
