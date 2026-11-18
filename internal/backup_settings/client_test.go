package backup_settings //nolint:revive // Match the established package under test.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func fullFixture(name string) BackupSettingsFull {
	return BackupSettingsFull{
		ID: 7, VersionID: 3, Name: &name, BackupType: api.BackupTypeS3,
		BackupS3Bucket: "daily-backups", BackupS3DefaultBufferSize: 131072,
		CreatedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestGetByNameSweepsEveryPage(t *testing.T) {
	for _, tc := range []struct {
		name, lookup string
		empty        bool
		status       int
		wantPages    int
		wantExit     int
	}{
		{name: "late match", lookup: "daily", wantPages: 3},
		{name: "no match", lookup: "missing", wantPages: 3},
		{name: "empty page", lookup: "missing", empty: true, wantPages: 2},
		{name: "later page forbidden", lookup: "daily", status: http.StatusForbidden, wantPages: 2, wantExit: qerrors.ExitUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			pages := 0
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, r *http.Request) {
				pages++
				query := r.URL.Query()
				page, _ := strconv.Atoi(query.Get("page"))
				if page != pages || query.Get("limit") != "100" || query.Get("order_by") != "id" {
					t.Errorf("query = %v, request %d", query, pages)
				}
				if page == 2 && tc.status != 0 {
					testutil.RespondError(w, tc.status, "denied")
					return
				}
				rows := []BackupSettingsTiny{}
				if !tc.empty || page == 1 {
					for i := (page-1)*100 + 1; i <= min(page*100, 250); i++ {
						name := "backup-" + strconv.Itoa(i)
						item := BackupSettingsTiny{ID: i + 100, Name: &name}
						if i == 250 {
							name = "daily"
							item.ID = 7
						}
						rows = append(rows, item)
					}
				}
				response := map[string]any{"results": rows, "page": page, "total_rows": 1}
				if page == 1 {
					response["total_rows"] = 250
					response["next"] = map[string]any{"start": 201}
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/backup-settings/7", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, fullFixture("daily"))
			})
			c, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.GetByName("token", tc.lookup)
			if exit := qerrors.GetExitCode(err); exit != tc.wantExit {
				t.Fatalf("exit = %d, want %d: %v", exit, tc.wantExit, err)
			}
			if err == nil && tc.lookup == "daily" && (got == nil || got.ID != 7) {
				t.Errorf("GetByName = %#v, want id 7", got)
			}
			if tc.lookup == "missing" && got != nil {
				t.Errorf("GetByName = %#v, want nil", got)
			}
			if pages != tc.wantPages {
				t.Errorf("list requests = %d, want %d", pages, tc.wantPages)
			}
		})
	}
}

func TestApplyUpdateSendsSparseBodyAndVersion(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	name := "daily"
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results": []BackupSettingsTiny{{ID: 7, Name: &name, BackupType: api.BackupTypeS3, Bucket: "old", CreatedAt: time.Now()}},
			"page":    1, "total_rows": 1,
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/backup-settings/7", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, fullFixture(name))
	})
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["id"] != float64(7) || body["version_id"] != float64(3) || body["name"] != name {
			t.Fatalf("identity/version body = %#v", body)
		}
		if body["backup_s3_bucket"] != "new-bucket" {
			t.Fatalf("backup_s3_bucket = %#v", body["backup_s3_bucket"])
		}
		for _, absent := range []string{"backup_s3_secret_key", "backup_s3_access_key", "backup_gpg_private_key"} {
			if _, ok := body[absent]; ok {
				t.Errorf("omitted credential %q was sent", absent)
			}
		}
		updated := fullFixture(name)
		updated.BackupS3Bucket = "new-bucket"
		testutil.RespondJSON(w, http.StatusOK, updated)
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Apply("token", &Manifest{
		APIVersion: "qluster.ai/v1", Kind: "BackupSettings",
		Metadata: metadata(name),
		Spec:     Spec{BackupType: ptr(api.BackupTypeS3), BackupS3Bucket: ptr("new-bucket")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "updated" || result.Name != name {
		t.Fatalf("result = %#v", result)
	}
}

func TestCreateAcceptsS3CompatibleKeysAndKeylessGS(t *testing.T) {
	for _, spec := range []Spec{
		{
			BackupType: ptr(api.BackupTypeS3Compatible), BackupS3Bucket: ptr("archive"),
			BackupS3EndpointURL: ptr("https://store.example.com"),
			BackupS3AccessKey:   ptr("test-access"), BackupS3SecretKey: ptr("test-secret"),
		},
		{BackupType: ptr(api.BackupTypeGs), BackupGSBucket: ptr("archive")},
	} {
		t.Run(string(*spec.BackupType), func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			creates := 0
			mock.RegisterHandler(http.MethodPost, "/api/orgs/"+testOrgID+"/backup-settings", func(w http.ResponseWriter, _ *http.Request) {
				creates++
				response := fullFixture("daily")
				response.BackupType = *spec.BackupType
				testutil.RespondJSON(w, http.StatusOK, response)
			})
			c, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatal(err)
			}
			result, err := c.Create("token", &Manifest{Metadata: metadata("daily"), Spec: spec})
			if err != nil {
				t.Fatal(err)
			}
			if creates != 1 || result.BackupType != *spec.BackupType {
				t.Errorf("creates = %d, result = %#v", creates, result)
			}
		})
	}
}
