package download

import (
	"path/filepath"
	"strings"
	"testing"
)

// With no --output the destination IS a name the server chose, so that name
// decides where the bytes land. Every case here is one the working directory
// must survive.
func TestServerFileNameCollapsesToOnePathElement(t *testing.T) {
	tests := []struct {
		name    string
		given   string
		want    string
		wantErr bool
	}{
		{name: "an ordinary name is untouched", given: "orders__bad__9f2c1ab3.csv.zip", want: "orders__bad__9f2c1ab3.csv.zip"},
		{name: "a dated name keeps its spaces", given: "orders - Quarantine rows (filtered) 2026-09-18.csv.zip", want: "orders - Quarantine rows (filtered) 2026-09-18.csv.zip"},
		{name: "a climbing name cannot climb", given: "../../etc/cron.d/qctl", want: "qctl"},
		{name: "an absolute name is not absolute here", given: "/etc/cron.d/qctl", want: "qctl"},
		{name: "a leading directory is dropped", given: "downloads/orders.csv.zip", want: "orders.csv.zip"},
		{name: "a bare climb is refused", given: "..", wantErr: true},
		{name: "a bare dot is refused", given: ".", wantErr: true},
		{name: "an empty name is refused", given: "", wantErr: true},
		{name: "a bare separator is refused", given: "/", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := serverFileName(tt.given)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("serverFileName(%q) = %q, want a refusal: it resolves to a directory, not a file", tt.given, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("serverFileName(%q): %v", tt.given, err)
			}
			if got != tt.want {
				t.Errorf("serverFileName(%q) = %q, want %q", tt.given, got, tt.want)
			}
		})
	}
}

// The split that matters: a name the SERVER chose is collapsed, a path the
// USER typed is not. Collapsing --output would refuse `--output
// ../archive/orders.zip`, which is a legitimate thing to ask for.
func TestMintDestinationCollapsesTheServersNameButNotOutput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got, err := mintDestination("", "../../../../tmp/evil.zip")
	if err != nil {
		t.Fatalf("mintDestination: %v", err)
	}
	if want := filepath.Join(dir, "evil.zip"); got != want {
		t.Errorf("the server's name escaped the working directory: got %q, want %q", got, want)
	}

	chosen := filepath.Join("..", "sibling", "orders.zip")
	got, err = mintDestination(chosen, "ignored.zip")
	if err != nil {
		t.Fatalf("mintDestination with --output: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("sibling", "orders.zip")) {
		t.Errorf("--output must be used as typed, got %q", got)
	}
}
