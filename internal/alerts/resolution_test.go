package alerts

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

func TestParseResolutionRejectsFractionalIntegers(t *testing.T) {
	for _, tc := range []struct {
		data string
		path string
	}{
		{"spec: {ids: [2.7]}", "spec.ids[0]"},
		{"spec: {cant_sniff_header_params: {header_line_number_for_file: 2.7}}", "spec.cant_sniff_header_params.header_line_number_for_file"},
		{`{"spec":{"ids":[2.7]}}`, "ids"},
	} {
		_, err := ParseResolution([]byte(tc.data))
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), tc.path) {
			t.Errorf("exit = %d, error = %v; want exit 2 naming %s", code, err, tc.path)
		}
	}
}

func TestParseResolutionReadsASpec(t *testing.T) {
	doc := []byte(`
apiVersion: qluster.ai/v1
kind: AlertResolution
metadata:
  id: 9
spec:
  action: map_value
  new_value: "1420.00"
  map_value_once: true
`)
	got, err := ParseResolution(doc)
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if got.Kind != AlertResolutionKind {
		t.Errorf("Kind = %q, want %q", got.Kind, AlertResolutionKind)
	}
	if got.Metadata.ID != "9" {
		t.Errorf("metadata.id = %q, want \"9\"", got.Metadata.ID)
	}
	if got.Spec.Action != "map_value" {
		t.Errorf("action = %q", got.Spec.Action)
	}
	if got.Spec.NewValue == nil || *got.Spec.NewValue != "1420.00" {
		t.Errorf("new_value = %v", got.Spec.NewValue)
	}
	if got.Spec.MapValueOnce == nil || !*got.Spec.MapValueOnce {
		t.Errorf("map_value_once = %v", got.Spec.MapValueOnce)
	}
}

func TestAlertIDRequiresAPositiveInteger(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want int
	}{
		{name: "valid", id: "9", want: 9},
		{name: "missing"},
		{name: "not an integer", id: "nine"},
		{name: "zero", id: "0"},
		{name: "negative", id: "-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&AlertResolution{Metadata: metadataForResolutionTest(tt.id)}).AlertID()
			if tt.want > 0 {
				if err != nil || got != tt.want {
					t.Fatalf("AlertID = %d, %v; want %d, nil", got, err, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatal("want an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
		})
	}
}

// An unknown key is a typo, and StrictUnmarshal's KnownFields(true) is what
// catches it. Exit 2, because it refuses something the user wrote.
func TestParseResolutionRejectsAnUnknownSpecKey(t *testing.T) {
	doc := []byte("apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: map_value\n  new_valu: x\n")
	_, err := ParseResolution(doc)
	if err == nil {
		t.Fatal("want an error for an unknown spec key")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// An omitted new_value and an explicitly empty one are DIFFERENT requests:
// _handle_composite_conflict_reapply (atlas alert_controller.py) branches on
// `request_body.new_value is not None` to tell "confirm the existing override"
// from "submit a value", including the explicit-empty sentinel.
func TestNewValueDistinguishesOmittedFromExplicitlyEmpty(t *testing.T) {
	omitted, err := ParseResolution([]byte("kind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: reapply_cell_override\n  ids: [7]\n"))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if omitted.Spec.ToRequest().NewValue != nil {
		t.Error("an omitted new_value must reach the wire as nil")
	}
	empty, err := ParseResolution([]byte("kind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: reapply_cell_override\n  ids: [7]\n  new_value: \"\"\n"))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	got := empty.Spec.ToRequest().NewValue
	if got == nil || *got != "" {
		t.Errorf("an explicit empty new_value must reach the wire as a pointer to \"\", got %v", got)
	}
}

func TestPresentFieldsTreatsAnEmptyListAsAbsent(t *testing.T) {
	spec := AlertResolutionSpec{Action: "overwrite_value", NewValue: strPtrForTest("x"), IDs: []int{}}
	if spec.PresentFields()["ids"] {
		t.Error("ids: [] selects no rows and must not count as present")
	}
	if err := spec.Validate(); err == nil {
		t.Error("want a refusal: overwrite_value needs a row selector")
	}
}

// An object present but with every sub-field empty sets nothing, which the
// backend answers with a 400 ("You may want to set the scope." / a body that
// sets nothing at all). Refusing it here costs no round trip.
func TestPresentFieldsTreatsAnEmptySniffObjectAsAbsent(t *testing.T) {
	spec := AlertResolutionSpec{Action: "resolve_sniff_header", CantSniffHeaderParams: &CantSniffHeaderParamsSpec{}}
	if spec.PresentFields()["cant_sniff_header_params"] {
		t.Error("an all-empty cant_sniff_header_params sets nothing and must not count as present")
	}
}

func TestValidateAcceptsAWellFormedSniffResolution(t *testing.T) {
	scope := "data_source_model"
	spec := AlertResolutionSpec{
		Action:                "resolve_sniff_header",
		Scope:                 &scope,
		CantSniffHeaderParams: &CantSniffHeaderParamsSpec{RawHeaders: []string{"id", "amount"}},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	req := spec.ToRequest()
	if req.CantSniffHeaderParams == nil || req.CantSniffHeaderParams.RawHeaders == nil {
		t.Fatal("raw_headers must reach the wire")
	}
	if got := *req.CantSniffHeaderParams.RawHeaders; !reflect.DeepEqual(got, []string{"id", "amount"}) {
		t.Errorf("raw_headers = %v", got)
	}
	if req.Scope == nil || *req.Scope != api.Scope("data_source_model") {
		t.Errorf("scope = %v", req.Scope)
	}
}

// TestEveryResolveFieldIsMappedOrDenied is the tripwire. A regenerated
// client.gen.go that adds a field to AlertResolveSchema must fail here rather
// than leave it quietly unsendable — the same guard get data has in
// TestNoParamIsSilentlyUnsupported (internal/commands/get/data_flags_test.go).
func TestEveryResolveFieldIsMappedOrDenied(t *testing.T) {
	mapped := map[string]bool{}
	st := reflect.TypeOf(AlertResolutionSpec{})
	for i := 0; i < st.NumField(); i++ {
		if name := jsonNameForTest(st.Field(i).Tag.Get("yaml")); name != "" {
			mapped[name] = true
		}
	}
	wire := reflect.TypeOf(api.AlertResolveSchema{})
	for i := 0; i < wire.NumField(); i++ {
		name := jsonNameForTest(wire.Field(i).Tag.Get("json"))
		if name == "" || mapped[name] || resolutionFieldDenylist[name] {
			continue
		}
		t.Errorf("AlertResolveSchema.%s (%s) is neither mapped by AlertResolutionSpec nor in "+
			"resolutionFieldDenylist: no manifest can supply it and nothing reports that.",
			wire.Field(i).Name, name)
	}
}

// TestEveryRequiredFieldIsSuppliable is the other half: a field the backend can
// REQUIRE but no manifest can set would be an exit 2 with no way out. An
// optional field resolutionFieldDenylist names is exempt.
func TestEveryRequiredFieldIsSuppliable(t *testing.T) {
	mapped := map[string]bool{}
	st := reflect.TypeOf(AlertResolutionSpec{})
	for i := 0; i < st.NumField(); i++ {
		if name := jsonNameForTest(st.Field(i).Tag.Get("yaml")); name != "" {
			mapped[name] = true
		}
	}
	for action, fields := range api.AlertActionFields {
		names := append([]string{}, fields.Required...)
		for _, group := range fields.OneOf {
			names = append(names, group...)
		}
		for _, name := range fields.Optional {
			if !resolutionFieldDenylist[name] {
				names = append(names, name)
			}
		}
		for _, name := range names {
			if !mapped[name] {
				t.Errorf("action %q reads %q, which AlertResolutionSpec cannot supply", action, name)
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

func metadataForResolutionTest(id string) pkgmanifest.Metadata { return pkgmanifest.Metadata{ID: id} }

func strPtrForTest(s string) *string { return &s }

// The skeleton must be re-readable by ParseResolution, or the loop it advertises
// is broken. Its required keys are emitted BARE (no value) so an unedited
// skeleton is refused by Validate rather than silently sent — new_value: "" is
// the explicit-empty sentinel and must never be produced by accident.
func TestSkeletonRoundTripsAndRefusesUnedited(t *testing.T) {
	a := &AlertFull{ID: 9, DatasetID: 42, DatasetName: "orders"}
	out := Skeleton(a, "map_value", nil)
	res, err := ParseResolution([]byte(out))
	if err != nil {
		t.Fatalf("the skeleton does not parse: %v\n%s", err, out)
	}
	if res.Kind != AlertResolutionKind || res.Metadata.ID != "9" {
		t.Errorf("skeleton envelope = %q/%q", res.Kind, res.Metadata.ID)
	}
	if res.Spec.NewValue != nil {
		t.Errorf("required keys must be emitted bare, got new_value = %q", *res.Spec.NewValue)
	}
	if err := res.Spec.Validate(); err == nil {
		t.Error("an unedited skeleton must be refused, not sent")
	}
}

// Optional keys are commented out, so they do not become present-but-empty.
func TestSkeletonCommentsOutOptionalKeys(t *testing.T) {
	out := Skeleton(&AlertFull{ID: 9}, "map_value", nil)
	if !strings.Contains(out, "# map_value_once:") {
		t.Errorf("optional keys should be commented out, got:\n%s", out)
	}
}

// A denylisted key is not offered, since ParseResolution refuses it.
func TestSkeletonOmitsDenylistedOptionalKeys(t *testing.T) {
	for action, fields := range api.AlertActionFields {
		out := Skeleton(&AlertFull{ID: 9}, action, nil)
		for _, name := range fields.Optional {
			if resolutionFieldDenylist[name] && strings.Contains(out, name+":") {
				t.Errorf("%s skeleton offers denylisted %s:\n%s", action, name, out)
			}
		}
	}
}

// Nothing is guessed. Prefilling new_value from field_value would produce a
// manifest that resolves NOTHING and reports success: handle_one_value_mapping
// (atlas capi/capi/controllers/map_value_controller_mixin.py) returns early
// when current_value == new_value.
func TestSkeletonQuotesTheCurrentValueInACommentNotInNewValue(t *testing.T) {
	a := &AlertFull{ID: 9, FieldName: strPtrForTest("amount"), FieldValue: strPtrForTest("1,420.00")}
	out := Skeleton(a, "map_value", nil)
	if !strings.Contains(out, "# field_value: 1,420.00") {
		t.Errorf("the current value belongs in a comment, got:\n%s", out)
	}
	if strings.Contains(out, "new_value: 1,420.00") {
		t.Error("new_value must not be prefilled from field_value")
	}
}

// The action's backend note is prose qctl never enforces, so it is shown.
func TestSkeletonCarriesTheActionsNote(t *testing.T) {
	out := Skeleton(&AlertFull{ID: 9}, "overwrite_value", nil)
	if !strings.Contains(out, "Needs a row selector") {
		t.Errorf("the skeleton should carry the action's note, got:\n%s", out)
	}
}

// hostileCellValues is the shared table for the two tests below. Every entry
// is something a quoted CSV cell can legitimately hold — an address, a
// free-text note — or something an attacker can put in one, and each one broke
// the generated skeleton before pkgmanifest.CommentSafe was applied.
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
	// documents and the first one resolves alert 77 with apply_migration.
	{"a whole yaml document", "x\n---\napiVersion: qluster.ai/v1\nkind: AlertResolution\n" +
		"metadata:\n  id: \"77\"\nspec:\n  action: apply_migration\n---"},
}

// A cell can hold anything, so no cell value may escape the comment line it is
// written on: one document out, the document the operator asked for, still
// refused unedited, and the value still readable.
func TestSkeletonKeepsHostileCellDataInsideItsComment(t *testing.T) {
	for _, tc := range hostileCellValues {
		t.Run(tc.name, func(t *testing.T) {
			a := &AlertFull{ID: 9, DatasetID: 42, DatasetName: "orders", FieldValue: strPtrForTest(tc.value)}
			out := Skeleton(a, "map_value", nil)

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
			if res.Kind != AlertResolutionKind || res.Metadata.ID != "9" || res.Spec.Action != "map_value" {
				t.Fatalf("skeleton resolves %q/%q/%q, want AlertResolution/9/map_value:\n%s",
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
			out := Skeleton(&AlertFull{ID: 9, DatasetID: 42, DatasetName: tc.value}, "map_value", nil)
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
			if res.Kind != AlertResolutionKind || res.Metadata.ID != "9" {
				t.Fatalf("skeleton envelope = %q/%q, want AlertResolution/9:\n%s", res.Kind, res.Metadata.ID, out)
			}
			// The header is the FIRST line, and it is still one line.
			header := strings.SplitN(out, "\n", 2)[0]
			if !strings.HasPrefix(header, "# alert 9 on table ") || !strings.HasSuffix(header, " (id 42)") {
				t.Errorf("header line = %q, want the table name inside it", header)
			}
			name := strings.TrimSuffix(strings.TrimPrefix(header, "# alert 9 on table "), " (id 42)")
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

// prefixCandidatesForTest is a body's prefix_candidates list, in the order the
// skeleton must keep.
func prefixCandidatesForTest() *[]string { return &[]string{"0614141", "061414", "06141"} }

// gtinPrefixAlertForTest carries all four facts, so a test can see where the
// prefix_candidates line lands among them.
func gtinPrefixAlertForTest(candidates *[]string) *AlertFull {
	return &AlertFull{
		ID: 9, DatasetID: 42, DatasetName: "orders",
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
	out := Skeleton(gtinPrefixAlertForTest(prefixCandidatesForTest()), "accept_gtin_prefix", nil)
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
// render exactly the skeleton of an alert with no body at all.
func TestSkeletonOmitsAbsentPrefixCandidates(t *testing.T) {
	bare := gtinPrefixAlertForTest(nil)
	bare.Body = nil
	want := Skeleton(bare, "accept_gtin_prefix", nil)
	for _, tc := range []struct {
		name string
		body *api.AlertBodySchema
	}{
		{"nil body", nil},
		{"nil list", &api.AlertBodySchema{}},
		{"empty list", &api.AlertBodySchema{PrefixCandidates: &[]string{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := gtinPrefixAlertForTest(nil)
			a.Body = tc.body
			out := Skeleton(a, "accept_gtin_prefix", nil)
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
	wantLines := skeletonLineCountForTest(Skeleton(gtinPrefixAlertForTest(prefixCandidatesForTest()), "accept_gtin_prefix", nil))
	for _, tc := range hostileCellValues {
		t.Run(tc.name, func(t *testing.T) {
			out := Skeleton(gtinPrefixAlertForTest(&[]string{"0614141", tc.value, "06141"}), "accept_gtin_prefix", nil)

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
			if res.Kind != AlertResolutionKind || res.Metadata.ID != "9" || res.Spec.Action != "accept_gtin_prefix" {
				t.Fatalf("skeleton resolves %q/%q/%q, want AlertResolution/9/accept_gtin_prefix:\n%s",
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
	if !slices.Contains(api.AlertActionFields["accept_gtin_prefix"].Required, "new_value") {
		t.Fatalf("api.AlertActionFields no longer requires new_value for accept_gtin_prefix: %+v",
			api.AlertActionFields["accept_gtin_prefix"])
	}
	out := Skeleton(gtinPrefixAlertForTest(prefixCandidatesForTest()), "accept_gtin_prefix", nil)
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

func TestApplyMigrationCarriesSuggestionDecisions(t *testing.T) {
	res, err := ParseResolution([]byte(`
spec:
  action: apply_migration
  accept_suggestion_ids: [22222222-2222-2222-2222-222222222222]
  dismiss_suggestion_ids: [33333333-3333-3333-3333-333333333333]
`))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if err := res.Spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	req := res.Spec.ToRequest()
	if req.AcceptSuggestionIds == nil || (*req.AcceptSuggestionIds)[0].String() != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("accept_suggestion_ids = %v", req.AcceptSuggestionIds)
	}
	if req.DismissSuggestionIds == nil || (*req.DismissSuggestionIds)[0].String() != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("dismiss_suggestion_ids = %v", req.DismissSuggestionIds)
	}
}

func TestSuggestionDecisionsAreRefusedAtExitTwo(t *testing.T) {
	for name, doc := range map[string]string{
		"not a uuid":     "spec: {action: apply_migration, accept_suggestion_ids: [nope]}",
		"in both lists":  "spec: {action: apply_migration, accept_suggestion_ids: [22222222-2222-2222-2222-222222222222], dismiss_suggestion_ids: [22222222-2222-2222-2222-222222222222]}",
		"another action": "spec: {action: disable_rule, dismiss_suggestion_ids: [22222222-2222-2222-2222-222222222222]}",
	} {
		res, err := ParseResolution([]byte(doc))
		if err == nil {
			err = res.Spec.Validate()
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s: exit = %d (%v), want 2", name, code, err)
		}
	}
}

func TestKeyColumnsOnAColumnMappingAreRefusedAtExitTwo(t *testing.T) {
	for name, doc := range map[string]string{
		"mapping":        "spec: {field_names_resolution: [{incoming_field_name: ID, field_name: id, operation: new, scope: dataset}], key_columns: [id]}",
		"another action": "spec: {action: disable_rule, key_columns: [id]}",
	} {
		res, err := ParseResolution([]byte(doc))
		if err == nil {
			err = res.Spec.Validate()
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s: exit = %d (%v), want 2", name, code, err)
		}
	}
}

func TestApplyMigrationCarriesKeyColumns(t *testing.T) {
	res, err := ParseResolution([]byte("spec: {action: apply_migration, key_columns: [id]}"))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if err := res.Spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if req := res.Spec.ToRequest(); req.KeyColumns == nil || !slices.Equal(*req.KeyColumns, []string{"id"}) {
		t.Errorf("key_columns = %v", req.KeyColumns)
	}
}

func TestApplyMigrationSendsNoKeyColumnsOnlyWhenTrue(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  *bool
	}{
		{"true", boolPtr(true)},
		{"false", nil},
		{"null", nil},
	} {
		t.Run(tc.value, func(t *testing.T) {
			res, err := ParseResolution([]byte("spec: {action: apply_migration, no_key_columns: " + tc.value + "}"))
			if err != nil {
				t.Fatalf("ParseResolution: %v", err)
			}
			if err := res.Spec.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got := res.Spec.ToRequest().NoKeyColumns
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Errorf("no_key_columns = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNoKeyColumnsIsRefusedAtExitTwo(t *testing.T) {
	for name, doc := range map[string]string{
		"with key_columns": "spec: {action: apply_migration, key_columns: [id], no_key_columns: true}",
		"another action":   "spec: {action: disable_rule, no_key_columns: true}",
		"mapping":          "spec: {field_names_resolution: [{incoming_field_name: ID, field_name: id, operation: new, scope: dataset}], no_key_columns: true}",
	} {
		res, err := ParseResolution([]byte(doc))
		if err == nil {
			err = res.Spec.Validate()
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s: exit = %d (%v), want 2", name, code, err)
		}
	}
}

// A masked viewer's skeleton names the facts that carry masked text, so a
// value copied from one of them into spec is not mistaken for the stored one.
func TestSkeletonNamesTheMaskedFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		masked *[]string
		want   string
	}{
		{"nothing masked", nil, ""},
		{"empty list", &[]string{}, ""},
		{"field masked", &[]string{"upc"},
			"# masked for you: field_value, prefix_candidates (display text, not the stored value; a PII column refuses it as a value)"},
		{"another field masked", &[]string{"sku"},
			"# masked for you: another_field_value (display text, not the stored value; a PII column refuses it as a value)"},
		{"both masked", &[]string{"sku", "upc"},
			"# masked for you: field_value, another_field_value, prefix_candidates (display text, not the stored value; a PII column refuses it as a value)"},
		{"a field the alert does not name", &[]string{"email"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := gtinPrefixAlertForTest(&[]string{"•••••", "•••••"})
			a.MaskedFields = tc.masked
			out := Skeleton(a, "accept_gtin_prefix", nil)
			lines := strings.Split(out, "\n")
			got := lineIndexForTest(lines, "# masked for you")
			switch {
			case tc.want == "" && got >= 0:
				t.Fatalf("want no masked line, got %q:\n%s", lines[got], out)
			case tc.want == "":
				return
			case got < 0 || lines[got] != tc.want:
				t.Fatalf("want the line %q, got:\n%s", tc.want, out)
			}
			if fill := lineIndexForTest(lines, "# Fill in every key below"); got > fill {
				t.Errorf("the masked line must precede the instructions:\n%s", out)
			}
			if _, err := ParseResolution([]byte(out)); err != nil {
				t.Errorf("the skeleton does not parse: %v\n%s", err, out)
			}
		})
	}
}

// field_name can list several fields; masking any one of them masks
// field_value.
func TestMaskedFactsCommentReadsEachListedField(t *testing.T) {
	got := MaskedFactsComment(SkeletonFacts{
		MaskedFields: &[]string{"last_name"},
		FieldName:    strPtrForTest("first_name, last_name"),
		FieldValue:   strPtrForTest("•••••"),
	})
	if !strings.HasPrefix(got, "# masked for you: field_value (") {
		t.Errorf("got %q", got)
	}
}
