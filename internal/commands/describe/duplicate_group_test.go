package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	dgGroupID = "0190aaaa-1111-4222-8333-444444444444"
	dgPath    = "/api/orgs/" + ctDescribeOrgID + "/datasets/42/duplicate-groups/" + dgGroupID
)

const dgDetail = `{"id":"` + dgGroupID + `","origin":"identical_rows","identity":"9f2a","file_key":"",` +
	`"data_source_model_id":5,"stored_item_id":311,"created_at":"2026-11-10T10:00:00Z","ended_at":null,"end_reason":null,` +
	`"ended_by_id":null,"key_values":null,"masked_fields":null,"created_by_id":null,"comment":null,"hold_mode":null,` +
	`"state":"needs_decision","awaiting_approval":false,"awaiting_corrected_file":false,"held_count":2,"unapplied":false,` +
	`"comment_thread_id":null,"comment_thread_is_internal":null,"shared_at":null,"shared_by_id":null,"shared_by_name":null,` +
	`"responses":[],"decision":null,"carried_decision":null,` +
	`"caller":{"can_decide":true,"reason":null,"caller_approved":false,"can_retract":false,"retract_reason":null},` +
	`"members":[{"id":812,"row_id":1042,"data_source_model_id":5,"location":"quarantine","rank":1,"outcome":"held",` +
	`"identical_at_load_to":[813],"deleted_at":null,"deleted_by_id":null,` +
	`"values":{"amount":10.5,"email":"a***@x","empty":null}}],"differing_cells":[],` +
	`"earlier_decisions":[],"earlier_responses":[]}`

func execDescribeDuplicateGroup(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := setupCommentThreadDescribeCommand()
	root.SilenceUsage, root.SilenceErrors = true, true
	describeCmd, _, _ := root.Find([]string{"describe"})
	describeCmd.AddCommand(NewDuplicateGroupCommand())
	cmdutil.StripKindRefs(root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"describe", "duplicate-group"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestDescribeDuplicateGroup_UnappliedPeriods(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	mock.RegisterHandler("GET", dgPath, func(w http.ResponseWriter, _ *http.Request) {
		detail := strings.Replace(dgDetail, `"unapplied":false`, `"unapplied":true,`+
			`"unapplied_periods":[{"dataset_id":43,"outcome":"skipped_locked","reason":"Period locked"}]`, 1)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(detail))
	})
	for _, format := range []string{"table", "json", "yaml"} {
		out, stderr, err := execDescribeDuplicateGroup(t, dgGroupID, "--table-id", "42", "-o", format)
		if err != nil {
			t.Fatalf("%s: %v\n%s", format, err, stderr)
		}
		for _, want := range []string{"skipped_locked", "Period locked"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s lacks %q:\n%s", format, want, out)
			}
		}
		if format == "table" && !strings.Contains(out, "UNAPPLIED PERIODS\n  table/43") {
			t.Errorf("missing period reference:\n%s", out)
		}
	}
}

func TestDescribeDuplicateGroup_Text(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	mock.RegisterHandler("GET", dgPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(dgDetail))
	})

	out, stderr, err := execDescribeDuplicateGroup(t, "duplicate-group/"+dgGroupID, "--table-id", "42")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{
		"duplicate-group/" + dgGroupID + " (table/42)", "Origin:    identical_rows", "identity: 9f2a",
		"MEMBERS (1)", "qctl duplicate-group allow " + dgGroupID + " --table-id 42 --count N",
		"MEMBER VALUES\n  member 812 (row 1042):\n    amount: 10.5\n    email: a***@x\n    empty: null\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := mock.Requests(); len(got) != 1 {
		t.Errorf("requests = %q, want the one group read", got)
	}
}

func TestDescribeDuplicateGroup_JSONIsTheRawGroup(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	mock.RegisterHandler("GET", dgPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(dgDetail))
	})

	out, stderr, err := execDescribeDuplicateGroup(t, dgGroupID, "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["identity"] != "9f2a" || got["earlier_decisions"] == nil {
		t.Errorf("not the raw detail: %v", got)
	}
}

func TestDescribeDuplicateGroup_RefusalsBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"812", "--table-id", "42"},
		{dgGroupID},
	} {
		mock := newCommentThreadDescribeEnv(t)
		_, _, err := execDescribeDuplicateGroup(t, args...)
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("%v: exit = %d, want 2 (err %v)", args, got, err)
		}
		if got := mock.Requests(); len(got) != 0 {
			t.Errorf("%v: requests = %q, want none", args, got)
		}
	}
}

func TestDescribeDuplicateGroup_NotFoundNamesTheList(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	mock.RegisterHandler("GET", dgPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "not found")
	})

	_, _, err := execDescribeDuplicateGroup(t, dgGroupID, "--table-id", "42")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit = %d, want not found (err %v)", got, err)
	}
	if !strings.Contains(err.Error(), "qctl get duplicate-groups --table-id 42 --include-ended") {
		t.Errorf("error lacks the list hint: %v", err)
	}
}

const (
	dgSourcePath   = "/api/orgs/" + ctDescribeOrgID + "/datasets/42/source-duplicate-groups/" + dgGroupID
	dgSourceDetail = `{"id":"` + dgGroupID + `","origin":"repeated_key","data_source_model_id":5,"stored_item_id":311,` +
		`"file_key":"","key_values":{"policy_id":"P-10","email":"a***@x"},"state":"held","held_reason":"undecided",` +
		`"created_at":"2026-11-10T10:00:00Z","ended_at":null,"end_reason":null,"comment_thread_id":null,"decision":null,` +
		`"members":[{"id":812,"row_id":1042,"data_source_model_id":5,"location":"quarantine","stored_item_id":311,` +
		`"file_row_number":17,"outcome":"held"},{"id":813,"row_id":1043,"data_source_model_id":7,"location":"quarantine",` +
		`"stored_item_id":312,"file_row_number":4,"outcome":"held"}],"differing_cells":[],"masked_fields":["email"],` +
		`"responses":[{"id":"0190dddd-1111-4222-8333-444444444444","group_id":"` + dgGroupID + `",` +
		`"data_source_model_id":5,"kind":"separate_rows","comment":"two orders","author_name":"Sam",` +
		`"created_at":"2026-11-11T10:00:00Z","ended_at":null,"end_reason":null}],"awaiting_corrected_file":false,` +
		`"caller":{"can_respond":true,"reason":null},"earlier_responses":[]}`
)

func TestDescribeDuplicateGroup_SenderText(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	mock.RegisterHandler("GET", dgSourcePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(dgSourceDetail))
	})

	out, stderr, err := execDescribeDuplicateGroup(t, dgGroupID, "--table-id", "42", "--sender")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{
		"duplicate-group/" + dgGroupID + " (table/42, as your cloud sources see it)",
		"State:     held (undecided) — the table's reviewers have not decided it",
		"DECISION\n  none in effect: the table's reviewers decide",
		"YOUR COPIES (2)", "masked for you: email",
		"YOUR RESPONSES (1)\n  separate_rows [r: 0190dddd-1111-4222-8333-444444444444]",
		"your copies are on cloud sources 5, 7",
		"qctl duplicate-group respond " + dgGroupID + " --kind separate-rows --comment <text> --cloud-source-id <id> --table-id 42",
		"a request, not a decision",
		"qctl duplicate-group withdraw-response " + dgGroupID + " 0190dddd-1111-4222-8333-444444444444 --table-id 42",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := mock.Requests(); len(got) != 1 {
		t.Errorf("requests = %q, want the one source group read", got)
	}
}

func TestDescribeDuplicateGroup_SenderJSONIsTheRawGroup(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	mock.RegisterHandler("GET", dgSourcePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(dgSourceDetail))
	})

	out, stderr, err := execDescribeDuplicateGroup(t, dgGroupID, "--table-id", "42", "--sender", "-o", "yaml")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(out, "held_reason: undecided") || !strings.Contains(out, "earlier_responses: []") {
		t.Errorf("not the raw source detail:\n%s", out)
	}
}

func TestDescribeDuplicateGroup_SenderNotFoundNamesTheSenderList(t *testing.T) {
	mock := newCommentThreadDescribeEnv(t)
	mock.RegisterHandler("GET", dgSourcePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondError(w, http.StatusNotFound, "not found")
	})

	_, _, err := execDescribeDuplicateGroup(t, dgGroupID, "--table-id", "42", "--sender")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit = %d, want not found (err %v)", got, err)
	}
	if !strings.Contains(err.Error(), "qctl get duplicate-groups --table-id 42 --sender --include-ended") {
		t.Errorf("error lacks the sender list hint: %v", err)
	}
}
