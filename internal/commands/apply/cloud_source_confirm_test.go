package apply

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// csConfirmTableName is the live table's name, distinct from the source's.
const csConfirmTableName = "sales_ledger"

// csConfirmOrgName is the cached name of testOrgID.
const csConfirmOrgName = "Acme Corp"

// csConfirmManifestHead is a CloudSource manifest for s3-orders in table 11 up
// to the fields a test adds under spec.
const csConfirmManifestHead = `apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
  data_source_type: s3
  s3_region_name: us-east-1
`

// csConfirmConflictBody is atlas's 409 for a stale version_id.
const csConfirmConflictBody = `{"detail":{"msg":"DataSourceModel version mismatch. DataSourceModel was updated separately and needs to be reloaded before being modified by the user.","error_id":""}}`

// csConfirmFixture is a mock API holding one live cloud source, s3-orders
// (id 42) in table 11, that records every PATCH sent to it and what the
// command had written by then.
type csConfirmFixture struct {
	env  *testutil.TestEnv
	mock *testutil.MockAPIServer

	stdout bytes.Buffer
	stderr bytes.Buffer

	patches       []map[string]json.RawMessage
	stdoutAtPatch string
	stderrAtPatch string
}

func newCsConfirmFixture(t *testing.T, live *cloud_sources.CloudSourceFull, patchStatus int, patchReply string) *csConfirmFixture {
	t.Helper()
	f := &csConfirmFixture{env: testutil.NewTestEnv(t), mock: testutil.NewMockAPIServer()}
	t.Cleanup(f.env.Cleanup)
	t.Cleanup(f.mock.Close)

	org := "/api/orgs/" + testOrgID
	f.mock.RegisterHandler("GET", org+"/datasets/11", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID: 11, Name: csConfirmTableName, SettingsModel: api.SettingsSchema{ID: 2},
		})
	})
	f.mock.RegisterHandler("GET", org+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
			Results: []cloud_sources.CloudSourceTiny{{
				ID: 42, Name: "s3-orders", DataSourceType: api.DataSourceType("s3"),
				DatasetID: 11, DatasetName: csConfirmTableName, State: api.DataSourceState("active"),
			}},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})
	f.mock.RegisterHandler("GET", org+"/data-sources/42", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, live)
	})
	// The mock transport runs the handler on the command's own goroutine, so
	// the snapshots are exactly what the command wrote before it sent this.
	f.mock.RegisterHandler("PATCH", org+"/data-sources/42", func(w http.ResponseWriter, r *http.Request) {
		f.stdoutAtPatch = f.stdout.String()
		f.stderrAtPatch = f.stderr.String()
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode PATCH body: %v", err)
		}
		f.patches = append(f.patches, body)
		if patchStatus == http.StatusOK {
			testutil.RespondJSON(w, http.StatusOK, live)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(patchStatus)
		_, _ = w.Write([]byte(patchReply))
	})

	cfg := f.env.SetupConfigWithOrg(f.mock.URL(), "test@example.com", testOrgID)
	cfg.Contexts[cfg.CurrentContext].Organizations = []config.OrganizationRef{{ID: testOrgID, Name: csConfirmOrgName}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	endpointKey, _ := config.NormalizeEndpointKey(f.mock.URL())
	f.env.SetupCredential(endpointKey, testOrgID, "token")
	return f
}

// apply runs `apply cloud-source -f <manifest> args...` and returns the
// manifest's path and the command's error.
func (f *csConfirmFixture) apply(manifest string, args ...string) (string, error) {
	path := f.env.CreateFile("cs.yaml", manifest)
	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetOut(&f.stdout)
	root.SetErr(&f.stderr)
	root.SetArgs(append([]string{"apply", "cloud-source", "-f", path}, args...))
	return path, root.Execute()
}

// patchCount is the number of PATCH requests the mock saw.
func (f *csConfirmFixture) patchCount() int {
	n := 0
	for _, request := range f.mock.Requests() {
		if strings.HasPrefix(request, "PATCH ") {
			n++
		}
	}
	return n
}

// csConfirmLive is sampleCloudSourceFull in table csConfirmTableName, with
// edit applied.
func csConfirmLive(edit func(*cloud_sources.CloudSourceFull)) *cloud_sources.CloudSourceFull {
	live := sampleCloudSourceFull()
	live.DatasetName = csConfirmPtr(csConfirmTableName)
	if edit != nil {
		edit(live)
	}
	return live
}

func csConfirmPtr[T any](v T) *T { return &v }

// csConfirmLine returns the first line of out that starts, after indentation,
// with prefix.
func csConfirmLine(out, prefix string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return line, true
		}
	}
	return "", false
}

var csConfirmTokenSplit = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// csConfirmTokens returns the identifier-shaped words of s.
func csConfirmTokens(s string) map[string]bool {
	tokens := map[string]bool{}
	for _, token := range csConfirmTokenSplit.Split(s, -1) {
		if token != "" {
			tokens[token] = true
		}
	}
	return tokens
}

// csConfirmChanges parses the lines under "Changes to apply:", up to the first
// blank line, into path → verb and path → line.
func csConfirmChanges(t *testing.T, out string) (verbs, lines map[string]string) {
	t.Helper()
	all := strings.Split(out, "\n")
	start := -1
	for i, line := range all {
		if strings.TrimSpace(line) == "Changes to apply:" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("no \"Changes to apply:\" block in:\n%s", out)
	}
	verbs, lines = map[string]string{}, map[string]string{}
	for _, line := range all[start:] {
		if strings.TrimSpace(line) == "" {
			break
		}
		var verb, path string
		for _, field := range strings.Fields(line) {
			switch {
			case verb == "" && (field == "SET" || field == "REPLACE" || field == "CLEAR"):
				verb = field
			case path == "" && strings.HasPrefix(field, "spec."):
				path = strings.TrimSuffix(field, ":")
			}
		}
		if verb == "" || path == "" {
			t.Errorf("change line without a verb and a spec path: %q", line)
			continue
		}
		verbs[path] = verb
		lines[path] = line
	}
	return verbs, lines
}

func csConfirmSortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// The preview names where the update lands, then lists every change with its
// verb and manifest path, and the PATCH that follows a yes carries exactly
// those fields.
func TestCsConfirmPreviewListsEveryChangeThenPatchesOnce(t *testing.T) {
	f := newCsConfirmFixture(t, csConfirmLive(func(live *cloud_sources.CloudSourceFull) {
		live.CsvDelimiter = csConfirmPtr(";")
		live.NullValuesOverride = &[]string{"NA"}
		live.ValueMappingOverride = &map[string]string{"NA": "n/a"}
	}), http.StatusOK, "")
	confirmYes(t)

	path, err := f.apply(csConfirmManifestHead + `  s3_bucket: new-bucket
  s3_prefix: incoming/
  settings_overrides:
    null_values: ["N/A"]
    infer_datetime: false
`)
	if err != nil {
		t.Fatalf("confirmed apply: %v", err)
	}
	out := f.stdout.String()

	for _, want := range []struct {
		prefix string
		texts  []string
		tokens []string
	}{
		{"Organization", []string{csConfirmOrgName, testOrgID}, nil},
		{"Cloud source", []string{"s3-orders"}, []string{"42"}},
		{"Table", []string{csConfirmTableName}, []string{"11"}},
		{"File", []string{path}, nil},
	} {
		line, ok := csConfirmLine(out, want.prefix)
		if !ok {
			t.Errorf("preview has no %s line:\n%s", want.prefix, out)
			continue
		}
		for _, text := range want.texts {
			if !strings.Contains(line, text) {
				t.Errorf("%s line %q does not name %q", want.prefix, line, text)
			}
		}
		tokens := csConfirmTokens(line)
		for _, token := range want.tokens {
			if !tokens[token] {
				t.Errorf("%s line %q does not carry the id %s", want.prefix, line, token)
			}
		}
	}

	verbs, lines := csConfirmChanges(t, out)
	wantVerbs := map[string]string{
		"spec.s3_bucket":                         "REPLACE",
		"spec.s3_prefix":                         "SET",
		"spec.csv_delimiter":                     "CLEAR",
		"spec.settings_overrides.null_values":    "REPLACE",
		"spec.settings_overrides.infer_datetime": "SET",
		"spec.settings_overrides.value_mapping":  "CLEAR",
	}
	for path, verb := range wantVerbs {
		if verbs[path] != verb {
			t.Errorf("%s: verb %q, want %q", path, verbs[path], verb)
		}
	}
	for path := range verbs {
		if _, ok := wantVerbs[path]; !ok {
			t.Errorf("preview lists a change the manifest does not make: %q", lines[path])
		}
	}
	if line := lines["spec.s3_bucket"]; !strings.Contains(line, "raw-orders") || !strings.Contains(line, "new-bucket") {
		t.Errorf("a REPLACE shows neither side's value: %q", line)
	}

	if n := f.patchCount(); n != 1 {
		t.Fatalf("PATCH requests = %d, want 1: %v", n, f.mock.Requests())
	}
	got := strings.Join(csConfirmSortedKeys(f.patches[0]), ",")
	want := "csv_delimiter,id,infer_datetime_override,null_values_override,s3_bucket,s3_prefix,value_mapping_override,version_id"
	if got != want {
		t.Errorf("PATCH keys = %s, want the previewed changes plus id and version_id: %s", got, want)
	}
	if !strings.Contains(out, "cloud-source/s3-orders updated") {
		t.Errorf("no updated line:\n%s", out)
	}
}

// A secret supplied over the stored mask is a change, shown without either
// value, and sent.
func TestCsConfirmNeverPrintsASecret(t *testing.T) {
	const mask = "**********"
	f := newCsConfirmFixture(t, csConfirmLive(func(live *cloud_sources.CloudSourceFull) {
		live.S3SecretKey = csConfirmPtr(mask)
	}), http.StatusOK, "")
	confirmYes(t)

	if _, err := f.apply(csConfirmManifestHead + `  s3_bucket: raw-orders
  s3_secret_key: supersecret
`); err != nil {
		t.Fatalf("confirmed apply: %v", err)
	}

	verbs, lines := csConfirmChanges(t, f.stdout.String())
	if len(verbs) != 1 || verbs["spec.s3_secret_key"] == "" {
		t.Fatalf("changes = %v, want spec.s3_secret_key alone", verbs)
	}
	if line := lines["spec.s3_secret_key"]; !strings.Contains(line, "not shown") {
		t.Errorf("the secret's change line does not say its value is not shown: %q", line)
	}
	for stream, text := range map[string]string{"stdout": f.stdout.String(), "stderr": f.stderr.String()} {
		for _, leaked := range []string{"supersecret", mask} {
			if strings.Contains(text, leaked) {
				t.Errorf("%s carries %q:\n%s", stream, leaked, text)
			}
		}
	}

	if len(f.patches) != 1 {
		t.Fatalf("PATCH requests = %d, want 1", len(f.patches))
	}
	if got := string(f.patches[0]["s3_secret_key"]); got != `"supersecret"` {
		t.Errorf("PATCH s3_secret_key = %s, want the manifest's value", got)
	}
}

// Declining is a skip, not a failure, and nothing is sent.
func TestCsConfirmDeclineSkipsWithoutPatching(t *testing.T) {
	f := newCsConfirmFixture(t, csConfirmLive(nil), http.StatusOK, "")
	confirmNo(t)

	path, err := f.apply(csConfirmManifestHead + "  s3_bucket: new-bucket\n")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitSuccess {
		t.Fatalf("declined apply exit code = %d (%v), want %d", code, err, qerrors.ExitSuccess)
	}
	if !strings.Contains(f.stdout.String(), "Skipping "+path) {
		t.Errorf("no Skipping line for %s:\n%s", path, f.stdout.String())
	}
	if n := f.patchCount(); n != 0 {
		t.Errorf("declined apply sent %d PATCH requests: %v", n, f.mock.Requests())
	}
}

// status.version_id older than the live version, on the same source, makes
// the preview name both versions ahead of the change list.
func TestCsConfirmStaleExportWarning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		stale  bool
	}{
		{"older version", "  id: 42\n  version_id: 1188\n", true},
		{"same version", "  id: 42\n  version_id: 1207\n", false},
		{"another source", "  id: 999\n  version_id: 1188\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCsConfirmFixture(t, csConfirmLive(func(live *cloud_sources.CloudSourceFull) {
				live.VersionID = csConfirmPtr(1207)
			}), http.StatusOK, "")
			confirmYes(t)

			if _, err := f.apply(csConfirmManifestHead + "  s3_bucket: new-bucket\nstatus:\n" + tc.status); err != nil {
				t.Fatalf("confirmed apply: %v", err)
			}
			out := f.stdout.String()
			changesAt := strings.Index(out, "Changes to apply:")
			if changesAt < 0 {
				t.Fatalf("no change list:\n%s", out)
			}
			warning := -1
			for _, line := range strings.Split(out, "\n") {
				tokens := csConfirmTokens(line)
				if tokens["1188"] && tokens["1207"] {
					warning = strings.Index(out, line)
					break
				}
			}
			if !tc.stale {
				// The warning is the only thing that would print either version.
				// Whole tokens only: the File line's temp path can contain the digits.
				for _, line := range strings.Split(out, "\n") {
					if tokens := csConfirmTokens(line); tokens["1188"] || tokens["1207"] {
						t.Errorf("preview names a version for a manifest that is not stale: %q", line)
					}
				}
				return
			}
			if warning < 0 {
				t.Fatalf("preview has no line naming versions 1188 and 1207:\n%s", out)
			}
			if warning > changesAt {
				t.Errorf("the stale-export warning follows the change list:\n%s", out)
			}
		})
	}
}

// Under --yes the stale-export warning reaches stderr before the request, and
// -o json keeps stdout parseable.
func TestCsConfirmStaleExportUnderYesGoesToStderr(t *testing.T) {
	f := newCsConfirmFixture(t, csConfirmLive(func(live *cloud_sources.CloudSourceFull) {
		live.VersionID = csConfirmPtr(1207)
	}), http.StatusOK, "")

	if _, err := f.apply(csConfirmManifestHead+"  s3_bucket: new-bucket\nstatus:\n  id: 42\n  version_id: 1188\n",
		"--yes", "-o", "json"); err != nil {
		t.Fatalf("apply --yes: %v", err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("PATCH requests = %d, want 1", len(f.patches))
	}
	named := false
	for _, line := range strings.Split(f.stderrAtPatch, "\n") {
		tokens := csConfirmTokens(line)
		named = named || (tokens["1188"] && tokens["1207"])
	}
	if !named {
		t.Errorf("stderr before the PATCH has no line naming versions 1188 and 1207: %q", f.stderrAtPatch)
	}
	var result map[string]any
	if err := json.Unmarshal(f.stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, f.stdout.String())
	}
	if result["action"] != "updated" {
		t.Errorf("result action = %v, want updated", result["action"])
	}
}

// A boundary override change carries the LEARN note and a CSV dialect change
// the cant_sniff_header note: in the preview interactively, and under --yes on
// stderr only once the PATCH has succeeded.
func TestCsConfirmSideEffectNotes(t *testing.T) {
	const learn, sniff = "learn", "cant_sniff_header"
	for _, change := range []struct {
		name     string
		spec     string
		has, not string
	}{
		{"boundary override", "  settings_overrides:\n    ignore_column_names: [\"tmp\"]\n", learn, sniff},
		{"ignore_column_ids", "  ignore_column_ids: [7]\n", learn, sniff},
		{"csv_delimiter", "  csv_delimiter: \"|\"\n", sniff, learn},
	} {
		manifest := csConfirmManifestHead + "  s3_bucket: raw-orders\n" + change.spec
		mentions := func(text, note string) bool { return strings.Contains(strings.ToLower(text), note) }

		t.Run(change.name+"/interactive", func(t *testing.T) {
			f := newCsConfirmFixture(t, csConfirmLive(nil), http.StatusOK, "")
			confirmYes(t)
			if _, err := f.apply(manifest); err != nil {
				t.Fatalf("confirmed apply: %v", err)
			}
			if len(f.patches) != 1 {
				t.Fatalf("PATCH requests = %d, want 1", len(f.patches))
			}
			if !mentions(f.stdoutAtPatch, change.has) {
				t.Errorf("preview has no %q note:\n%s", change.has, f.stdoutAtPatch)
			}
			if change.has == learn {
				assertCsLearnConditions(t, f.stdoutAtPatch)
			}
			if mentions(f.stdout.String(), change.not) {
				t.Errorf("preview has a %q note for a change that does not cause it:\n%s", change.not, f.stdout.String())
			}
		})

		t.Run(change.name+"/yes", func(t *testing.T) {
			f := newCsConfirmFixture(t, csConfirmLive(nil), http.StatusOK, "")
			if _, err := f.apply(manifest, "--yes"); err != nil {
				t.Fatalf("apply --yes: %v", err)
			}
			if len(f.patches) != 1 {
				t.Fatalf("PATCH requests = %d, want 1", len(f.patches))
			}
			if mentions(f.stderrAtPatch, change.has) {
				t.Errorf("the %q note reached stderr before the PATCH: %q", change.has, f.stderrAtPatch)
			}
			if !mentions(f.stderr.String(), change.has) {
				t.Errorf("stderr has no %q note after a successful PATCH: %q", change.has, f.stderr.String())
			}
			if change.has == learn {
				assertCsLearnConditions(t, f.stderr.String())
			}
			if mentions(f.stderr.String(), change.not) {
				t.Errorf("stderr has a %q note for a change that does not cause it: %q", change.not, f.stderr.String())
			}
			if mentions(f.stdout.String(), change.has) {
				t.Errorf("the %q note reached stdout: %q", change.has, f.stdout.String())
			}
		})

		t.Run(change.name+"/yes conflict", func(t *testing.T) {
			f := newCsConfirmFixture(t, csConfirmLive(nil), http.StatusConflict, csConfirmConflictBody)
			if _, err := f.apply(manifest, "--yes"); err == nil {
				t.Fatal("a 409 PATCH succeeded")
			}
			if len(f.patches) != 1 {
				t.Fatalf("PATCH requests = %d, want 1", len(f.patches))
			}
			if mentions(f.stderr.String(), change.has) {
				t.Errorf("stderr reports the %q side effect of a PATCH the server refused: %q", change.has, f.stderr.String())
			}
		})
	}
}

func assertCsLearnConditions(t *testing.T, out string) {
	t.Helper()
	for _, condition := range []string{
		"may queue", "caller is not external", "a file of this source is enabled for training",
		"table has a draft migration or has files but no migration yet",
	} {
		if !strings.Contains(out, condition) {
			t.Errorf("learning note omits %q:\n%s", condition, out)
		}
	}
}

func TestCsConfirmCsvDelimiterRecoveryNotes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		live     *string
		spec     string
		wantNote bool
	}{
		{"clear a value", csConfirmPtr(";"), "null", true},
		{"clear an empty string", csConfirmPtr(""), "null", false},
		{"set an empty string over null", nil, `""`, false},
	} {
		for _, yes := range []bool{false, true} {
			t.Run(tc.name+"/yes="+fmt.Sprint(yes), func(t *testing.T) {
				f := newCsConfirmFixture(t, csConfirmLive(func(live *cloud_sources.CloudSourceFull) {
					live.CsvDelimiter = tc.live
				}), http.StatusOK, "")
				var args []string
				if yes {
					args = []string{"--yes"}
				} else {
					confirmYes(t)
				}
				manifest := csConfirmManifestHead + "  s3_bucket: raw-orders\n  csv_delimiter: " + tc.spec + "\n"
				if _, err := f.apply(manifest, args...); err != nil {
					t.Fatalf("apply: %v", err)
				}
				if len(f.patches) != 1 {
					t.Fatalf("PATCH requests = %d, want 1", len(f.patches))
				}
				if got := string(f.patches[0]["csv_delimiter"]); got != tc.spec {
					t.Errorf("PATCH csv_delimiter = %s, want %s", got, tc.spec)
				}
				notes := f.stdoutAtPatch
				if yes {
					notes = f.stderr.String()
				}
				if got := strings.Contains(notes, "cant_sniff_header"); got != tc.wantNote {
					t.Errorf("recovery note present = %v, want %v:\n%s", got, tc.wantNote, notes)
				}
			})
		}
	}
}

// An override outside the boundary set carries neither note.
func TestCsConfirmNonBoundaryOverrideHasNoSideEffectNote(t *testing.T) {
	manifest := csConfirmManifestHead + "  s3_bucket: raw-orders\n  settings_overrides:\n    null_values: [\"N/A\"]\n"
	f := newCsConfirmFixture(t, csConfirmLive(nil), http.StatusOK, "")
	confirmYes(t)
	if _, err := f.apply(manifest); err != nil {
		t.Fatalf("confirmed apply: %v", err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("PATCH requests = %d, want 1", len(f.patches))
	}
	if !strings.Contains(f.stdoutAtPatch, "null_values") {
		t.Fatalf("preview does not list the override change:\n%s", f.stdoutAtPatch)
	}
	for _, note := range []string{"learn", "cant_sniff_header", "Side effects"} {
		if strings.Contains(f.stdout.String(), note) {
			t.Errorf("preview mentions %q for a change that has no side effect:\n%s", note, f.stdout.String())
		}
	}
}

// A 409 exits like any other 4xx and tells the user to re-run apply; a plain
// 4xx does not.
func TestCsConfirmConflictExitCodeAndHint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reply  string
		rerun  bool
	}{
		{"409", http.StatusConflict, csConfirmConflictBody, true},
		{"400", http.StatusBadRequest, `{"detail":{"msg":"Invalid cron expression.","error_id":""}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCsConfirmFixture(t, csConfirmLive(nil), tc.status, tc.reply)
			_, err := f.apply(csConfirmManifestHead+"  s3_bucket: new-bucket\n", "--yes")
			if err == nil {
				t.Fatalf("a %d PATCH succeeded", tc.status)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
			}
			if got := strings.Contains(err.Error(), "re-run apply"); got != tc.rerun {
				t.Errorf("message says re-run apply = %v, want %v: %v", got, tc.rerun, err)
			}
		})
	}
}

// The help lists every data source type and every field an update clears when
// the manifest leaves it out, from the code that decides them.
func TestCsConfirmHelpIsDerived(t *testing.T) {
	root := setupApplyRoot()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"apply", "cloud-source", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	help := stdout.String()

	typeLine, ok := csConfirmLine(help, "data_source_type:")
	if !ok {
		t.Fatalf("help has no data_source_type line:\n%s", help)
	}
	types := cloud_sources.ValidDataSourceTypes()
	if len(types) == 0 {
		t.Fatal("ValidDataSourceTypes is empty")
	}
	typeTokens := csConfirmTokens(typeLine)
	for _, dsType := range types {
		if !typeTokens[string(dsType)] {
			t.Errorf("data_source_type line %q does not list %s", typeLine, dsType)
		}
	}

	start, end := strings.Index(help, "UPDATE SEMANTICS"), strings.Index(help, "EXAMPLE")
	if start < 0 || end < start {
		t.Fatalf("help has no UPDATE SEMANTICS section before EXAMPLE:\n%s", help)
	}
	keys := cloud_sources.ClearedWhenOmittedKeys()
	if len(keys) == 0 {
		t.Fatal("ClearedWhenOmittedKeys is empty")
	}
	semantics := csConfirmTokens(help[start:end])
	for _, key := range keys {
		if !semantics[key] {
			t.Errorf("UPDATE SEMANTICS does not name %s:\n%s", key, help[start:end])
		}
	}
}
