package rows

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

const (
	releaseTableRoute = "/datasets/42/update-keep-in-quarantine"
	releaseCloudRoute = "/data-sources/7/update-keep-in-quarantine"
)

// runRelease answers the release route with body and runs `rows release` at
// table scope, or at cloud source 7 when cloud is set.
func runRelease(t *testing.T, cloud bool, body string, extra ...string) (stdout, stderr string, err error) {
	t.Helper()
	mock := newRowsTestEnv(t)
	route := releaseTableRoute
	args := []string{"rows", "release", "orders"}
	if cloud {
		registerCloudSourceResolve(mock)
		route = releaseCloudRoute
		args = append(args, "--cloud-source-id", "7")
	}
	got := record(mock, "POST", "/api/orgs/"+testOrgID+route, body)
	stdout, stderr, err = runRows(t, append(append(args, extra...), "--yes")...)
	if got.hits != 1 {
		t.Fatalf("%s hits = %d, want 1", route, got.hits)
	}
	return stdout, stderr, err
}

// approvalEntry is one approvals element of the release answer.
func approvalEntry(rowID, current, required int) string {
	return fmt.Sprintf(`{"row_id":%d,"current_approvals":%d,"required_approvals":%d}`, rowID, current, required)
}

// approvalAnswer is the release answer of the approval path.
func approvalAnswer(affected, skipped int, released []int, approvals ...string) string {
	ids := make([]string, len(released))
	for i, id := range released {
		ids[i] = fmt.Sprint(id)
	}
	return fmt.Sprintf(`{"result":"ok","affected_rows":%d,"skipped_rows":%d,"released_row_ids":[%s],"approvals":[%s]}`,
		affected, skipped, strings.Join(ids, ","), strings.Join(approvals, ","))
}

func withSkippedRowIDs(body string, ids []int) string {
	encoded, _ := json.Marshal(ids)
	return strings.TrimSuffix(body, "}") + `,"skipped_row_ids":` + string(encoded) + "}"
}

func assertExitZero(t *testing.T, err error, stderr string) {
	t.Helper()
	if err != nil {
		t.Fatalf("err = %v (exit %d), want success\nstderr:\n%s", err, qerrors.GetExitCode(err), stderr)
	}
}

// Approvals recorded and nothing released: the rows are waiting, and nothing
// may read as released.
func TestRelease_ApprovalsRecordedNothingReleased(t *testing.T) {
	stdout, stderr, err := runRelease(t, false,
		approvalAnswer(2, 0, nil, approvalEntry(1044, 1, 2), approvalEntry(1045, 1, 3)),
		"--row-id", "1044", "--row-id", "1045")
	assertExitZero(t, err, stderr)

	want := "release: approved 2 rows in table orders (id: 42); 0 released, 2 short of approvals\n" +
		"Short of approvals:\n" +
		"  row 1044: 1 of 2 approvals\n" +
		"  row 1045: 1 of 3 approvals\n" +
		"\n" + followUpHeading + ":\n" +
		"  qctl get data orders --is-bad-data\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	// affected_rows on this path sums two UPDATEs; it is not the headline.
	if strings.Contains(stdout, "affected") {
		t.Errorf("stdout reports affected_rows on the approval path:\n%s", stdout)
	}
}

// The threshold met: released rows are listed as released, the rest as
// waiting, and the skipped rows are counted. Cloud-source scope.
func TestRelease_ThresholdMetAtCloudSourceScope(t *testing.T) {
	stdout, stderr, err := runRelease(t, true,
		withSkippedRowIDs(approvalAnswer(3, 2, []int{1042, 1043},
			approvalEntry(1042, 2, 2), approvalEntry(1043, 1, 1), approvalEntry(1044, 1, 2)), []int{1051, 1050}),
		"--status", "held_for_approval")
	assertExitZero(t, err, stderr)

	want := "release: approved 3 rows in cloud source s3-nightly (id: 7) of table orders; " +
		"2 released, 1 short of approvals\n" +
		"Released:\n" +
		"  row 1042: 2 of 2 approvals\n" +
		"  row 1043: 1 of 1 approval\n" +
		"Short of approvals:\n" +
		"  row 1044: 1 of 2 approvals\n" +
		"2 rows skipped: you edited them or submitted them for review, so another approver has to approve them.\n" +
		"Skipped row IDs: 1050 1051\n" +
		"\n" + followUpHeading + ":\n" +
		"  qctl get data orders --data-source-model-id 7 --is-bad-data\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

// At the threshold but absent from released_row_ids: approved, and still held.
// It must not read as released, nor as waiting.
func TestRelease_ApprovedButStillHeld(t *testing.T) {
	stdout, stderr, err := runRelease(t, false,
		approvalAnswer(2, 0, []int{1042}, approvalEntry(1042, 2, 2), approvalEntry(1046, 2, 2)),
		"--row-id", "1042", "--row-id", "1046")
	assertExitZero(t, err, stderr)

	want := "release: approved 2 rows in table orders (id: 42); " +
		"1 released, 0 short of approvals, 1 approved but still held\n" +
		"Released:\n" +
		"  row 1042: 2 of 2 approvals\n" +
		"Approved but still held (" + releaseHeldReason + "):\n" +
		"  row 1046: 2 of 2 approvals\n"
	if !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout:\n%s\nwant prefix:\n%s", stdout, want)
	}
	if strings.Contains(stdout, "Short of approvals:") {
		t.Errorf("an approved row is listed as waiting:\n%s", stdout)
	}
}

// Every row skipped is still a 200, and exits 0, at both scopes.
func TestRelease_EveryRowSkippedExitsZero(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		t.Run(fmt.Sprintf("cloud=%v", cloud), func(t *testing.T) {
			stdout, stderr, err := runRelease(t, cloud, withSkippedRowIDs(approvalAnswer(0, 3, nil), []int{9, 7, 8}),
				"--status", "held_for_approval")
			assertExitZero(t, err, stderr)
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Errorf("GetExitCode = %d, want 0", code)
			}
			for _, want := range []string{
				"release: approved 0 rows in ",
				"; nothing was released\n",
				"3 rows skipped: you edited them or submitted them for review, so another approver has to approve them.\n",
				"Skipped row IDs: 7 8 9\n",
			} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout)
				}
			}
			for _, bucket := range []string{"Released:", "Short of approvals:", "Approved but still held"} {
				if strings.Contains(stdout, bucket) {
					t.Errorf("stdout lists a %q group with no approvals:\n%s", bucket, stdout)
				}
			}
		})
	}
}

// Nothing matched: no approvals and nothing skipped.
func TestRelease_NothingMatched(t *testing.T) {
	stdout, stderr, err := runRelease(t, false, approvalAnswer(0, 0, nil), "--row-id", "9999")
	assertExitZero(t, err, stderr)
	if !strings.HasPrefix(stdout, "release: no rows matched in table orders (id: 42); nothing was approved or released\n") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, "skipped") {
		t.Errorf("stdout reports skipped rows when none were:\n%s", stdout)
	}
}

// A group over releaseRowCap rows is summarized by approval progress; a group
// at the cap still lists its rows.
func TestRelease_OverTheCapIsSummarizedByProgress(t *testing.T) {
	var entries []string
	var released []int
	for i := 0; i < releaseRowCap; i++ {
		id := 2000 + i
		released = append(released, id)
		entries = append(entries, approvalEntry(id, 2, 2))
	}
	for i := 0; i < 12; i++ {
		entries = append(entries, approvalEntry(3000+i, 1, 2))
	}
	for i := 0; i < 3; i++ {
		entries = append(entries, approvalEntry(4000+i, 1, 3))
	}
	stdout, stderr, err := runRelease(t, false,
		approvalAnswer(len(entries), 0, released, entries...), "--status", "held_for_approval")
	assertExitZero(t, err, stderr)

	for _, want := range []string{
		"release: approved 25 rows in table orders (id: 42); 10 released, 15 short of approvals\n",
		"Short of approvals:\n  12 rows at 1 of 2 approvals\n  3 rows at 1 of 3 approvals\n",
		fmt.Sprintf("Groups of more than %d rows are summarized by approval progress; -o json lists every row.\n", releaseRowCap),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "row 3000:") {
		t.Errorf("an over-cap group listed its rows:\n%s", stdout)
	}
	// Exactly at the cap: listed, not summarized.
	if got := strings.Count(stdout, ": 2 of 2 approvals\n"); got != releaseRowCap {
		t.Errorf("released rows listed = %d, want %d:\n%s", got, releaseRowCap, stdout)
	}
}

// Without a review policy the answer carries no approvals: the human output is
// the affected-row line hold prints, and the document's three approval keys
// are null.
func TestRelease_NoPolicyAnswer(t *testing.T) {
	const plain = `{"result":"ok","affected_rows":3}`
	for _, cloud := range []bool{false, true} {
		t.Run(fmt.Sprintf("cloud=%v", cloud), func(t *testing.T) {
			stdout, stderr, err := runRelease(t, cloud, plain, "--row-id", "7", "--row-id", "8", "--row-id", "9")
			assertExitZero(t, err, stderr)

			mock := newRowsTestEnv(t)
			route := releaseTableRoute
			args := []string{"rows", "hold", "orders"}
			if cloud {
				registerCloudSourceResolve(mock)
				route = releaseCloudRoute
				args = append(args, "--cloud-source-id", "7")
			}
			record(mock, "POST", "/api/orgs/"+testOrgID+route, plain)
			holdOut, _, err := runRows(t, append(args, "--row-id", "7", "--row-id", "8", "--row-id", "9", "--yes")...)
			if err != nil {
				t.Fatalf("hold: %v", err)
			}
			if want := "release:" + strings.TrimPrefix(holdOut, "hold:"); stdout != want {
				t.Errorf("stdout:\n%s\nwant hold's output under release's verb:\n%s", stdout, want)
			}
			if !strings.HasPrefix(stdout, "release: 3 row(s) affected in ") {
				t.Errorf("stdout:\n%s", stdout)
			}

			stdout, stderr, err = runRelease(t, cloud, plain, "--row-id", "7", "-o", "json")
			assertExitZero(t, err, stderr)
			got := decodeOne(t, stdout)
			if got["affected_rows"] != float64(3) {
				t.Errorf("affected_rows = %v, want 3", got["affected_rows"])
			}
			for _, key := range []string{"skipped_rows", "skipped_row_ids", "released_row_ids", "approvals"} {
				if v, ok := got[key]; !ok || v != nil {
					t.Errorf("%s = %v (present %v), want null", key, v, ok)
				}
			}
			if len(got) != 5 {
				t.Errorf("document = %v, want exactly five keys", got)
			}
			if !strings.Contains(stderr, "release: 3 row(s) affected in ") {
				t.Errorf("stderr missing the result line:\n%s", stderr)
			}
		})
	}
}

// The approval path's -o json document carries the server's fields, and the
// result lines move to stderr.
func TestRelease_JSONOnTheApprovalPath(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		t.Run(fmt.Sprintf("cloud=%v", cloud), func(t *testing.T) {
			stdout, stderr, err := runRelease(t, cloud,
				withSkippedRowIDs(approvalAnswer(2, 1, []int{1042}, approvalEntry(1042, 2, 2), approvalEntry(1044, 1, 2)), []int{1050}),
				"--row-id", "1042", "--row-id", "1044", "--row-id", "1050", "-o", "json")
			assertExitZero(t, err, stderr)

			got := decodeOne(t, stdout)
			if len(got) != 5 {
				t.Errorf("document = %v, want exactly five keys", got)
			}
			if got["affected_rows"] != float64(2) || got["skipped_rows"] != float64(1) {
				t.Errorf("document = %v, want affected_rows 2 and skipped_rows 1", got)
			}
			if ids, _ := got["released_row_ids"].([]any); len(ids) != 1 || ids[0] != float64(1042) {
				t.Errorf("released_row_ids = %v, want [1042]", got["released_row_ids"])
			}
			if ids, _ := got["skipped_row_ids"].([]any); len(ids) != 1 || ids[0] != float64(1050) {
				t.Errorf("skipped_row_ids = %v, want [1050]", got["skipped_row_ids"])
			}
			approvals, _ := got["approvals"].([]any)
			if len(approvals) != 2 {
				t.Fatalf("approvals = %v, want two entries", got["approvals"])
			}
			second, _ := approvals[1].(map[string]any)
			if second["row_id"] != float64(1044) || second["current_approvals"] != float64(1) || second["required_approvals"] != float64(2) {
				t.Errorf("approvals[1] = %v, want row 1044 at 1 of 2", approvals[1])
			}
			for _, want := range []string{"release: approved 2 rows in ", "  row 1044: 1 of 2 approvals\n", "1 row skipped:", followUpHeading} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q:\n%s", want, stderr)
				}
			}
		})
	}
}

func TestRelease_SkippedIDsAreBoundedOnlyInProse(t *testing.T) {
	ids := make([]int, releaseRowCap+2)
	for i := range ids {
		ids[i] = 2000 + i
	}
	body := withSkippedRowIDs(approvalAnswer(0, len(ids), nil), ids)
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			stdout, stderr, err := runRelease(t, false, body, "--status", "held_for_approval", "-o", format)
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("exit = %d, want 0: %v", code, err)
			}
			prose := stdout
			if format != "table" {
				prose = stderr
				var document struct {
					SkippedRowIDs []int `yaml:"skipped_row_ids"`
				}
				if err := yaml.Unmarshal([]byte(stdout), &document); err != nil {
					t.Fatal(err)
				}
				if len(document.SkippedRowIDs) != len(ids) {
					t.Fatalf("skipped IDs were lost: %s", stdout)
				}
			}
			for _, want := range []string{"Skipped row IDs: 2000 2001", "2009 … (2 more; -o json lists every skipped row)"} {
				if !strings.Contains(prose, want) {
					t.Errorf("prose missing %q:\n%s", want, prose)
				}
			}
			if strings.Contains(prose, "2010") || strings.Contains(prose, "2011") {
				t.Errorf("skipped IDs exceed prose cap:\n%s", prose)
			}
		})
	}
}

// An approval answer that matched nothing still writes empty lists, not
// nulls, so a script can tell it from the no-policy answer.
func TestRelease_JSONKeepsEmptyListsWhenNothingMatched(t *testing.T) {
	stdout, stderr, err := runRelease(t, false, withSkippedRowIDs(approvalAnswer(0, 0, nil), []int{}), "--row-id", "9999", "-o", "json")
	assertExitZero(t, err, stderr)
	got := decodeOne(t, stdout)
	for _, key := range []string{"skipped_row_ids", "released_row_ids", "approvals"} {
		if v, ok := got[key].([]any); !ok || len(v) != 0 {
			t.Errorf("%s = %v, want []", key, got[key])
		}
	}
	if got["skipped_rows"] != float64(0) {
		t.Errorf("skipped_rows = %v, want 0", got["skipped_rows"])
	}
}

func TestRelease_OlderApprovalAnswerDoesNotInventSkippedIDs(t *testing.T) {
	stdout, stderr, err := runRelease(t, false, approvalAnswer(0, 1, nil), "--row-id", "7", "-o", "json")
	if code := qerrors.GetExitCode(err); code != 0 {
		t.Fatalf("exit = %d, want 0: %v", code, err)
	}
	got := decodeOne(t, stdout)
	if ids, present := got["skipped_row_ids"]; !present || ids != nil {
		t.Errorf("skipped_row_ids = %v (present %v), want null", ids, present)
	}
	if strings.Contains(stderr, "Skipped row IDs:") || !strings.Contains(stderr, "1 row skipped:") {
		t.Errorf("older answer's report = %s", stderr)
	}
}

// hold and reset-hold keep their one-key document.
func TestHoldAndResetHold_JSONStaysAffectedRowsOnly(t *testing.T) {
	for _, verb := range []string{"hold", resetHoldVerb} {
		t.Run(verb, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			record(mock, "POST", "/api/orgs/"+testOrgID+releaseTableRoute, `{"result":"ok","affected_rows":2}`)
			stdout, _, err := runRows(t, "rows", verb, "orders", "--row-id", "7", "--yes", "-o", "json")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stdout != "{\n  \"affected_rows\": 2\n}\n" {
				t.Errorf("stdout = %q, want the {affected_rows} document", stdout)
			}
		})
	}
}
