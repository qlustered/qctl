package concepts

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrg = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func concept(id, slug, name string) Tiny {
	return Tiny{ID: uuid.MustParse(id), OrganizationID: uuid.MustParse(testOrg), Slug: slug, Name: name, UpdatedAt: time.Now()}
}

func TestResolveOrderAndExitCodes(t *testing.T) {
	items := []Tiny{
		concept("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "policy-number", "Policy Number"),
		concept("bbbb0000-bbbb-cccc-dddd-eeeeeeee0002", "policy_number", "Policy Number"),
	}
	for _, tc := range []struct {
		input string
		want  uuid.UUID
		code  int
	}{
		{input: "policy-number", want: items[0].ID},
		{input: "bbbb0000", want: items[1].ID},
		{input: "policy", code: qerrors.ExitBadUsage},
		{input: "missing", code: qerrors.ExitNotFound},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := Resolve(items, tc.input)
			if tc.code == 0 && (err != nil || got != tc.want) {
				t.Fatalf("Resolve = %s, %v; want %s", got, err, tc.want)
			}
			if tc.code != 0 && qerrors.GetExitCode(err) != tc.code {
				t.Fatalf("exit code = %d, want %d (err: %v)", qerrors.GetExitCode(err), tc.code, err)
			}
		})
	}
}

func TestResolveIDReadsCompleteCatalog(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	items := []Tiny{
		concept("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "policy-number-extra", "Policy Number Extra"),
		concept("bbbb0000-bbbb-cccc-dddd-eeeeeeee0002", "policy-number", "Policy Number"),
	}
	var limits []int
	mock.RegisterHandler("GET", "/api/orgs/"+testOrg+"/concepts", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		limits = append(limits, limit)
		count := len(items)
		page := 1
		end := min(limit, count)
		testutil.RespondJSON(w, http.StatusOK, Page{Results: items[:end], TotalRows: &count, Page: &page})
	})
	c := NewClient(mock.URL(), testOrg, 0)
	c.listLimit = 1
	got, err := c.ResolveID("token", "policy-number")
	if err != nil || got != items[1].ID {
		t.Fatalf("ResolveID = %s, %v; want %s", got, err, items[1].ID)
	}
	if len(limits) != 2 || limits[0] != 1 || limits[1] <= len(items) {
		t.Fatalf("limits = %v, want re-read with room beyond total", limits)
	}
}

func TestResolveIDRejectsIncompleteCatalog(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	one := concept("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001", "policy-number", "Policy Number")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrg+"/concepts", func(w http.ResponseWriter, _ *http.Request) {
		count := 2
		testutil.RespondJSON(w, http.StatusOK, Page{Results: []Tiny{one}, TotalRows: &count})
	})
	c := NewClient(mock.URL(), testOrg, 0)
	_, err := c.ResolveID("token", "policy-number")
	if !errors.Is(err, ErrIncompleteListing) {
		t.Fatalf("error = %v, want incomplete listing", err)
	}
}

func TestResolveIDRejectsMalformedFullUUIDAsUsage(t *testing.T) {
	c := NewClient("http://localhost:1", testOrg, 0)
	_, err := c.ResolveID("token", "aaaa0000-bbbb-cccc-dddd-eeeeeeee00--")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want 2 (error: %v)", code, err)
	}
}
