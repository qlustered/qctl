package alerts

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// heldAlertFull is an unresolved preload_check_held alert whose file tripped
// row_drop and period_mismatch, offering the actions the issue type declares.
func heldAlertFull() AlertFull {
	a := sampleAlertFull()
	resolved := false
	list := []api.AlertAction{api.AlertActionReleaseHeldFile, api.AlertActionRejectHeldFile, api.AlertActionTryAgain}
	a.ID = 12
	a.IssueType = api.AlertTypePreloadCheckHeld
	a.Resolved = &resolved
	a.AlertActionsList = &list
	share := 0.3
	bucket := api.PreloadCheckPeriodBucketPeriod
	findings := []api.PreloadCheckFinding{
		{Reason: api.RowDrop, RowCount: alertPtr(40), BaselineRowCount: alertPtr(1000), RowDropShare: &share},
		{
			Reason: api.PeriodMismatch, LargestBucket: &bucket, LargestPeriodKey: alertPtr("2026-05"),
			LargestRowCount: alertPtr(30), RoutedPeriodKey: alertPtr("2026-06"), RoutedRowCount: alertPtr(10),
			NonBlankRowCount: alertPtr(40),
		},
	}
	reason := api.NotAMember
	a.Body = &api.AlertBodySchema{Findings: &findings, NotAskedReason: &reason, FileName: alertPtr("june.csv")}
	return a
}

// findings, asked_user_id and not_asked_reason say why the file is held and
// who was asked, so describe shows them at the default tier, and -v keeps them.
func TestPreloadCheckHeldBodyIsShownAtDefaultVerbosity(t *testing.T) {
	a := heldAlertFull()
	for _, verbosity := range []int{0, 1} {
		body := APIResponseToManifest(&a, nil, verbosity).Spec.Body
		if body == nil || len(body.Findings) != 2 || body.NotAskedReason != "not_a_member" {
			t.Fatalf("verbosity %d: body = %#v, want two findings and not_asked_reason not_a_member", verbosity, body)
		}
		if got := body.Findings[1]; got.Reason != "period_mismatch" || got.LargestBucket != "period" ||
			got.RoutedPeriodKey == nil || *got.RoutedPeriodKey != "2026-06" {
			t.Errorf("verbosity %d: period_mismatch finding = %#v", verbosity, got)
		}
	}
	if body := APIResponseToManifest(&a, nil, 0).Spec.Body; body.FileName != "" {
		t.Errorf("default tier leaked a -v body field: %#v", body)
	}

	asked := uuid.MustParse("6f1c2a4e-0d1b-4c8e-9a51-3b7d2e8f4a10")
	a.Body = &api.AlertBodySchema{AskedUserID: &asked}
	for _, verbosity := range []int{0, 1} {
		body := APIResponseToManifest(&a, nil, verbosity).Spec.Body
		if body == nil || body.AskedUserID != asked.String() {
			t.Errorf("verbosity %d: body = %#v, want asked_user_id %s", verbosity, body, asked)
		}
	}
}

// A finding carries only its own trigger's keys in both encodings.
func TestPreloadCheckFindingRendersOnlyItsTriggerKeys(t *testing.T) {
	a := heldAlertFull()
	findings := (*a.Body.Findings)[:1]
	a.Body.Findings = &findings
	doc := markdown.ProcessFieldsPlain(APIResponseToManifest(&a, nil, 0), []string{"message"})

	out, err := jsonnum.MarshalYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	yamlText := string(out)
	if !strings.Contains(yamlText, "row_drop_share: 0.3\n") {
		t.Errorf("yaml lacks row_drop_share: 0.3:\n%s", yamlText)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"row_drop_share":0.3,`) && !strings.Contains(string(raw), `"row_drop_share":0.3}`) {
		t.Errorf("json lacks row_drop_share 0.3: %s", raw)
	}
	for _, key := range []string{"largest_bucket", "largest_period_key", "largest_row_count", "routed_period_key", "routed_row_count", "non_blank_row_count", "missing_columns"} {
		if strings.Contains(yamlText, key) || strings.Contains(string(raw), key) {
			t.Errorf("row_drop finding carries %s:\n%s", key, yamlText)
		}
	}
}

// A replaces_loaded_file finding names the loaded file it would replace, at
// every verbosity, with its load time as RFC3339 in UTC.
func TestReplacesLoadedFileFindingIsShownAtDefaultVerbosity(t *testing.T) {
	a := heldAlertFull()
	loadedAt := time.Date(2026, 7, 3, 9, 30, 0, 0, time.FixedZone("PDT", -7*3600))
	findings := []api.PreloadCheckFinding{{
		Reason: api.ReplacesLoadedFile, RowCount: alertPtr(40), RoutedPeriodKey: alertPtr("2026-06"),
		ReplacedStoredItemID: alertPtr(77), ReplacedFileName: alertPtr("june_v1.csv"), ReplacedLoadedAt: &loadedAt,
		ReplacedRowCount: alertPtr(1000), PeriodLabel: alertPtr("Jun 2026"),
	}}
	a.Body.Findings = &findings
	for _, verbosity := range []int{0, 1} {
		body := APIResponseToManifest(&a, nil, verbosity).Spec.Body
		if body == nil || len(body.Findings) != 1 {
			t.Fatalf("verbosity %d: body = %#v, want one finding", verbosity, body)
		}
		got := body.Findings[0]
		if got.ReplacedStoredItemID == nil || *got.ReplacedStoredItemID != 77 || got.ReplacedFileName != "june_v1.csv" ||
			got.ReplacedLoadedAt != "2026-07-03T16:30:00Z" || got.ReplacedRowCount == nil || *got.ReplacedRowCount != 1000 ||
			got.PeriodLabel != "Jun 2026" {
			t.Errorf("verbosity %d: replaces_loaded_file finding = %#v", verbosity, got)
		}
	}
	out, err := jsonnum.MarshalYAML(APIResponseToManifest(&a, nil, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"replaced_stored_item_id: 77\n", "replaced_file_name: june_v1.csv\n", "replaced_loaded_at: \"2026-07-03T16:30:00Z\"\n",
		"replaced_row_count: 1000\n", "period_label: Jun 2026\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("yaml lacks %q:\n%s", want, out)
		}
	}

	// A finding of another reason carries none of them.
	a = heldAlertFull()
	raw, err := json.Marshal(APIResponseToManifest(&a, nil, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"replaced_stored_item_id", "replaced_file_name", "replaced_loaded_at", "replaced_row_count", "period_label"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("row_drop or period_mismatch finding carries %s: %s", key, raw)
		}
	}
}

// sent_to_period_key says where send_held_file_to_period sent the file, so
// describe shows it at every verbosity, and alone it keeps the body.
func TestSentToPeriodKeyIsShownAtDefaultVerbosity(t *testing.T) {
	a := heldAlertFull()
	a.Body = &api.AlertBodySchema{SentToPeriodKey: alertPtr("2026-05")}
	for _, verbosity := range []int{0, 1} {
		body := APIResponseToManifest(&a, nil, verbosity).Spec.Body
		if body == nil || body.SentToPeriodKey != "2026-05" {
			t.Errorf("verbosity %d: body = %#v, want sent_to_period_key 2026-05", verbosity, body)
		}
	}
	a = heldAlertFull()
	if body := APIResponseToManifest(&a, nil, 0).Spec.Body; body.SentToPeriodKey != "" {
		t.Errorf("a null sent_to_period_key is shown: %q", body.SentToPeriodKey)
	}
}

// The three held-file decisions say what they settle and when they answer
// 409, so NEXT ACTIONS prints the backend's note under each.
func TestFooterPrintsTheHeldFileActionNotes(t *testing.T) {
	a := heldAlertFull()
	list := append(*a.AlertActionsList, api.AlertActionSendHeldFileToPeriod)
	a.AlertActionsList = &list
	for mode, canPrompt := range terminalModes {
		out := RenderNextActionsPlain(&a, canPrompt)
		for _, action := range []string{"release_held_file", "reject_held_file", "send_held_file_to_period"} {
			if note := api.AlertActionFields[action].Note; note == "" || !strings.Contains(out, "    "+note+"\n") {
				t.Errorf("%s: footer lacks the %s note:\n%s", mode, action, out)
			}
		}
	}
}

// removed_row_count says what loading the held file costs, so describe shows
// it at every verbosity, a zero count included, and leaves it out when null.
func TestPreloadCheckHeldRemovedRowCountIsShownAtDefaultVerbosity(t *testing.T) {
	for _, n := range []int{0, 120} {
		a := heldAlertFull()
		a.Body.RemovedRowCount = alertPtr(n)
		for _, verbosity := range []int{0, 1} {
			body := APIResponseToManifest(&a, nil, verbosity).Spec.Body
			if body == nil || body.RemovedRowCount == nil || *body.RemovedRowCount != n {
				t.Fatalf("verbosity %d: body = %#v, want removed_row_count %d", verbosity, body, n)
			}
		}
		out, err := jsonnum.MarshalYAML(APIResponseToManifest(&a, nil, 0))
		if err != nil {
			t.Fatal(err)
		}
		if want := "removed_row_count: " + strconv.Itoa(n) + "\n"; !strings.Contains(string(out), want) {
			t.Errorf("yaml lacks %q:\n%s", want, out)
		}
	}
	a := heldAlertFull()
	if body := APIResponseToManifest(&a, nil, 0).Spec.Body; body.RemovedRowCount != nil {
		t.Errorf("a null removed_row_count is shown: %d", *body.RemovedRowCount)
	}
}
