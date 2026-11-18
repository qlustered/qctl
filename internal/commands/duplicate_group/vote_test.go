package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const (
	otherID    = "0190ffff-1111-4222-8333-444444444444"
	approveURL = groupPath + "/decision/" + decisionID + "/approve"
	disagreURL = groupPath + "/decision/" + decisionID + "/disagree"
	dismissURL = groupPath + "/decision/" + decisionID + "/dismiss"
	withdrawTo = groupPath + "/decision/withdraw"
	stopURL    = groupPath + "/decision/stop-series"
)

type voteCase struct {
	verb  string
	path  string
	args  []string
	body  map[string]any
	state string
	line  string
}

func voteCases() []voteCase {
	return []voteCase{
		{"approve", approveURL, []string{"--comment", "ok"}, map[string]any{"comment": "ok"}, "effective",
			"approved allow 2 [d: " + decisionID + "] (decision: effective"},
		{"disagree", disagreURL, []string{"--reason", "two orders"}, map[string]any{"reason": "two orders"}, "pending",
			"disagreed with allow 2 [d: " + decisionID + "] (decision: pending"},
		{"dismiss", dismissURL, []string{"--reason", "wrong key"}, map[string]any{"reason": "wrong key"}, "dismissed",
			"dismissed allow 2 [d: " + decisionID + "] (decision: dismissed"},
		{"withdraw", withdrawTo, []string{"--comment", "wrong key"},
			map[string]any{"decision_id": decisionID, "comment": "wrong key"}, "withdrawn",
			"withdrew allow 2 [d: " + decisionID + "] (decision: withdrawn"},
	}
}

func allowTwo(d map[string]any) { d["allowed_count"] = 2 }

// Each vote verb acts on the group's pending decision, named in the URL or
// the body, after one read.
func TestVoteVerbsTargetThePendingDecision(t *testing.T) {
	for _, tc := range voteCases() {
		for _, pin := range []bool{false, true} {
			name := tc.verb
			if pin {
				name += " with --decision"
			}
			t.Run(name, func(t *testing.T) {
				e := newEnv(t)
				e.group(groupJSON("repeated_key", pending("allow", allowTwo)))
				e.respond("POST", tc.path, http.StatusOK, decisionResponse(decisionJSON(decisionID, "allow", tc.state, allowTwo)))
				args := append([]string{tc.verb, "duplicate-group/" + groupID, "--table-id", "42", "--yes"}, tc.args...)
				if pin {
					args = append(args, "--decision", decisionID)
				}
				out, _, err := e.run(args...)
				assertExit(t, err, 0)
				e.assertRequests("GET "+groupPath, "POST "+tc.path)
				if got := e.postBody(tc.path); !reflect.DeepEqual(got, tc.body) {
					t.Errorf("body = %v, want %v", got, tc.body)
				}
				for _, want := range []string{"duplicate-group/" + groupID + " " + tc.line,
					"Next: qctl describe duplicate-group " + groupID + " --table-id 42"} {
					if !strings.Contains(out, want) {
						t.Errorf("stdout lacks %q:\n%s", want, out)
					}
				}
			})
		}
	}
}

func TestVoteDecisionMismatchExitsTwo(t *testing.T) {
	for _, tc := range voteCases() {
		t.Run(tc.verb, func(t *testing.T) {
			e := newEnv(t)
			e.group(groupJSON("repeated_key", pending("allow")))
			args := append([]string{tc.verb, groupID, "--table-id", "42", "--yes", "--decision", otherID}, tc.args...)
			_, _, err := e.run(args...)
			assertExit(t, err, qerrors.ExitBadUsage)
			e.assertRequests("GET " + groupPath)
			if err != nil && !strings.Contains(err.Error(), "its pending decision is allow [d: "+decisionID+"]") {
				t.Errorf("err does not name the pending decision: %v", err)
			}
		})
	}
}

func TestVoteWithNothingPendingExitsTwo(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   []string
		absent string
	}{
		{"undecided", func(map[string]any) {}, []string{"has no pending decision to"}, "effective"},
		{"own effective decision", func(g map[string]any) {
			g["decision"] = decisionJSON(carriedID, "keep_one", "effective", func(d map[string]any) { d["kept_member_id"] = 812 })
		}, []string{"its effective keep-one member 812 stays until a change takes effect",
			"propose one with allow, keep-one or not-duplicates"}, "stop-series"},
		{"carried series allow", func(g map[string]any) {
			g["carried_decision"] = decisionJSON(carriedID, "allow", "effective", func(d map[string]any) {
				d["carried"] = true
				d["scope"] = "series"
			})
			g["caller"].(map[string]any)["stop_for_series_decision_id"] = carriedID
		}, []string{"its effective allow stays",
			"'qctl duplicate-group stop-series " + groupID + " --table-id 42' proposes to stop the series allow [d: " +
				carriedID + "]"}, ""},
	}
	for _, tc := range cases {
		for _, v := range voteCases() {
			t.Run(tc.name+"/"+v.verb, func(t *testing.T) {
				e := newEnv(t)
				e.group(groupJSON("repeated_key", tc.mutate))
				_, _, err := e.run(append([]string{v.verb, groupID, "--table-id", "42", "--yes"}, v.args...)...)
				assertExit(t, err, qerrors.ExitBadUsage)
				e.assertRequests("GET " + groupPath)
				if err == nil {
					return
				}
				for _, want := range tc.want {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("err lacks %q: %v", want, err)
					}
				}
				if tc.absent != "" && strings.Contains(err.Error(), tc.absent) {
					t.Errorf("err carries %q: %v", tc.absent, err)
				}
			})
		}
	}
}

func approvedByCaller(g map[string]any) {
	pending("allow", allowTwo)(g)
	g["caller"].(map[string]any)["caller_approved"] = true
}

// Re-approving is allowed, so an approval you already gave is no error: with
// nothing to change it sends nothing.
func TestApproveAlreadyApprovedSendsNothing(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key", approvedByCaller))
	out, _, err := e.run("approve", groupID, "--table-id", "42")
	assertExit(t, err, 0)
	e.assertRequests("GET " + groupPath)
	if !strings.Contains(out, "duplicate-group/"+groupID+" already has your approval on the pending allow 2 [d: "+
		decisionID+"] (approvals 1/2)") {
		t.Errorf("stdout = %q", out)
	}
}

func TestApproveAlreadyApprovedNameOutput(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key", approvedByCaller))
	out, errOut, err := e.run("approve", groupID, "--table-id", "42", "-o", "name")
	assertExit(t, err, 0)
	if out != "duplicate-group/"+groupID+"\n" {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(errOut, "already has your approval") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestApproveAgainWithACommentReplacesIt(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key", approvedByCaller))
	e.respond("POST", approveURL, http.StatusOK, decisionResponse(decisionJSON(decisionID, "allow", "pending", allowTwo)))
	_, _, err := e.run("approve", groupID, "--table-id", "42", "--comment", "still two", "--yes")
	assertExit(t, err, 0)
	e.assertRequests("GET "+groupPath, "POST "+approveURL)
	if got := e.postBody(approveURL); !reflect.DeepEqual(got, map[string]any{"comment": "still two"}) {
		t.Errorf("body = %v", got)
	}
}

func TestApproveReportsWhatTheDecisionWaitsFor(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key", pending("allow")))
	held := decisionJSON(decisionID, "allow", "pending", func(d map[string]any) {
		d["disagreements"] = []any{
			map[string]any{"reviewer_id": otherID, "disagreed_at": "2026-11-11T12:00:00Z", "reason": "no", "counted": true},
			map[string]any{"reviewer_id": carriedID, "disagreed_at": "2026-11-11T12:00:00Z", "reason": "no", "counted": false},
		}
	})
	e.respond("POST", approveURL, http.StatusOK, decisionResponse(held))
	out, _, err := e.run("approve", groupID, "--table-id", "42", "--yes")
	assertExit(t, err, 0)
	if got := e.postBody(approveURL); !reflect.DeepEqual(got, map[string]any{"comment": nil}) {
		t.Errorf("body = %v", got)
	}
	for _, want := range []string{"Waiting for 1 more approval(s)",
		"Held by 1 disagreement(s): it takes effect only once each disagreeing reviewer approves"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}

func TestDisagreeSaysHowToTakeItBack(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key", pending("allow")))
	e.respond("POST", disagreURL, http.StatusOK, decisionResponse(decisionJSON(decisionID, "allow", "pending")))
	out, _, err := e.run("disagree", groupID, "--table-id", "42", "--reason", "two orders", "--yes")
	assertExit(t, err, 0)
	want := "'qctl duplicate-group approve " + groupID + " --table-id 42' replaces it with your approval"
	if !strings.Contains(out, want) {
		t.Errorf("stdout lacks %q:\n%s", want, out)
	}
}

// Each route's refusal keeps the exit code its status maps to and gains the
// hint for its code.
func TestVoteRefusalHints(t *testing.T) {
	notLive := apiError("DUPLICATE_DECISION_NOT_LIVE", "not pending", nil)
	cases := []struct {
		name   string
		verb   string
		path   string
		args   []string
		status int
		body   map[string]any
		exit   int
		want   string
	}{
		{"approve not live", "approve", approveURL, nil, http.StatusConflict, notLive,
			qerrors.ExitBadUsage, "no longer the group's pending decision"},
		{"withdraw not live", "withdraw", withdrawTo, nil, http.StatusConflict, notLive,
			qerrors.ExitBadUsage, "no longer the group's pending decision"},
		{"dismiss not live", "dismiss", dismissURL, []string{"--reason", "x"}, http.StatusConflict, notLive,
			qerrors.ExitBadUsage, "qctl describe duplicate-group " + groupID + " --table-id 42"},
		{"withdraw not yours", "withdraw", withdrawTo, nil, http.StatusForbidden,
			apiError("DUPLICATE_DECISION_NOT_YOURS", "not yours", nil),
			qerrors.ExitUnauthorized, "only its proposer may withdraw it"},
		{"disagree as proposer", "disagree", disagreURL, []string{"--reason", "x"}, http.StatusForbidden,
			apiError("DUPLICATE_DECISION_PROPOSER", "you proposed it", nil),
			qerrors.ExitUnauthorized, "'qctl duplicate-group withdraw " + groupID + " --table-id 42'"},
		{"dismiss as proposer", "dismiss", dismissURL, []string{"--reason", "x"}, http.StatusForbidden,
			apiError("DUPLICATE_DECISION_PROPOSER", "you proposed it", nil),
			qerrors.ExitUnauthorized, "withdraw it instead"},
		{"dismiss not admin", "dismiss", dismissURL, []string{"--reason", "x"}, http.StatusForbidden,
			map[string]any{"detail": map[string]any{"msg": "Only an admin or ops user may dismiss"}},
			qerrors.ExitUnauthorized, "dismissing needs an admin or ops role"},
		{"approve without permission", "approve", approveURL, nil, http.StatusForbidden,
			map[string]any{"detail": map[string]any{"msg": "denied"}},
			qerrors.ExitUnauthorized, "DATA_REVIEW_APPROVE permission"},
		{"approve not designated", "approve", approveURL, nil, http.StatusForbidden,
			apiError("APPROVER_NOT_DESIGNATED", "not designated", nil),
			qerrors.ExitUnauthorized, "get data-review-policy --table-id 42"},
		{"disagree ended", "disagree", disagreURL, []string{"--reason", "x"}, http.StatusConflict,
			apiError("DUPLICATE_GROUP_ENDED", "ended", nil), qerrors.ExitBadUsage, "takes no decision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.group(groupJSON("repeated_key", pending("allow")))
			e.respond("POST", tc.path, tc.status, tc.body)
			_, _, err := e.run(append([]string{tc.verb, groupID, "--table-id", "42", "--yes"}, tc.args...)...)
			assertExit(t, err, tc.exit)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err lacks %q:\n%v", tc.want, err)
			}
		})
	}
}

func TestSettleNote(t *testing.T) {
	replaced := uuid.MustParse(carriedID)
	two := 2
	effective := &duplicate_groups.Decision{ID: replaced, Kind: duplicate_groups.KindAllow, AllowedCount: &two}
	cases := []struct {
		name  string
		group duplicate_groups.Group
		p     duplicate_groups.Decision
		want  string
	}{
		{"nothing governs", duplicate_groups.Group{}, duplicate_groups.Decision{}, " The group returns to undecided."},
		{"a change keeps the decision it replaces", duplicate_groups.Group{Decision: effective},
			duplicate_groups.Decision{ReplacesDecisionID: &replaced},
			" The group keeps its effective allow 2 [d: " + carriedID + "]."},
		{"a stop keeps an earlier series allow", duplicate_groups.Group{EarlierDecisions: []duplicate_groups.Decision{*effective}},
			duplicate_groups.Decision{Kind: duplicate_groups.KindStopSeries, ReplacesDecisionID: &replaced},
			" The group keeps its effective allow 2 [d: " + carriedID + "]."},
		{"a carried allow governs", duplicate_groups.Group{CarriedDecision: effective}, duplicate_groups.Decision{},
			" The group keeps its effective allow 2 [d: " + carriedID + "]."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := settleNote(&tc.group, &tc.p); got != tc.want {
				t.Errorf("settleNote = %q, want %q", got, tc.want)
			}
		})
	}
}

func seriesAllowGroup(g map[string]any) {
	g["carried_decision"] = decisionJSON(carriedID, "allow", "effective", func(d map[string]any) {
		d["carried"] = true
		d["scope"] = "series"
		d["period_key"] = "2026-09"
	})
	g["caller"].(map[string]any)["stop_for_series_decision_id"] = carriedID
}

func stopDecision(state string) map[string]any {
	return decisionJSON(otherID, "stop_series", state, func(d map[string]any) {
		d["scope"] = "series"
		d["replaces_decision_id"] = carriedID
	})
}

func TestStopSeries(t *testing.T) {
	cases := []struct {
		state string
		want  string
		other string
	}{
		{"pending", "Waiting for 1 more approval(s)", "no longer applies"},
		{"enacted", "The series allow [d: " + carriedID + "] no longer applies in any period", "Waiting for"},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			e := newEnv(t)
			e.group(groupJSON("repeated_key", seriesAllowGroup))
			e.respond("POST", stopURL, http.StatusOK, decisionResponse(stopDecision(tc.state)))
			out, _, err := e.run("stop-series", groupID, "--table-id", "42", "--comment", "one month only", "--yes")
			assertExit(t, err, 0)
			e.assertRequests("GET "+groupPath, "POST "+stopURL)
			want := map[string]any{"decision_id": carriedID, "comment": "one month only"}
			if got := e.postBody(stopURL); !reflect.DeepEqual(got, want) {
				t.Errorf("body = %v, want %v", got, want)
			}
			for _, w := range []string{"duplicate-group/" + groupID + " proposed stop-series [d: " + otherID + "] (decision: " + tc.state,
				tc.want} {
				if !strings.Contains(out, w) {
					t.Errorf("stdout lacks %q:\n%s", w, out)
				}
			}
			for _, absent := range []string{tc.other, "scope: series"} {
				if strings.Contains(out, absent) {
					t.Errorf("stdout carries %q:\n%s", absent, out)
				}
			}
		})
	}
}

func TestStopSeriesRefusesLocally(t *testing.T) {
	cases := map[string]func(map[string]any){
		"no series allow to stop": func(map[string]any) {},
		"a pending decision": func(g map[string]any) {
			seriesAllowGroup(g)
			pending("allow")(g)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.group(groupJSON("repeated_key", mutate))
			_, _, err := e.run("stop-series", groupID, "--table-id", "42", "--yes")
			assertExit(t, err, qerrors.ExitBadUsage)
			e.assertRequests("GET " + groupPath)
		})
	}
}

func TestStopSeriesRefusalHints(t *testing.T) {
	cases := []struct {
		code   string
		status int
		exit   int
		want   string
	}{
		{"DUPLICATE_DECISION_NOT_SERIES_ALLOW", http.StatusConflict, qerrors.ExitBadUsage, "no effective series allow it may stop"},
		{"DUPLICATE_DECISION_PENDING", http.StatusConflict, qerrors.ExitBadUsage, "withdrawn or dismissed first"},
		{"DUPLICATE_DECISION_NOT_LIVE", http.StatusConflict, qerrors.ExitBadUsage, "the series allow no longer governs the group"},
		{"DUPLICATE_DECISION_REVIEWER_EXCLUDED", http.StatusForbidden, qerrors.ExitUnauthorized, "another reviewer must"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			e := newEnv(t)
			e.group(groupJSON("repeated_key", seriesAllowGroup))
			e.respond("POST", stopURL, tc.status, apiError(tc.code, "refused", nil))
			_, _, err := e.run("stop-series", groupID, "--table-id", "42", "--yes")
			assertExit(t, err, tc.exit)
			if err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err lacks %q:\n%v", tc.want, err)
			}
		})
	}
}

// A pending stop-series proposal is approved through the per-decision route,
// since the decision route cannot send that kind.
func TestApproveAPendingStopSeries(t *testing.T) {
	e := newEnv(t)
	e.group(groupJSON("repeated_key", seriesAllowGroup, pending("stop_series", func(d map[string]any) {
		d["scope"] = "series"
		d["replaces_decision_id"] = carriedID
	})))
	enacted := stopDecision("enacted")
	enacted["id"] = decisionID
	e.respond("POST", approveURL, http.StatusOK, decisionResponse(enacted))
	out, _, err := e.run("approve", groupID, "--table-id", "42", "--yes")
	assertExit(t, err, 0)
	e.assertRequests("GET "+groupPath, "POST "+approveURL)
	for _, want := range []string{"approved stop-series [d: " + decisionID + "] (decision: enacted",
		"The series allow [d: " + carriedID + "] no longer applies in any period"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
}
