package get

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

const softDeletedTablesURL = "/api/orgs/" + testOrgID + "/ops/soft-deleted-tables"

var (
	softDeletedOrgID   = uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	softDeletedActorID = uuid.MustParse("0190cccc-0000-4000-8000-000000000002")
)

// softDeletedReportFixture is a report with one readable group of two tables
// (one deleter known by name, one not recorded) and one unreadable group.
func softDeletedReportFixture() api.SoftDeletedTablesReportSchema {
	ann := "Ann Ops"
	readable, small, big := 3*1024*1024+1000, 1000, 3*1024*1024
	sizeError := "Could not read the destination: OperationalError"
	deleted := time.Now().Add(-45 * 24 * time.Hour)
	return api.SoftDeletedTablesReportSchema{
		DeletedBefore: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC),
		MinAgeDays:    30, TableCount: 3, SizeBytes: readable,
		Groups: []api.SoftDeletedTablesGroupSchema{
			{
				OrganizationID: &softDeletedOrgID, OrganizationName: "Acme",
				DestinationID: 4, DestinationName: "warehouse", DatabaseName: "acme_db",
				SizeBytes: &readable,
				Tables: []api.SoftDeletedTableSchema{
					{
						DatasetID: 101, Name: "Claims 2025", SchemaName: "public", TableName: "claims_2025",
						DeletedAt: deleted, DeletedByID: &softDeletedActorID, DeletedByName: &ann, SizeBytes: &big,
					},
					{
						DatasetID: 102, Name: "Scratch", SchemaName: "work", TableName: "scratch",
						DeletedAt: deleted, SizeBytes: &small,
					},
				},
			},
			{
				OrganizationName: "No organization",
				DestinationID:    9, DestinationName: "legacy", DatabaseName: "old_db",
				SizeError: &sizeError,
				Tables: []api.SoftDeletedTableSchema{
					{DatasetID: 201, Name: "Orphan", SchemaName: "public", TableName: "orphan", DeletedAt: deleted},
				},
			},
		},
	}
}

func TestGetSoftDeletedTablesTable(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := servePrivilege(e, softDeletedTablesURL, http.StatusOK, privilegeWireBody(t, softDeletedReportFixture()))

	out, stderr, err := e.run(t, NewSoftDeletedTablesCommand(), "soft-deleted-tables")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stderr)
	}
	if query.Has("min_age_days") {
		t.Errorf("min_age_days sent without --min-age-days: %v", *query)
	}
	for _, header := range []string{"REFERENCE", "NAME", "ORGANIZATION", "DESTINATION", "DATABASE", "RELATION", "DELETED", "DELETED-BY", "SIZE"} {
		if !strings.Contains(out, header) {
			t.Errorf("missing header %q\n%s", header, out)
		}
	}
	checks := map[string][]string{
		"table/101": {"Acme", "warehouse", "acme_db", "public.claims_2025", "Ann", "3.0", "MB", "1 month ago"},
		"table/102": {"work.scratch", "1000", "bytes"},
		"table/201": {"legacy", "old_db", "public.orphan", "unknown"},
	}
	for ref, wants := range checks {
		row := strings.Join(rowFields(t, out, ref), " ")
		for _, want := range wants {
			if !strings.Contains(row, want) {
				t.Errorf("%s row missing %q: %s", ref, want, row)
			}
		}
	}
	// The deleter cell is the name the report embeds, not the UUID.
	if strings.Contains(out, softDeletedActorID.String()) {
		t.Errorf("deleted_by should show the name when the server sent one\n%s", out)
	}
	if strings.Index(out, "table/101") > strings.Index(out, "table/201") {
		t.Errorf("rows must keep the server's order\n%s", out)
	}
	for _, want := range []string{
		"3 table(s) soft-deleted at least 30 days ago (on or before 2026-10-18T09:00:00Z), 3.0 MB in all, not counting 1 group(s) of unknown size.",
		"Size unknown for No organization / legacy (old_db): Could not read the destination: OperationalError",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q\n%s", want, stderr)
		}
	}
	assertPrivilegeRequests(t, e, "GET "+softDeletedTablesURL)
}

func TestGetSoftDeletedTablesSendsMinAgeDays(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := servePrivilege(e, softDeletedTablesURL, http.StatusOK, privilegeWireBody(t, softDeletedReportFixture()))

	if _, stderr, err := e.run(t, NewSoftDeletedTablesCommand(), "soft-deleted-tables", "--min-age-days", "0"); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stderr)
	}
	if got := query.Get("min_age_days"); got != "0" {
		t.Errorf("min_age_days = %q, want 0 (an explicit zero must reach the server)", got)
	}
}

func TestGetSoftDeletedTablesRejectsNegativeMinAgeBeforeAnyRequest(t *testing.T) {
	e := newDataReviewGetEnv(t)

	_, _, err := e.run(t, NewSoftDeletedTablesCommand(), "soft-deleted-tables", "--min-age-days", "-1")
	assertExitCode(t, err, 2)
	assertPrivilegeRequests(t, e)
}

func TestGetSoftDeletedTablesRejectsUnknownColumnBeforeAnyRequest(t *testing.T) {
	e := newDataReviewGetEnv(t)

	_, _, err := e.run(t, NewSoftDeletedTablesCommand(), "soft-deleted-tables", "--columns", "nope")
	assertExitCode(t, err, 2)
	assertPrivilegeRequests(t, e)
}

func TestGetSoftDeletedTablesJSONIsTheWireBody(t *testing.T) {
	e := newDataReviewGetEnv(t)
	body := privilegeWireBody(t, softDeletedReportFixture())
	servePrivilege(e, softDeletedTablesURL, http.StatusOK, body)

	out, _, err := e.run(t, NewSoftDeletedTablesCommand(), "soft-deleted-tables", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != string(body) {
		t.Errorf("-o json is not the wire body\n got: %s\nwant: %s", out, body)
	}
}

func TestGetSoftDeletedTablesNamePrintsTableReferences(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, softDeletedTablesURL, http.StatusOK, privilegeWireBody(t, softDeletedReportFixture()))

	out, stderr, err := e.run(t, NewSoftDeletedTablesCommand(), "soft-deleted-tables", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "table/101\ntable/102\ntable/201\n"; out != want {
		t.Errorf("-o name = %q, want %q", out, want)
	}
	if stderr != "" {
		t.Errorf("-o name should print no summary, got %q", stderr)
	}
}

func TestGetSoftDeletedTablesEmptyReport(t *testing.T) {
	e := newDataReviewGetEnv(t)
	empty := api.SoftDeletedTablesReportSchema{
		DeletedBefore: time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC), MinAgeDays: 30,
		Groups: []api.SoftDeletedTablesGroupSchema{},
	}
	servePrivilege(e, softDeletedTablesURL, http.StatusOK, privilegeWireBody(t, empty))

	out, stderr, err := e.run(t, NewSoftDeletedTablesCommand(), "soft-deleted-tables")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("an empty report prints no table, got %q", out)
	}
	want := "No table was soft-deleted at least 30 days ago (on or before 2026-10-18T09:00:00Z) in any organization.\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestGetSoftDeletedTablesNonOpsNotFoundExplainsTheRole(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, softDeletedTablesURL, http.StatusNotFound, []byte(`{"detail":{"msg":"Not found."}}`))

	_, _, err := e.run(t, NewSoftDeletedTablesCommand(), "soft-deleted-tables")
	assertExitCode(t, err, 3)
	if !strings.Contains(err.Error(), "unless your role in the current organization is ops") {
		t.Errorf("a 404 should explain the ops gate, got: %v", err)
	}
}

func TestFormatByteSize(t *testing.T) {
	for size, want := range map[int]string{
		0:                             "0 bytes",
		1023:                          "1023 bytes",
		1024:                          "1.0 KB",
		3 * 1024 * 1024:               "3.0 MB",
		5 * 1024 * 1024 * 1024:        "5.0 GB",
		2 * 1024 * 1024 * 1024 * 1024: "2.0 TB",
	} {
		if got := formatByteSize(size); got != want {
			t.Errorf("formatByteSize(%d) = %q, want %q", size, got, want)
		}
	}
}
