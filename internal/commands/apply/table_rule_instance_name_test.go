package apply

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

// capturePatchBody registers the patch endpoint answering 200 with the
// fixture rule, and returns where the body it received is kept.
func capturePatchBody(mock *testutil.MockAPIServer) *[]byte {
	var body []byte
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			body, _ = io.ReadAll(r.Body)
			testutil.RespondJSON(w, http.StatusOK, sampleDatasetRuleDetailForApply())
		})
	return &body
}

// The round trip the ticket is about: what `get table-rule -o yaml` writes,
// status block included, applies as a patch, and a rename made by editing its
// metadata.instance_name reaches the request.
func TestApplyTableRulePatch_ExportedManifestRoundTrips(t *testing.T) {
	for name, tc := range map[string]struct {
		rename   string
		wantName string // "" when the body must carry no instance_name
	}{
		"renamed":  {rename: "renamed_rule", wantName: "renamed_rule"},
		"unedited": {},
	} {
		t.Run(name, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			confirmYes(t)
			registerParamsValidationMocks(mock)
			body := capturePatchBody(mock)

			detail := sampleDatasetRuleDetailForApply()
			exported := dataset_rules.APIResponseToManifest(&detail)
			if tc.rename != "" {
				exported.Metadata.InstanceName = tc.rename
			}
			data, err := yaml.Marshal(exported)
			if err != nil {
				t.Fatalf("marshal exported manifest: %v", err)
			}
			if !strings.Contains(string(data), "\nstatus:") || !strings.Contains(string(data), "  instance_name: ") {
				t.Fatalf("control: the export lacks its status block or metadata.instance_name\n%s", data)
			}

			if _, err := execApply(t, env.CreateFile("exported.yaml", string(data))); err != nil {
				t.Fatalf("the exported manifest must apply, got: %v", err)
			}
			var sent map[string]interface{}
			if err := json.Unmarshal(*body, &sent); err != nil {
				t.Fatalf("patch body %q: %v", *body, err)
			}
			got, present := sent["instance_name"]
			switch {
			case tc.wantName == "" && present:
				t.Errorf("an unedited export restates the live name, which must not be sent; body = %s", *body)
			case tc.wantName != "" && got != tc.wantName:
				t.Errorf("instance_name = %v, want %q; body = %s", got, tc.wantName, *body)
			}
		})
	}
}

// metadata.instance_name alone renames the rule, exactly as
// spec.instance_name does: the body is the minimal one.
func TestApplyTableRulePatch_MetadataInstanceNameSendsTheRename(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)
	registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
	body := capturePatchBody(mock)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
  instance_name: renamed_rule
spec:
  dataset_id: 1
`
	if _, err := execApply(t, env.CreateFile("rename.yaml", manifest)); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got, want := strings.TrimSpace(string(*body)), `{"instance_name":"renamed_rule"}`; got != want {
		t.Errorf("patch body = %s, want exactly %s", got, want)
	}
}

// A manifest whose only patchable key restates the live name sends nothing,
// and says why.
func TestApplyTableRulePatch_RestatedNameIsUnchanged(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()
	confirmYes(t)
	registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
	capturePatchBody(mock)

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
  instance_name: email_check
spec:
  dataset_id: 1
`
	out, err := execApply(t, env.CreateFile("restated.yaml", manifest))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if want := "Table rule " + testTableRuleID + " unchanged: spec.instance_name already matches the live rule"; !strings.Contains(out, want) {
		t.Errorf("output missing %q\n---\n%s", want, out)
	}
	for _, req := range mock.Requests() {
		if strings.HasPrefix(req, "PATCH ") {
			t.Errorf("a restated name sent a request: %s", req)
		}
	}
}

// Two different names for one field is refused with exit 2 before any
// request, in either mode.
func TestApplyTableRule_ConflictingInstanceNamesExitTwo(t *testing.T) {
	for mode, identity := range map[string]string{
		"patch":       "metadata:\n  id: " + testTableRuleID + "\n  instance_name: from_metadata\nspec:\n  dataset_id: 1\n",
		"instantiate": "metadata:\n  instance_name: from_metadata\nspec:\n  dataset_id: 1\n  rule_revision_id: " + testRuleRevID + "\n",
	} {
		t.Run(mode, func(t *testing.T) {
			env, mock := setupTableRuleApplyEnv(t)
			defer env.Cleanup()
			defer mock.Close()
			confirmYes(t)

			manifest := "apiVersion: qluster.ai/v1\nkind: TableRule\n" + identity + "  instance_name: from_spec\n"
			_, err := execApply(t, env.CreateFile("conflict.yaml", manifest))
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
		})
	}
}

// An instantiate manifest naming the rule in metadata sends that name, rather
// than leaving the server to generate one.
func TestApplyTableRuleInstantiate_MetadataInstanceNameIsSent(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/details", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleRuleRevisionFull(nil))
	})
	var instantiateReq api.InstantiateRuleRequestSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleRevID+"/instantiate", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&instantiateReq); err != nil {
			t.Errorf("decode instantiate request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusCreated, sampleDatasetRuleDetailForApply())
	})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  instance_name: my_email_check
spec:
  rule_revision_id: ` + testRuleRevID + `
  dataset_id: 1
`
	if _, err := execApply(t, env.CreateFile("instantiate.yaml", manifest), "--yes"); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if instantiateReq.InstanceName == nil || *instantiateReq.InstanceName != "my_email_check" {
		t.Errorf("instance_name sent = %v, want my_email_check", instantiateReq.InstanceName)
	}
}
