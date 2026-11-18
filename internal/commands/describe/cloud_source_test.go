package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func setupDescribeCloudSourceRoot() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "qctl",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Add global flags (same as in root command)
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")

	describeCmd := &cobra.Command{
		Use:   "describe",
		Short: "Show details of a resource",
	}
	describeCmd.AddCommand(NewCloudSourceCommand())
	rootCmd.AddCommand(describeCmd)

	return rootCmd
}

func sampleCloudSourceFullResponse() *cloud_sources.CloudSourceFull {
	now := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	id := 42
	datasetID := 11
	version := 3
	name := "s3-orders"
	datasetName := "orders"
	state := api.DataSourceState("active")
	dsType := api.DataSourceType("s3")
	bucket := "raw-orders"
	secret := "super-secret"
	schedule := "@hourly"

	return &cloud_sources.CloudSourceFull{
		ID:                          &id,
		Name:                        &name,
		DatasetID:                   &datasetID,
		DatasetName:                 &datasetName,
		DataSourceType:              &dsType,
		SettingsModelID:             func() *int { v := 2; return &v }(),
		VersionID:                   &version,
		S3Bucket:                    &bucket,
		S3SecretKey:                 &secret,
		S3AccessKey:                 &secret,
		State:                       &state,
		Schedule:                    &schedule,
		CreatedAt:                   &now,
		DeleteSourceFileAfterBackup: func() *bool { v := false; return &v }(),
	}
}

func sampleCloudSourceTinyResponse() *cloud_sources.CloudSourceTiny {
	clean, bad := 9182, 41
	lastSync := time.Now().Add(-3 * time.Hour)
	return &cloud_sources.CloudSourceTiny{
		ID: 42, Name: "s3-orders", DatasetID: 11, DatasetName: "orders",
		DataSourceType: api.DataSourceType("s3"), State: api.DataSourceState("active"),
		CleanRowsCount: &clean, BadRowsCount: &bad, LastSync: &lastSync, UnresolvedAlerts: 2,
	}
}

func TestDescribeCloudSource_PeriodCursors(t *testing.T) {
	const cursors = `[
  {"dataset_id":11,"settings_model_id":2,"period_key":"2026-06","delta_applied_cursor":null},
  {"dataset_id":12,"settings_model_id":3,"period_key":"2026-07","delta_applied_cursor":0},
  {"dataset_id":13,"settings_model_id":4,"period_key":"2026-08","delta_applied_cursor":84}
]`
	full := sampleCloudSourceFullResponse()
	series := uuid.MustParse("0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b")
	full.TableSeriesID = &series
	body := fullBody(t, full, map[string]string{"period_delta_cursors": cursors})
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out, calls, err := runDescribeCloudSource(t, body, sampleCloudSourceTinyResponse(), "42", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Errorf("requests = %d, want 2", calls)
			}
			if format == "table" {
				for _, want := range []string{"Period delta cursors:", "2026-06 11 2 -", "2026-07 12 3 0", "2026-08 13 4 84"} {
					if !strings.Contains(strings.Join(strings.Fields(out), " "), want) {
						t.Errorf("summary lacks %q:\n%s", want, out)
					}
				}
				return
			}
			var summary map[string]any
			if err := yaml.Unmarshal([]byte(out), &summary); err != nil {
				t.Fatal(err)
			}
			var want any
			if err := yaml.Unmarshal([]byte(cursors), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(summary["period_delta_cursors"], want) {
				t.Errorf("period cursors = %#v, want %#v", summary["period_delta_cursors"], want)
			}
		})
	}
}

// fullBody is full's JSON form with each of raw's members set on it, the
// values raw JSON: a detail response the generated type cannot express.
func fullBody(t *testing.T, full *cloud_sources.CloudSourceFull, raw map[string]string) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for key, value := range raw {
		members[key] = json.RawMessage(value)
	}
	body, err := json.Marshal(members)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

// respondStatus answers every request with status and a detail body.
func respondStatus(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, status, map[string]string{"detail": http.StatusText(status)})
	}
}

// serveDescribeCloudSource serves full and tiny as cloud source 42's detail
// and /tiny routes and runs `qctl describe cloud-source args...`. It returns
// stdout.
func serveDescribeCloudSource(t *testing.T, full, tiny http.HandlerFunc, args ...string) (string, error) {
	t.Helper()
	out, _, err := serveDescribeCloudSourceWithCounts(t, full, tiny, respondStatus(http.StatusForbidden), args...)
	return out, err
}

func serveDescribeCloudSourceWithCounts(t *testing.T, full, tiny, counts http.HandlerFunc, args ...string) (string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/42", full)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/42/tiny", tiny)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/11/quarantine/count-by-status", counts)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupDescribeCloudSourceRoot()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"describe", "cloud-source"}, args...))
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

// runDescribeCloudSource serves full and tiny with status 200 as cloud source
// 42 and runs `qctl describe cloud-source args...`. full is a
// *cloud_sources.CloudSourceFull or, for a body the generated type cannot
// express, a json.RawMessage. It returns stdout and the number of requests
// that reached the server.
func runDescribeCloudSource(t *testing.T, full any, tiny *cloud_sources.CloudSourceTiny, args ...string) (string, int, error) {
	t.Helper()
	calls := 0
	respond := func(body any) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			calls++
			testutil.RespondJSON(w, http.StatusOK, body)
		}
	}
	out, err := serveDescribeCloudSource(t, respond(full), respond(tiny), args...)
	return out, calls, err
}

func TestDescribeCloudSource_Summary(t *testing.T) {
	full := sampleCloudSourceFullResponse()
	applied := 1000
	full.DeltaAppliedCursor = &applied
	out, _, err := runDescribeCloudSource(t, full, sampleCloudSourceTinyResponse(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Cloud source: s3-orders (id: 42)\n",
		"Type:         s3\n",
		"Table:        orders (id: 11)\n",
		"State:        active\n",
		"Schedule:     @hourly\n",
		"Last ingest:  3 hours ago\n",
		"Rows:         9182 clean / 41 quarantined\n",
		"Alerts:       2 unresolved\n",
		"Delta cursor: 1000\n",
		"Overrides:    none\n",
		"qctl get cloud-source 42 -o yaml",
		"qctl get table-config 11",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "super-secret") {
		t.Errorf("a secret reached the summary:\n%s", out)
	}
	for _, label := range []string{"Series:", "Routing:"} {
		if strings.Contains(out, label) {
			t.Errorf("a non-series source prints %q:\n%s", label, out)
		}
	}
}

// A series source names its series and how uploads are routed to a period,
// in text and in the structured summary.
func TestDescribeCloudSource_SeriesSource(t *testing.T) {
	full := sampleCloudSourceFullResponse()
	series := uuid.MustParse("0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b")
	mode, pattern := "file_name", "{period_key}.csv"
	full.TableSeriesID, full.RoutingMode, full.RoutingPattern = &series, &mode, &pattern
	start, due := "2026-03", 12
	full.StartPeriodKey, full.BrokerDueDays = &start, &due

	out, _, err := runDescribeCloudSource(t, full, sampleCloudSourceTinyResponse(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Table:        orders (id: 11)\nSeries:       0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b\nRouting:      file_name {period_key}.csv\n" +
			"Intake:       start 2026-03, end (none)\nBroker due:   12 days after each period ends, a weekend date moved to the Friday before\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}

	out, _, err = runDescribeCloudSource(t, full, sampleCloudSourceTinyResponse(), "42", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var s cloud_sources.Summary
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("not a summary: %v\n%s", err, out)
	}
	if s.TableSeriesID == nil || *s.TableSeriesID != series || s.RoutingMode != mode || s.RoutingPattern != pattern || s.RoutingColumn != "" {
		t.Errorf("structured summary routing = %v %q %q %q", s.TableSeriesID, s.RoutingMode, s.RoutingPattern, s.RoutingColumn)
	}
	if in := s.Intake; in == nil || in.StartPeriodKey == nil || *in.StartPeriodKey != start || in.EndPeriodKey != nil ||
		in.BrokerDueDays == nil || *in.BrokerDueDays != due {
		t.Errorf("structured summary intake = %+v", s.Intake)
	}
}

// A source that never finished an ingestion, has no schedule, and whose
// cursor never advanced.
func TestDescribeCloudSource_SummaryOfAnIdleSource(t *testing.T) {
	full := sampleCloudSourceFullResponse()
	full.Schedule = nil
	tiny := sampleCloudSourceTinyResponse()
	tiny.LastSync, tiny.CleanRowsCount, tiny.BadRowsCount = nil, nil, nil
	out, _, err := runDescribeCloudSource(t, full, tiny, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Schedule:     (none)\n",
		"Last ingest:  never\n",
		"Rows:         - clean / - quarantined\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Delta cursor:") {
		t.Errorf("a never-advanced cursor is shown:\n%s", out)
	}
}

// Overrides are named; their values are never printed.
func TestDescribeCloudSource_NamesOverridesWithoutValues(t *testing.T) {
	full := sampleCloudSourceFullResponse()
	nulls := []string{"SENTINEL-NULL"}
	mapping := map[string]map[string]string{"status": {"SENTINEL-FROM": "SENTINEL-TO"}}
	full.NullValuesOverride = &nulls
	full.ValueMappingPerColumnOverride = &mapping
	out, _, err := runDescribeCloudSource(t, full, sampleCloudSourceTinyResponse(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Overrides:    null_values, value_mapping_per_column\n") {
		t.Errorf("the overrides are not named:\n%s", out)
	}
	if strings.Contains(out, "SENTINEL") {
		t.Errorf("an override's value reached the summary:\n%s", out)
	}
}

// excluded_row_signatures_override is the deleted-rows ledger, not a setting:
// a source that sets nothing else overrides none.
func TestDescribeCloudSource_DeletedLineSignaturesIsNotAnOverride(t *testing.T) {
	body := fullBody(t, sampleCloudSourceFullResponse(), map[string]string{
		"excluded_row_signatures_override": `[]`,
	})
	out, _, err := runDescribeCloudSource(t, body, sampleCloudSourceTinyResponse(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Overrides:    none\n") {
		t.Errorf("the deleted-rows ledger was named as an override:\n%s", out)
	}
}

// An override the generated response type has no field for is named from the
// response body.
func TestDescribeCloudSource_NamesAnOverrideTheGeneratedTypeLacks(t *testing.T) {
	const key = "newer_setting_override"
	var decoded cloud_sources.CloudSourceFull
	if err := json.Unmarshal([]byte(`{"`+key+`": ["%Y"]}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundTrip, _ := json.Marshal(decoded); strings.Contains(string(roundTrip), key) {
		t.Fatalf("%s survives the generated type; this test needs a key it drops", key)
	}

	body := fullBody(t, sampleCloudSourceFullResponse(), map[string]string{
		key:                                `["SENTINEL-%Y"]`,
		"null_values_override":             `null`,
		"excluded_row_signatures_override": `[]`,
	})
	out, _, err := runDescribeCloudSource(t, body, sampleCloudSourceTinyResponse(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Overrides:    newer_setting\n") {
		t.Errorf("the override is not named:\n%s", out)
	}
	if strings.Contains(out, "SENTINEL") {
		t.Errorf("an override's value reached the summary:\n%s", out)
	}
}

func TestDescribeCloudSource_StructuredSummary(t *testing.T) {
	full := sampleCloudSourceFullResponse()
	nulls := []string{"N/A"}
	full.NullValuesOverride = &nulls
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			out, _, err := runDescribeCloudSource(t, full, sampleCloudSourceTinyResponse(), "42", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var s cloud_sources.Summary
			if format == "json" {
				err = json.Unmarshal([]byte(out), &s)
			} else {
				err = yaml.Unmarshal([]byte(out), &s)
			}
			if err != nil {
				t.Fatalf("not a summary: %v\n%s", err, out)
			}
			if s.ID != 42 || s.TableID != 11 || s.State != "active" || s.UnresolvedAlerts != 2 {
				t.Errorf("summary = %+v", s)
			}
			if s.CleanRowsCount == nil || *s.CleanRowsCount != 9182 || s.LastSync == nil {
				t.Errorf("the /tiny half is missing: %+v", s)
			}
			if len(s.Overrides) != 1 || s.Overrides[0] != "null_values" {
				t.Errorf("overrides = %v, want [null_values]", s.Overrides)
			}
			if strings.Contains(out, "N/A") || strings.Contains(out, "super-secret") {
				t.Errorf("a value reached the structured summary:\n%s", out)
			}
			if strings.Contains(out, "kind:") || strings.Contains(out, `"kind"`) {
				t.Errorf("-o %s printed a manifest, not the summary:\n%s", format, out)
			}

			// The sample's cursor never advanced: the member is present and
			// null, not left out.
			var members map[string]any
			if format == "json" {
				err = json.Unmarshal([]byte(out), &members)
			} else {
				err = yaml.Unmarshal([]byte(out), &members)
			}
			if err != nil {
				t.Fatalf("not an object: %v\n%s", err, out)
			}
			if cursor, ok := members["delta_applied_cursor"]; !ok || cursor != nil {
				t.Errorf("delta_applied_cursor = %v (present: %t), want null:\n%s", cursor, ok, out)
			}
			if cursors, ok := members["period_delta_cursors"].([]any); !ok || len(cursors) != 0 {
				t.Errorf("period_delta_cursors = %#v, want []:\n%s", members["period_delta_cursors"], out)
			}
			for _, member := range []string{"table_series_id", "routing_mode", "routing_pattern", "routing_column", "intake"} {
				if _, ok := members[member]; ok {
					t.Errorf("a non-series source carries %s:\n%s", member, out)
				}
			}
		})
	}
}

func TestDescribeCloudSource_NonNumericIDIsBadUsage(t *testing.T) {
	_, calls, err := runDescribeCloudSource(t, sampleCloudSourceFullResponse(), sampleCloudSourceTinyResponse(), "s3-orders")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	if calls != 0 {
		t.Errorf("%d requests reached the server, want 0", calls)
	}
}

// A deleted source is the detail route's 404; /tiny, which still answers for
// it, is never asked.
func TestDescribeCloudSource_DeletedSourceIsNotFound(t *testing.T) {
	tinyCalls := 0
	full := func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]string{"detail": "Cloud source 42 is deleted."})
	}
	tiny := func(w http.ResponseWriter, _ *http.Request) {
		tinyCalls++
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceTinyResponse())
	}
	_, err := serveDescribeCloudSource(t, full, tiny, "42")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitNotFound, err)
	}
	if tinyCalls != 0 {
		t.Errorf("/tiny was asked %d times for a deleted source, want 0", tinyCalls)
	}
}

// Either read failing fails the command with that read's exit code, and
// nothing is printed.
func TestDescribeCloudSource_ReadFailuresKeepTheirExitCode(t *testing.T) {
	ok := func(body any) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, body)
		}
	}
	for _, tc := range []struct {
		name       string
		full, tiny http.HandlerFunc
		want       int
	}{
		{"detail 401", respondStatus(http.StatusUnauthorized), ok(sampleCloudSourceTinyResponse()), qerrors.ExitUnauthorized},
		{"detail 403", respondStatus(http.StatusForbidden), ok(sampleCloudSourceTinyResponse()), qerrors.ExitUnauthorized},
		{"tiny 403", ok(sampleCloudSourceFullResponse()), respondStatus(http.StatusForbidden), qerrors.ExitUnauthorized},
		{"tiny 500", ok(sampleCloudSourceFullResponse()), respondStatus(http.StatusInternalServerError), qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := serveDescribeCloudSource(t, tc.full, tc.tiny, "42")
			if err == nil {
				t.Fatalf("no error; printed:\n%s", out)
			}
			if got := qerrors.GetExitCode(err); got != tc.want {
				t.Errorf("exit code = %d, want %d (err: %v)", got, tc.want, err)
			}
			if out != "" {
				t.Errorf("a failed read still printed:\n%s", out)
			}
		})
	}
}
