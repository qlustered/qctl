package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestApplyCloudSourceRepointRefusesMissingSecretsBeforeConfirmation(t *testing.T) {
	for _, typed := range []bool{false, true} {
		for _, value := range []string{"", "**********", "${QCTL_REPOINT_MISSING}"} {
			t.Run(value+"/typed="+strconv.FormatBool(typed), func(t *testing.T) {
				t.Setenv("QCTL_REPOINT_MISSING", "")
				live := sampleCloudSourceFull()
				live.S3SecretKey = testutil.StringPtr("**********")
				server := newCloudSourceServer(t, live)
				forbidCloudSourcePrompt(t)
				manifest := s3OrdersManifest("  s3_prefix: other/\n  s3_secret_key: '" + value + "'\n")
				root := setupApplyRoot()
				root.SilenceErrors, root.SilenceUsage = true, true
				var out bytes.Buffer
				root.SetOut(&out)
				root.SetErr(&out)
				args := []string{"apply"}
				if typed {
					args = append(args, "cloud-source")
				}
				root.SetArgs(append(args, "-f", server.env.CreateFile("cs.yaml", manifest), "--yes"))
				err := root.Execute()
				if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.s3_secret_key") {
					t.Fatalf("expected missing secret at exit 2: %v", err)
				}
				if len(server.writes()) != 0 {
					t.Fatalf("refused apply wrote: %v", server.writes())
				}
			})
		}
	}
}

func TestApplyCloudSourceDraftRepointWarnsBeforeWriting(t *testing.T) {
	for _, yes := range []bool{false, true} {
		t.Run("yes="+strconv.FormatBool(yes), func(t *testing.T) {
			live := sampleCloudSourceFull()
			state := api.DataSourceStateDraft
			live.State = &state
			live.S3SecretKey = testutil.StringPtr("**********")
			server := newCloudSourceServer(t, live)
			root := setupApplyRoot()
			root.SilenceErrors, root.SilenceUsage = true, true
			var out, stderr bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&stderr)
			server.mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/data-sources/42", func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(stderr.String(), "clears omitted secrets") {
					t.Error("draft warning was not printed before PATCH")
				}
				server.record(t, live)(w, r)
			})
			if !yes {
				cmdutil.SetReader(strings.NewReader("n\n"))
				t.Cleanup(cmdutil.ResetReader)
			}
			args := []string{"apply", "-f", server.env.CreateFile("cs.yaml", s3OrdersPrefixManifest)}
			if yes {
				args = append(args, "--yes", "-o", "json")
			}
			root.SetArgs(args)
			err := root.Execute()
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"Warning:", "clears omitted secrets", "spec.s3_secret_key"} {
				if !strings.Contains(stderr.String(), text) {
					t.Errorf("stderr lacks %q: %s", text, stderr.String())
				}
			}
			if yes {
				if len(server.writes()) != 1 || !json.Valid(out.Bytes()) {
					t.Fatalf("writes = %v, stdout = %s", server.writes(), out.String())
				}
			} else if len(server.writes()) != 0 {
				t.Fatalf("declined apply wrote: %v", server.writes())
			}
		})
	}
}

func TestApplyCloudSourceRepointExpandsAndResendsUnchangedURL(t *testing.T) {
	const url = "https://example.com/book.xlsx?token=private"
	t.Setenv("QCTL_REPOINT_URL", url)
	live := sampleCloudSourceFull()
	typ := api.DataSourceTypeSimpleURL
	live.DataSourceType, live.SimpleURL = &typ, testutil.StringPtr(url)
	server := newCloudSourceServer(t, live)
	manifest := "apiVersion: qluster.ai/v1\nkind: CloudSource\nmetadata:\n  name: s3-orders\nspec:\n  dataset_id: 11\n  data_source_type: simple_url\n  simple_url: ${QCTL_REPOINT_URL}\n  excel_sheet_name: Sheet2\n"
	if _, err := server.apply(t, manifest, "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(server.bodies) != 1 || string(server.bodies[0]["simple_url"]) != `"`+url+`"` {
		t.Fatalf("unchanged URL was not resupplied: %v", server.bodies)
	}
}
