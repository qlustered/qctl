package diff

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

// exportedManifest is what `get table-rule -o yaml` writes for sampleDetail,
// status block included, with edit applied first.
func exportedManifest(t *testing.T, edit func(*dataset_rules.TableRuleManifest)) string {
	t.Helper()
	detail := sampleDetail()
	m := dataset_rules.APIResponseToManifest(&detail)
	if edit != nil {
		edit(m)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal exported manifest: %v", err)
	}
	if !strings.Contains(string(out), "\nstatus:") {
		t.Fatalf("control: the exported manifest has no status block\n%s", out)
	}
	return string(out)
}

// A rename made by editing the exported manifest is previewed: the
// metadata.instance_name the export writes is the name apply sends.
func TestDiffTableRule_ExportedManifestPreviewsTheRename(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerParamsRevisionDetails(mock)

	manifest := exportedManifest(t, func(m *dataset_rules.TableRuleManifest) { m.Metadata.InstanceName = "renamed" })
	stdout, stderr, err := execDiff(t, env.CreateFile("exported.yaml", manifest))
	if got := qerrors.GetExitCode(err); err == nil || got != 1 {
		t.Fatalf("a rename must be a difference (exit 1), got exit %d: %v\nstderr: %s", got, err, stderr)
	}
	diffLines, body, _ := strings.Cut(stdout, "\nmerge patch body:\n")
	if want := `instance_name    : "email_check" -> "renamed"`; strings.TrimSpace(diffLines) != want {
		t.Errorf("diff lines = %q, want only %q", diffLines, want)
	}
	if !strings.Contains(body, `"instance_name":"renamed"`) {
		t.Errorf("merge patch body must carry the rename\n---\n%s", stdout)
	}
}

// The unedited export restates the live rule, so it previews as no
// difference.
func TestDiffTableRule_UneditedExportedManifestHasNoDifferences(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerParamsRevisionDetails(mock)

	stdout, stderr, err := execDiff(t, env.CreateFile("exported.yaml", exportedManifest(t, nil)))
	if err != nil {
		t.Fatalf("an unedited export must be no difference (exit 0), got: %v\nstderr: %s", err, stderr)
	}
	if stdout != "" {
		t.Errorf("no differences must print nothing, got %q", stdout)
	}
}

// A restated name is dropped from the previewed body, as apply drops it from
// the request; a real change beside it is still previewed.
func TestDiffTableRule_RestatedNameLeavesTheBody(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerDetailGet(mock)
	registerParamsRevisionDetails(mock)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: email_check
  params:
    threshold: 0.95
`
	stdout, _, err := execDiff(t, env.CreateFile("restated.yaml", manifest))
	if got := qerrors.GetExitCode(err); err == nil || got != 1 {
		t.Fatalf("the params change must be a difference (exit 1), got exit %d: %v", got, err)
	}
	if !strings.Contains(stdout, "merge patch body:\n"+`{"params":{"threshold":0.95}}`) {
		t.Errorf("merge patch body must leave the restated name out\n---\n%s", stdout)
	}
}

// metadata.instance_name and spec.instance_name naming different rules is a
// manifest diff cannot read, refused with exit 2 before any request.
func TestDiffTableRule_ConflictingInstanceNamesExitTwo(t *testing.T) {
	env, mock := newDiffTestEnv(t)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
  instance_name: from_metadata
spec:
  dataset_id: 1
  instance_name: from_spec
`
	_, _, err := execDiff(t, env.CreateFile("conflict.yaml", manifest))
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d: %v", got, qerrors.ExitBadUsage, err)
	}
	for _, want := range []string{`"from_metadata"`, `"from_spec"`, "conflict.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err.Error(), want)
		}
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("a refused manifest sent requests: %v", reqs)
	}
}
