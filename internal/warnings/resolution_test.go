package warnings

import (
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

func TestParseResolutionRejectsFractionalIntegers(t *testing.T) {
	for _, data := range []string{"spec: {ids: [2.7]}", `{"spec":{"ids":[2.7]}}`} {
		_, err := ParseResolution([]byte(data))
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "ids") {
			t.Errorf("exit = %d, error = %v; want exit 2 naming ids", code, err)
		}
	}
}

func TestParseResolutionReadsASpec(t *testing.T) {
	doc := []byte("apiVersion: qluster.ai/v1\nkind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  new_value: \"1420.00\"\n")
	got, err := ParseResolution(doc)
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if got.Kind != WarningResolutionKind {
		t.Errorf("Kind = %q, want %q", got.Kind, WarningResolutionKind)
	}
	id, err := got.WarningID()
	if err != nil || id != 55 {
		t.Errorf("WarningID = %d, %v; want 55, nil", id, err)
	}
	if got.Spec.NewValue == nil || *got.Spec.NewValue != "1420.00" {
		t.Errorf("new_value = %v", got.Spec.NewValue)
	}
}

func TestWarningIDRequiresAPositiveInteger(t *testing.T) {
	for _, id := range []string{"", "fifty-five", "0", "-1"} {
		t.Run(id, func(t *testing.T) {
			_, err := (&WarningResolution{Metadata: pkgmanifest.Metadata{ID: id}}).WarningID()
			if err == nil {
				t.Fatal("want an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
		})
	}
}

// The warning wire schema has no sheet_name, so the manifest must not offer one.
func TestWarningResolutionRejectsAnAlertOnlyKey(t *testing.T) {
	_, err := ParseResolution([]byte("kind: WarningResolution\nmetadata:\n  id: 55\nspec:\n  action: map_value\n  sheet_name: Q3\n"))
	if err == nil {
		t.Fatal("want an error: sheet_name is not on WarningsResolveSchema")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestWarningValidateRejectsAMissingRequiredField(t *testing.T) {
	err := WarningResolutionSpec{Action: "map_value"}.Validate()
	if err == nil {
		t.Fatal("want an error: map_value requires new_value")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "new_value") {
		t.Errorf("message must name new_value, got %q", err)
	}
}

func TestWarningToRequestCopiesSelectors(t *testing.T) {
	spec := WarningResolutionSpec{Action: "overwrite_value", NewValue: strPtrForTest("x"), IDs: []int{7, 8}}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	req := spec.ToRequest()
	if req.Ids == nil || !reflect.DeepEqual(*req.Ids, []int{7, 8}) {
		t.Errorf("ids = %v", req.Ids)
	}
	if req.Action == nil || *req.Action != api.WarningAction("overwrite_value") {
		t.Errorf("action = %v", req.Action)
	}
}

func TestEveryWarningResolveFieldIsMappedOrDenied(t *testing.T) {
	mapped := map[string]bool{}
	st := reflect.TypeOf(WarningResolutionSpec{})
	for i := 0; i < st.NumField(); i++ {
		if name := jsonNameForTest(st.Field(i).Tag.Get("yaml")); name != "" {
			mapped[name] = true
		}
	}
	wire := reflect.TypeOf(api.WarningsResolveSchema{})
	for i := 0; i < wire.NumField(); i++ {
		name := jsonNameForTest(wire.Field(i).Tag.Get("json"))
		if name == "" || mapped[name] || warningResolutionFieldDenylist[name] {
			continue
		}
		t.Errorf("WarningsResolveSchema.%s (%s) is neither mapped by WarningResolutionSpec nor "+
			"in warningResolutionFieldDenylist: no manifest can supply it.", wire.Field(i).Name, name)
	}
}

func TestEveryWarningRequiredFieldIsSuppliable(t *testing.T) {
	mapped := map[string]bool{}
	st := reflect.TypeOf(WarningResolutionSpec{})
	for i := 0; i < st.NumField(); i++ {
		if name := jsonNameForTest(st.Field(i).Tag.Get("yaml")); name != "" {
			mapped[name] = true
		}
	}
	for action, fields := range api.WarningActionFields {
		names := append([]string{}, fields.Required...)
		for _, group := range fields.OneOf {
			names = append(names, group...)
		}
		names = append(names, fields.Optional...)
		for _, name := range names {
			if !mapped[name] {
				t.Errorf("action %q reads %q, which WarningResolutionSpec cannot supply", action, name)
			}
		}
	}
}

func jsonNameForTest(tag string) string {
	if tag == "" || tag == "-" {
		return ""
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	return tag
}

func strPtrForTest(s string) *string { return &s }

// The skeleton must be re-readable by ParseResolution, or the loop it
// advertises is broken. Its required keys are emitted BARE so an unedited
// skeleton is refused by Validate rather than silently sent.
func TestSkeletonRoundTripsAndRefusesUnedited(t *testing.T) {
	w := &WarningFull{ID: 55, DatasetID: 42, DatasetName: "orders"}
	out := Skeleton(w, "map_value")
	res, err := ParseResolution([]byte(out))
	if err != nil {
		t.Fatalf("the skeleton does not parse: %v\n%s", err, out)
	}
	if res.Kind != WarningResolutionKind || res.Metadata.ID != "55" {
		t.Errorf("skeleton envelope = %q/%q", res.Kind, res.Metadata.ID)
	}
	if res.Spec.NewValue != nil {
		t.Errorf("required keys must be emitted bare, got new_value = %q", *res.Spec.NewValue)
	}
	if err := res.Spec.Validate(); err == nil {
		t.Error("an unedited skeleton must be refused, not sent")
	}
}

// The action's backend note is prose qctl never enforces, so it is shown.
func TestSkeletonCarriesTheActionsNote(t *testing.T) {
	out := Skeleton(&WarningFull{ID: 55}, "overwrite_value")
	if !strings.Contains(out, "Needs a row selector") {
		t.Errorf("the skeleton should carry the action's note, got:\n%s", out)
	}
}

// hostileCellValues mirrors the alert side's table, and for the same reason:
// every entry is something a quoted CSV cell can hold, and each one broke the
// generated skeleton before pkgmanifest.CommentSafe was applied.
var hostileCellValues = []struct {
	name  string
	value string
}{
	{"embedded newline", "1420\nUSD"},
	{"crlf", "1420\r\nUSD"},
	{"lone carriage return", "1420\rUSD"},
	{"leading document marker", "---\nkind: Evil"},
	{"comment character", "# not really a comment"},
	{"nul byte", "1420\x00USD"},
	{"unicode line separator", "1420\u2028USD"},
	// The injection the whole guard exists for: without it this renders as TWO
	// documents and the first one resolves warning 77 with map_value.
	{"a whole yaml document", "x\n---\napiVersion: qluster.ai/v1\nkind: WarningResolution\n" +
		"metadata:\n  id: \"77\"\nspec:\n  action: map_value\n  new_value: pwned\n---"},
}

// A cell can hold anything, so no cell value may escape the comment line it is
// written on: one document out, the document the operator asked for, still
// refused unedited, and the value still readable.
func TestSkeletonKeepsHostileCellDataInsideItsComment(t *testing.T) {
	for _, tc := range hostileCellValues {
		t.Run(tc.name, func(t *testing.T) {
			w := &WarningFull{ID: 55, DatasetID: 42, DatasetName: "orders", FieldValue: strPtrForTest(tc.value)}
			out := Skeleton(w, "map_value")

			docs, err := pkgmanifest.SplitDocuments([]byte(out))
			if err != nil {
				t.Fatalf("the skeleton does not split: %v\n%s", err, out)
			}
			if len(docs) != 1 {
				t.Fatalf("got %d documents, want exactly 1:\n%s", len(docs), out)
			}
			res, err := ParseResolution(docs[0])
			if err != nil {
				t.Fatalf("the skeleton does not parse: %v\n%s", err, out)
			}
			if res.Kind != WarningResolutionKind || res.Metadata.ID != "55" || res.Spec.Action != "map_value" {
				t.Fatalf("skeleton resolves %q/%q/%q, want WarningResolution/55/map_value:\n%s",
					res.Kind, res.Metadata.ID, res.Spec.Action, out)
			}
			if err := res.Spec.Validate(); err == nil {
				t.Errorf("an unedited skeleton must still be refused:\n%s", out)
			}
			if got, ok := recoverSkeletonFact(out, "field_value"); !ok || got != tc.value {
				t.Errorf("field_value must stay legible: recovered %q (found=%v), want %q\n%s",
					got, ok, tc.value, out)
			}
		})
	}
}

// The dataset name is cell-adjacent customer text on the header line, and it
// had the same exposure as the facts below it.
func TestSkeletonKeepsAHostileDatasetNameInsideItsComment(t *testing.T) {
	for _, tc := range hostileCellValues {
		t.Run(tc.name, func(t *testing.T) {
			out := Skeleton(&WarningFull{ID: 55, DatasetID: 42, DatasetName: tc.value}, "map_value")
			docs, err := pkgmanifest.SplitDocuments([]byte(out))
			if err != nil {
				t.Fatalf("the skeleton does not split: %v\n%s", err, out)
			}
			if len(docs) != 1 {
				t.Fatalf("got %d documents, want exactly 1:\n%s", len(docs), out)
			}
			res, err := ParseResolution(docs[0])
			if err != nil {
				t.Fatalf("the skeleton does not parse: %v\n%s", err, out)
			}
			if res.Kind != WarningResolutionKind || res.Metadata.ID != "55" {
				t.Fatalf("skeleton envelope = %q/%q, want WarningResolution/55:\n%s", res.Kind, res.Metadata.ID, out)
			}
			// The header is the FIRST line, and it is still one line.
			header := strings.SplitN(out, "\n", 2)[0]
			if !strings.HasPrefix(header, "# warning 55 on table ") || !strings.HasSuffix(header, " (id 42)") {
				t.Errorf("header line = %q, want the table name inside it", header)
			}
			name := strings.TrimSuffix(strings.TrimPrefix(header, "# warning 55 on table "), " (id 42)")
			if got, ok := unescapeSkeletonValue(name); !ok || got != tc.value {
				t.Errorf("the table name must stay legible: recovered %q (found=%v), want %q", got, ok, tc.value)
			}
		})
	}
}

// recoverSkeletonFact reads one "# <name>: <value>" comment back out of a
// skeleton. It exists to prove the guard did not silently DROP information:
// every byte of the cell has to still be there.
func recoverSkeletonFact(skeleton, name string) (string, bool) {
	prefix := "# " + name + ": "
	for _, line := range strings.Split(skeleton, "\n") {
		if strings.HasPrefix(line, prefix) {
			return unescapeSkeletonValue(strings.TrimPrefix(line, prefix))
		}
	}
	return "", false
}

// unescapeSkeletonValue reverses pkgmanifest.CommentSafe. A value with nothing
// to escape is printed raw, so both forms are accepted; a raw value that
// happens to look like a Go quoted string is why this returns the unquoted form
// only when unquoting succeeds.
func unescapeSkeletonValue(rendered string) (string, bool) {
	if unquoted, err := strconv.Unquote(rendered); err == nil {
		return unquoted, true
	}
	return rendered, true
}

// mapValueActions is every warning action whose name carries map_value: the
// eight whose notes atlas #1059 rewrote.
func mapValueActions(t *testing.T) []string {
	t.Helper()
	var actions []string
	for action := range api.WarningActionFields {
		if strings.Contains(action, "map_value") {
			actions = append(actions, action)
		}
	}
	sort.Strings(actions)
	if len(actions) == 0 {
		t.Fatal("fixture drift: api.WarningActionFields names no map_value action")
	}
	return actions
}

// map_value_once is refused on presence whatever its value and whatever the
// action: an otherwise-complete map_value manifest carrying it must fail, and
// so must one where it is false, or one setting it on an action that never
// read it.
//
// qctl refused it by hand until atlas #1059 dropped it from
// x-warning-action-fields; actionfields now refuses it as an unread key. This
// test is deliberately unchanged in WHAT it asserts, because the behaviour is
// what had to survive deleting the override -- only the wording it expects
// moved, from qctl's own sentence to the generic unread-field message.
func TestValidateRefusesMapValueOnceOnEveryAction(t *testing.T) {
	var actions []string
	for action := range api.WarningActionFields {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		for _, once := range []bool{true, false} {
			t.Run(action+"/"+strconv.FormatBool(once), func(t *testing.T) {
				spec := WarningResolutionSpec{
					Action:       action,
					NewValue:     strPtrForTest("x"),
					IDs:          []int{7},
					MapValueOnce: &once,
				}
				err := spec.Validate()
				if err == nil {
					t.Fatal("want a refusal: map_value_once is alert-only")
				}
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
				}
				if !strings.Contains(err.Error(), "spec.map_value_once") {
					t.Errorf("message must name spec.map_value_once, got %q", err)
				}
			})
		}
	}
}

// Leaving map_value_once out must still validate: the refusal is about the key,
// not about the action.
func TestValidateAcceptsAMapValueManifestWithoutMapValueOnce(t *testing.T) {
	for _, action := range mapValueActions(t) {
		if err := (WarningResolutionSpec{Action: action, NewValue: strPtrForTest("x")}).Validate(); err != nil {
			t.Errorf("%s: %v", action, err)
		}
	}
}

// The "this action reads:" line of every other refusal must not offer the key
// the warning refuses.
func TestValidateDoesNotListMapValueOnceAsRead(t *testing.T) {
	err := WarningResolutionSpec{Action: "map_value"}.Validate()
	if err == nil {
		t.Fatal("want an error: map_value requires new_value")
	}
	if strings.Contains(err.Error(), "map_value_once") {
		t.Errorf("a warning refusal must not list map_value_once as read, got %q", err)
	}
}

// No map_value action's skeleton offers map_value_once as a key, and each one
// still carries the backend's own note -- which from atlas #1059 is the thing
// that tells the reader why, so qctl no longer prints a correction under it.
func TestSkeletonNeverOffersMapValueOnce(t *testing.T) {
	for _, action := range mapValueActions(t) {
		t.Run(action, func(t *testing.T) {
			out := Skeleton(&WarningFull{ID: 55, FieldName: strPtrForTest("amount")}, action)
			for _, line := range strings.Split(out, "\n") {
				key := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
				if strings.HasPrefix(key, "map_value_once:") {
					t.Errorf("the skeleton offers map_value_once on line %q:\n%s", line, out)
				}
			}
			if !strings.Contains(out, "# another_field_new_value:") {
				t.Errorf("the other optional keys must survive the filter:\n%s", out)
			}
			note := api.WarningActionFields[action].Note
			if note == "" {
				t.Fatalf("fixture drift: the %s action has no backend note left to print", action)
			}
			if !strings.Contains(out, "# note from the backend: "+pkgmanifest.CommentSafe(note)+"\n") {
				t.Errorf("the skeleton must carry the backend's note:\n%s", out)
			}
			if strings.Contains(out, "correction from qctl") {
				t.Errorf("nothing is left to correct: atlas #1059 fixed the note itself:\n%s", out)
			}
		})
	}
}

// TestWarningActionFieldsListNoAlertOnlyField is the INVERSE of the tripwire
// atlas #1059 retired.
//
// qctl used to refuse map_value_once from a hand-written list, because
// x-warning-action-fields listed it for all eight map_value actions although
// every warning route rejected it (atlas-uhe2). #1059 dropped it, so
// actionfields refuses it as an unread key and the list was deleted -- which
// means the refusal now rests ENTIRELY on the generated table. A regen that put
// the field back would silently start sending it, and the user would get the
// backend's 422 instead of qctl's exit 2.
//
// The field stays on WarningResolutionSpec either way: the wire schema still
// carries map_value_once, and TestEveryWarningResolveFieldIsMappedOrDenied
// wants every wire field mapped or denied.
func TestWarningActionFieldsListNoAlertOnlyField(t *testing.T) {
	for action, fields := range api.WarningActionFields {
		if fields.Accepts("map_value_once") {
			t.Errorf("api.WarningActionFields lists map_value_once for %q again, so nothing refuses it "+
				"before the wire: atlas has regressed #1059 (atlas-uhe2). Restore the override -- a "+
				"refusal in WarningResolutionSpec.Validate keyed on the field's presence, whatever the "+
				"action -- and delete this test.", action)
		}
	}
}

// prefixCandidatesForTest is a body's prefix_candidates list, in the order the
// skeleton must keep.
func prefixCandidatesForTest() *[]string { return &[]string{"0614141", "061414", "06141"} }

// gtinPrefixWarningForTest carries all four facts, so a test can see where the
// prefix_candidates line lands among them.
func gtinPrefixWarningForTest(candidates *[]string) *WarningFull {
	return &WarningFull{
		ID: 55, DatasetID: 42, DatasetName: "orders",
		FieldName: strPtrForTest("upc"), FieldValue: strPtrForTest("00614141999996"),
		AnotherFieldName: strPtrForTest("sku"), AnotherFieldValue: strPtrForTest("A-100"),
		Body: &api.AlertBodySchema{PrefixCandidates: candidates},
	}
}

// lineIndexForTest is the index of the first line starting with prefix, or -1.
func lineIndexForTest(lines []string, prefix string) int {
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return i
		}
	}
	return -1
}

// skeletonLineCountForTest counts lines the way YAML breaks them: on LF, CR,
// NEL, LINE SEPARATOR and PARAGRAPH SEPARATOR, a CRLF counting once. A
// skeleton has no blank lines, so a count of non-empty runs is its line count.
func skeletonLineCountForTest(s string) int {
	return len(strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '\n', '\r', '\u0085', '\u2028', '\u2029':
			return true
		}
		return false
	}))
}

// accept_gtin_prefix's new_value must be one of the body's prefix_candidates,
// so the skeleton lists them as the last fact, ahead of the instructions, and
// the listing makes nothing acceptable unedited.
func TestSkeletonListsThePrefixCandidates(t *testing.T) {
	out := Skeleton(gtinPrefixWarningForTest(prefixCandidatesForTest()), "accept_gtin_prefix")
	lines := strings.Split(out, "\n")

	got := lineIndexForTest(lines, "# prefix_candidates")
	if got < 0 || lines[got] != "# prefix_candidates: 0614141, 061414, 06141" {
		t.Fatalf("want the line %q, got:\n%s", "# prefix_candidates: 0614141, 061414, 06141", out)
	}
	if n := strings.Count(out, "\n# prefix_candidates"); n != 1 {
		t.Errorf("want exactly one prefix_candidates line, got %d:\n%s", n, out)
	}
	if prev := lineIndexForTest(lines, "# another_field_value: "); prev < 0 || got != prev+1 {
		t.Errorf("prefix_candidates must follow another_field_value directly (at %d, fact at %d):\n%s", got, prev, out)
	}
	if fill := lineIndexForTest(lines, "# Fill in every key below"); fill < 0 || got > fill {
		t.Errorf("prefix_candidates must precede the instructions (at %d, instructions at %d):\n%s", got, fill, out)
	}

	res, err := ParseResolution([]byte(out))
	if err != nil {
		t.Fatalf("the skeleton does not parse: %v\n%s", err, out)
	}
	if err := res.Spec.Validate(); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("an unedited skeleton must be refused with exit 2, got %v (exit %d)", err, qerrors.GetExitCode(err))
	}
}

// No candidates, no line: a nil body, an absent list and an empty one all
// render exactly the skeleton of a warning with no body at all.
func TestSkeletonOmitsAbsentPrefixCandidates(t *testing.T) {
	bare := gtinPrefixWarningForTest(nil)
	bare.Body = nil
	want := Skeleton(bare, "accept_gtin_prefix")
	for _, tc := range []struct {
		name string
		body *api.AlertBodySchema
	}{
		{"nil body", nil},
		{"nil list", &api.AlertBodySchema{}},
		{"empty list", &api.AlertBodySchema{PrefixCandidates: &[]string{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := gtinPrefixWarningForTest(nil)
			w.Body = tc.body
			out := Skeleton(w, "accept_gtin_prefix")
			if i := lineIndexForTest(strings.Split(out, "\n"), "# prefix_candidates"); i >= 0 {
				t.Errorf("no candidates must mean no line, got:\n%s", out)
			}
			if out != want {
				t.Errorf("skeleton differs from the bodiless one:\n got:\n%s\nwant:\n%s", out, want)
			}
		})
	}
}

// A candidate is derived from cell data, so it gets the same guard as the
// facts: one document out, no extra lines, and the candidate still readable.
func TestSkeletonKeepsAHostilePrefixCandidateOnItsLine(t *testing.T) {
	wantLines := skeletonLineCountForTest(Skeleton(gtinPrefixWarningForTest(prefixCandidatesForTest()), "accept_gtin_prefix"))
	for _, tc := range hostileCellValues {
		t.Run(tc.name, func(t *testing.T) {
			out := Skeleton(gtinPrefixWarningForTest(&[]string{"0614141", tc.value, "06141"}), "accept_gtin_prefix")

			if got := skeletonLineCountForTest(out); got != wantLines {
				t.Errorf("got %d lines, want %d:\n%s", got, wantLines, out)
			}
			docs, err := pkgmanifest.SplitDocuments([]byte(out))
			if err != nil {
				t.Fatalf("the skeleton does not split: %v\n%s", err, out)
			}
			if len(docs) != 1 {
				t.Fatalf("got %d documents, want exactly 1:\n%s", len(docs), out)
			}
			res, err := ParseResolution(docs[0])
			if err != nil {
				t.Fatalf("the skeleton does not parse: %v\n%s", err, out)
			}
			if res.Kind != WarningResolutionKind || res.Metadata.ID != "55" || res.Spec.Action != "accept_gtin_prefix" {
				t.Fatalf("skeleton resolves %q/%q/%q, want WarningResolution/55/accept_gtin_prefix:\n%s",
					res.Kind, res.Metadata.ID, res.Spec.Action, out)
			}
			if err := res.Spec.Validate(); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("an unedited skeleton must still be refused with exit 2, got %v:\n%s", err, out)
			}
			list, ok := recoverSkeletonFact(out, "prefix_candidates")
			if !ok || !strings.HasPrefix(list, "0614141, ") || !strings.HasSuffix(list, ", 06141") {
				t.Fatalf("prefix_candidates line = %q (found=%v), want it bracketed by the clean candidates", list, ok)
			}
			// recoverSkeletonFact's unquote fails on the whole list, so it hands
			// the raw line back; unescape the middle candidate on its own.
			middle := strings.TrimSuffix(strings.TrimPrefix(list, "0614141, "), ", 06141")
			if got, _ := unescapeSkeletonValue(middle); got != tc.value {
				t.Errorf("the candidate must stay legible: recovered %q, want %q", got, tc.value)
			}
		})
	}
}

// The loop the listing advertises: a user pastes a candidate off the comment
// line UNQUOTED into the bare new_value key. It has a leading zero, so it only
// survives if new_value decodes as the scalar's text rather than as a number.
func TestSkeletonAcceptsAPastedPrefixCandidate(t *testing.T) {
	if !slices.Contains(api.WarningActionFields["accept_gtin_prefix"].Required, "new_value") {
		t.Fatalf("api.WarningActionFields no longer requires new_value for accept_gtin_prefix: %+v",
			api.WarningActionFields["accept_gtin_prefix"])
	}
	out := Skeleton(gtinPrefixWarningForTest(prefixCandidatesForTest()), "accept_gtin_prefix")
	const bare = "\n  new_value:\n"
	if n := strings.Count(out, bare); n != 1 {
		t.Fatalf("want exactly one bare new_value key, got %d:\n%s", n, out)
	}
	edited := strings.Replace(out, bare, "\n  new_value: 0614141\n", 1)

	docs, err := pkgmanifest.SplitDocuments([]byte(edited))
	if err != nil || len(docs) != 1 {
		t.Fatalf("the edited skeleton splits into %d documents (err %v):\n%s", len(docs), err, edited)
	}
	res, err := ParseResolution(docs[0])
	if err != nil {
		t.Fatalf("the edited skeleton does not parse: %v\n%s", err, edited)
	}
	if res.Spec.NewValue == nil || *res.Spec.NewValue != "0614141" {
		t.Fatalf("new_value = %v, want the string \"0614141\" with its leading zero", res.Spec.NewValue)
	}
	if err := res.Spec.Validate(); err != nil {
		t.Errorf("the edited skeleton must validate, got %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	if req := res.Spec.ToRequest(); req.NewValue == nil || *req.NewValue != "0614141" {
		t.Errorf("request new_value = %v, want \"0614141\"", req.NewValue)
	}
}

// The warning skeleton names its masked facts exactly as the alert one does.
func TestSkeletonNamesTheMaskedFacts(t *testing.T) {
	w := gtinPrefixWarningForTest(&[]string{"•••••"})
	w.MaskedFields = &[]string{"upc"}
	out := Skeleton(w, "accept_gtin_prefix")
	want := "# masked for you: field_value, prefix_candidates (display text, not the stored value; a PII column refuses it as a value)"
	lines := strings.Split(out, "\n")
	got := lineIndexForTest(lines, "# masked for you")
	if got < 0 || lines[got] != want {
		t.Fatalf("want the line %q, got:\n%s", want, out)
	}
	if _, err := ParseResolution([]byte(out)); err != nil {
		t.Errorf("the skeleton does not parse: %v\n%s", err, out)
	}

	w.MaskedFields = nil
	if out := Skeleton(w, "accept_gtin_prefix"); strings.Contains(out, "# masked for you") {
		t.Errorf("an unmasked warning's skeleton must not carry the line:\n%s", out)
	}
}
