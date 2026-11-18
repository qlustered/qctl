package cmdutil

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func selectorCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	AddTableFlags(cmd, "Filter by table")
	return cmd
}

func TestAddTableFlagsRegistersANameAndAnID(t *testing.T) {
	cmd := selectorCmd()
	name := cmd.Flags().Lookup("table")
	if name == nil || name.Value.Type() != "string" {
		t.Fatalf("--table must be a string flag, got %v", name)
	}
	id := cmd.Flags().Lookup("table-id")
	if id == nil || id.Value.Type() != "int" {
		t.Fatalf("--table-id must be an int flag, got %v", id)
	}
	if !strings.Contains(name.Usage, "name") {
		t.Errorf("--table usage must say it is a name: %q", name.Usage)
	}
}

func TestValidateOptionalTableSelectorAllowsOmitted(t *testing.T) {
	if err := ValidateOptionalTableSelector(selectorCmd(), ""); err != nil {
		t.Fatalf("omitted optional selector: %v", err)
	}
}

func TestValidateOptionalTableSelectorRejectsLocalErrors(t *testing.T) {
	tests := []struct {
		name string
		set  map[string]string
		want string
	}{
		{
			name: "non-positive id",
			set:  map[string]string{"table-id": "0"},
			want: "--table-id must be a positive integer",
		},
		{
			name: "conflicting flags",
			set:  map[string]string{"table": "orders", "table-id": "42"},
			want: "table specified more than once (--table, --table-id)",
		},
		{
			name: "empty name",
			set:  map[string]string{"table": ""},
			want: "--table cannot be empty",
		},
		{
			name: "empty name still conflicts",
			set:  map[string]string{"table": "", "table-id": "42"},
			want: "table specified more than once (--table, --table-id)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := selectorCmd()
			for name, value := range tt.set {
				if err := cmd.Flags().Set(name, value); err != nil {
					t.Fatal(err)
				}
			}
			err := ValidateOptionalTableSelector(cmd, "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			assertBadUsage(t, err)
		})
	}
}

func TestRequiredTableSelectorValidatorsRejectOmission(t *testing.T) {
	tests := []struct {
		name      string
		validate  func(*cobra.Command) error
		want      []string
		doNotWant string
	}{
		{
			name:     "positional accepted",
			validate: func(cmd *cobra.Command) error { return ValidateTableSelector(cmd, "") },
			want:     []string{"table is required", "the positional argument", "--table <name>", "--table-id <id>"},
		},
		{
			name:      "flags only",
			validate:  ValidateTableSelectorFromFlags,
			want:      []string{"table is required", "--table <name>", "--table-id <id>"},
			doNotWant: "positional",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.validate(selectorCmd())
			if err == nil {
				t.Fatal("omitted required selector was accepted")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error must contain %q: %v", want, err)
				}
			}
			if tt.doNotWant != "" && strings.Contains(err.Error(), tt.doNotWant) {
				t.Errorf("the error must not contain %q: %v", tt.doNotWant, err)
			}
			assertBadUsage(t, err)
		})
	}
}

func TestOptionalTableIDReturnsUnsetWhenNothingGiven(t *testing.T) {
	cmd := selectorCmd()
	id, ok, err := OptionalTableID(nil, cmd, "")
	if err != nil || ok || id != 0 {
		t.Fatalf("got (%d, %v, %v), want (0, false, nil)", id, ok, err)
	}
}

func TestOptionalTableIDTakesTableIDWithoutAnAPICall(t *testing.T) {
	cmd := selectorCmd()
	if err := cmd.Flags().Set("table-id", "42"); err != nil {
		t.Fatal(err)
	}
	// ctx is nil on purpose: --table-id must resolve with no client at all.
	id, ok, err := OptionalTableID(nil, cmd, "")
	if err != nil || !ok || id != 42 {
		t.Fatalf("got (%d, %v, %v), want (42, true, nil)", id, ok, err)
	}
}

func TestOptionalTableIDRejectsNonPositiveTableID(t *testing.T) {
	cmd := selectorCmd()
	if err := cmd.Flags().Set("table-id", "0"); err != nil {
		t.Fatal(err)
	}
	_, _, err := OptionalTableID(nil, cmd, "")
	if err == nil || !strings.Contains(err.Error(), "--table-id must be a positive integer") {
		t.Fatalf("expected a positive-integer error, got %v", err)
	}
	assertBadUsage(t, err)
}

func TestOptionalTableIDRejectsTwoSelectors(t *testing.T) {
	cases := [][2]string{
		{"table", "orders"},
		{"table-id", "42"},
	}
	cmd := selectorCmd()
	for _, c := range cases {
		if err := cmd.Flags().Set(c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := OptionalTableID(nil, cmd, "")
	if err == nil || !strings.Contains(err.Error(), "table specified more than once") {
		t.Fatalf("expected a specified-more-than-once error, got %v", err)
	}
	if !strings.Contains(err.Error(), "--table") || !strings.Contains(err.Error(), "--table-id") {
		t.Errorf("the error must name both selectors: %v", err)
	}
	assertBadUsage(t, err)
}

func TestOptionalTableIDRejectsPositionalPlusFlag(t *testing.T) {
	cmd := selectorCmd()
	if err := cmd.Flags().Set("table-id", "42"); err != nil {
		t.Fatal(err)
	}
	_, _, err := OptionalTableID(nil, cmd, "orders")
	if err == nil || !strings.Contains(err.Error(), "table specified more than once") {
		t.Fatalf("expected a specified-more-than-once error, got %v", err)
	}
	assertBadUsage(t, err)
}

func TestResolveTableIDRequiresASelector(t *testing.T) {
	cmd := selectorCmd()
	_, err := ResolveTableID(nil, cmd, "")
	if err == nil || !strings.Contains(err.Error(), "table is required") {
		t.Fatalf("expected a table-required error, got %v", err)
	}
	for _, want := range []string{"the positional argument", "--table <name>", "--table-id <id>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must show %q: %v", want, err)
		}
	}
	assertBadUsage(t, err)
}

// TestResolveTableIDFromFlagsRequiresASelector is the other half of the pair,
// and the reason the pair exists: on a command whose positional slot holds a
// rule name or a suggestion UUID, "pass it as the positional argument" is not
// merely unhelpful — a user who follows it binds the table into <name-or-id>
// or <suggestion-id> and gets a wrong lookup rather than an error. The absence
// assertion is the load-bearing one; a shared message that says "positional"
// would satisfy every "table is required" substring test ever written.
func TestResolveTableIDFromFlagsRequiresASelector(t *testing.T) {
	cmd := selectorCmd()
	_, err := ResolveTableIDFromFlags(nil, cmd)
	if err == nil || !strings.Contains(err.Error(), "table is required") {
		t.Fatalf("expected a table-required error, got %v", err)
	}
	for _, want := range []string{"--table <name>", "--table-id <id>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must show %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "positional") {
		t.Errorf("this command has no table positional; the error must not offer one: %v", err)
	}
	assertBadUsage(t, err)
}

// The flags-only variant must be the same resolver underneath, not a second
// implementation that could drift on the name-vs-ID guarantee.
func TestResolveTableIDFromFlagsDelegatesTheSameNameLookup(t *testing.T) {
	ctx, mock := newResolveTestContext(t)

	mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results":    []interface{}{map[string]interface{}{"id": 7, "name": "42"}},
			"total_rows": 1, "page": 1,
		})
	})

	cmd := selectorCmd()
	if err := cmd.Flags().Set("table", "42"); err != nil {
		t.Fatal(err)
	}

	id, err := ResolveTableIDFromFlags(ctx, cmd)
	if err != nil || id != 7 {
		t.Fatalf("got (%d, %v), want (7, nil)", id, err)
	}
}

// TestOptionalTableIDResolvesANameThatLooksLikeAnID pins the entire reason
// this helper exists: a table literally named "42" must be reachable through
// --table. Routing --table through datasets.ResolveID (which tries
// strconv.Atoi first) would silently treat "42" as an ID instead of a name.
// The mock's name-search result returns a different ID (7) than the string
// itself (42), so an Atoi short-circuit could not accidentally produce the
// right answer for the wrong reason — and a handler on the ID-fetch endpoint
// proves that endpoint is never hit at all.
func TestOptionalTableIDResolvesANameThatLooksLikeAnID(t *testing.T) {
	ctx, mock := newResolveTestContext(t)

	nameSearchCalls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		nameSearchCalls++
		if got := r.URL.Query().Get("name"); got != "42" {
			t.Errorf("expected the name-search query name=42, got %q", got)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results":    []interface{}{map[string]interface{}{"id": 7, "name": "42"}},
			"total_rows": 1, "page": 1,
		})
	})

	idFetchCalls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/datasets/42", func(w http.ResponseWriter, _ *http.Request) {
		idFetchCalls++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": 42, "name": "wrong-table"})
	})

	cmd := selectorCmd()
	if err := cmd.Flags().Set("table", "42"); err != nil {
		t.Fatal(err)
	}

	id, ok, err := OptionalTableID(ctx, cmd, "")
	if err != nil || !ok {
		t.Fatalf("got (%d, %v, %v), want (7, true, nil)", id, ok, err)
	}
	if id != 7 {
		t.Errorf("resolved ID = %d, want 7 (the table actually named \"42\"); "+
			"an Atoi short-circuit would have returned 42 instead", id)
	}
	if nameSearchCalls != 1 {
		t.Errorf("expected exactly 1 name-search request, got %d", nameSearchCalls)
	}
	if idFetchCalls != 0 {
		t.Errorf("--table must never route through the ID-fetch endpoint (datasets.ResolveID's Atoi path); got %d calls", idFetchCalls)
	}
}

// TestResolveTableIDDelegatesTheSameNameLookup is a light check that
// ResolveTableID's success path is really OptionalTableID underneath — the
// name-vs-ID guarantee itself is pinned above.
func TestResolveTableIDDelegatesTheSameNameLookup(t *testing.T) {
	ctx, mock := newResolveTestContext(t)

	mock.RegisterHandler("GET", "/api/orgs/"+resolveTestOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results":    []interface{}{map[string]interface{}{"id": 7, "name": "42"}},
			"total_rows": 1, "page": 1,
		})
	})

	cmd := selectorCmd()
	if err := cmd.Flags().Set("table", "42"); err != nil {
		t.Fatal(err)
	}

	id, err := ResolveTableID(ctx, cmd, "")
	if err != nil || id != 7 {
		t.Fatalf("got (%d, %v), want (7, nil)", id, err)
	}
}
