package alerts

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"gopkg.in/yaml.v3"
)

func alertPtr[T any](value T) *T {
	return &value
}

func TestAPIResponseToManifestPreservesVerbosityTiers(t *testing.T) {
	assignedID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	whodunitID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	resp := &AlertFull{
		ID:                           7,
		IssueType:                    api.AlertType("MISSING_COLUMN"),
		Msg:                          "missing email",
		DatasetID:                    11,
		DatasetName:                  "users",
		FieldName:                    alertPtr("email"),
		FieldValue:                   alertPtr("bad"),
		AnotherFieldName:             alertPtr("backup_email"),
		AnotherFieldValue:            alertPtr("also-bad"),
		DataSourceModelID:            alertPtr(13),
		DataSourceModelName:          alertPtr("uploads"),
		MigrationModelID:             alertPtr(17),
		Publisher:                    alertPtr("validator"),
		StackTrace:                   alertPtr("trace"),
		RedirectURL:                  alertPtr("/alerts/7"),
		BlocksIngestionForDataSource: alertPtr(true),
		BlocksIngestionForDataset:    alertPtr(true),
		BlocksProfiling:              alertPtr(true),
		BlocksStoredItem:             alertPtr(true),
		ResolvableByUser:             alertPtr(true),
		ResolveAfterMigration:        alertPtr(true),
		Resolved:                     alertPtr(true),
		IsRowLevel:                   true,
		Count:                        1000,
		CountCapped:                  alertPtr(true),
		AlertActionsList:             alertPtr([]api.AlertAction{"replace", "ignore"}),
		IngestionJobIds:              alertPtr([]int{29, 31}),
		IngestionJobs: alertPtr([]api.AlertIngestionJobSchema{
			{ID: 29, Kind: "file", State: "done", StoredItemID: alertPtr(41), FileName: alertPtr("orders.csv")},
			{ID: 31, Kind: "dry_run", State: "done", IsDryRun: true, DryRunJobID: alertPtr(43)},
		}),
		AssignedUser: &api.UserInfoTinySchema{
			ID: assignedID, Email: "assigned@example.com",
		},
		Whodunit: &api.UserInfoTinySchema{
			ID: whodunitID, Email: "actor@example.com",
		},
		DependentAlert: &api.DependentAlertSchema{ID: 37, Msg: "resolve first"},
		StoredItemsToAlerts: alertPtr([]api.StoredItemToAlertSchema{
			{}, {},
		}),
	}

	stats := &IssueStats{AffectingRowsCount: alertPtr(1234)}

	base := APIResponseToManifest(resp, stats, 0)
	if base.Spec.FieldValue != nil || base.Spec.DataSourceModelID != 0 || base.Spec.Whodunit != nil || base.Status.ExpiredAt != "" || base.Spec.IngestionJobs != nil {
		t.Fatalf("verbosity 0 leaked Tier 2 fields: %#v", base)
	}
	if base.Spec.FieldName != "email" || base.Spec.DataSourceModelName != "uploads" {
		t.Fatalf("verbosity 0 omitted base optional fields: %#v", base.Spec)
	}
	if base.Spec.AssignedUser == nil || base.Spec.AssignedUser.ID != assignedID.String() {
		t.Fatalf("assigned user mapping = %#v", base.Spec.AssignedUser)
	}
	if base.Spec.DependentAlert == nil || base.Spec.DependentAlert.ID != 37 {
		t.Fatalf("dependent alert mapping = %#v", base.Spec.DependentAlert)
	}
	if !reflect.DeepEqual(base.Spec.Actions, []string{"replace", "ignore"}) {
		t.Fatalf("actions = %#v", base.Spec.Actions)
	}
	if base.Status.Count != 1000 || !base.Status.CountCapped || base.Status.AffectedFilesCount != 2 ||
		base.Status.AffectingRowsCount == nil || *base.Status.AffectingRowsCount != 1234 {
		t.Fatalf("status counts = %#v", base.Status)
	}
	if got := APIResponseToManifest(resp, nil, 0).Status.AffectingRowsCount; got != nil {
		t.Fatalf("no stats read, affecting_rows_count = %d, want absent", *got)
	}

	verbose := APIResponseToManifest(resp, stats, 1)
	if verbose.Spec.DataSourceModelID != 13 || verbose.Spec.MigrationModelID != 17 || verbose.Spec.FieldValue == nil || *verbose.Spec.FieldValue != "bad" {
		t.Fatalf("verbosity 1 omitted identifiers or values: %#v", verbose.Spec)
	}
	if verbose.Spec.Whodunit == nil || verbose.Spec.Whodunit.ID != whodunitID.String() {
		t.Fatalf("whodunit mapping = %#v", verbose.Spec.Whodunit)
	}
	if !reflect.DeepEqual(verbose.Spec.IngestionJobIDs, []int{29, 31}) {
		t.Fatalf("ingestion job IDs = %#v", verbose.Spec.IngestionJobIDs)
	}
	wantJobs := []AlertIngestionJob{
		{ID: 29, Kind: "file", State: "done", StoredItemID: alertPtr(41), FileName: "orders.csv"},
		{ID: 31, Kind: "dry_run", State: "done", IsDryRun: true, DryRunJobID: alertPtr(43)},
	}
	if !reflect.DeepEqual(verbose.Spec.IngestionJobs, wantJobs) {
		t.Fatalf("ingestion jobs = %#v", verbose.Spec.IngestionJobs)
	}
}

// A 0 from the stats read is a count, not an absence: the key stays.
func TestAffectingRowsCountKeepsAZero(t *testing.T) {
	a := sampleAlertFull()
	out, err := yaml.Marshal(APIResponseToManifest(&a, &IssueStats{AffectingRowsCount: alertPtr(0)}, 0))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "affecting_rows_count: 0") {
		t.Errorf("want affecting_rows_count: 0, got:\n%s", out)
	}
}

// The raw dump carries the stats response beside the alert's, under wire
// names, and leaves the alert's own response as sent.
func TestAPIResponseToRawManifestCarriesTheStatsBesideTheResponse(t *testing.T) {
	a := sampleAlertFull()
	m, err := APIResponseToRawManifest(&a, &IssueStats{AffectingRowsCount: alertPtr(1234)})
	if err != nil {
		t.Fatalf("APIResponseToRawManifest: %v", err)
	}
	stats, ok := m.RawStats.(map[string]interface{})
	if !ok || stats["affecting_rows_count"] != int64(1234) {
		t.Fatalf("raw_stats = %#v, want affecting_rows_count 1234", m.RawStats)
	}
	if _, leaked := m.RawResponse.(map[string]interface{})["affecting_rows_count"]; leaked {
		t.Errorf("raw_response gained affecting_rows_count: %#v", m.RawResponse)
	}

	m, err = APIResponseToRawManifest(&a, nil)
	if err != nil {
		t.Fatalf("APIResponseToRawManifest: %v", err)
	}
	if m.RawStats != nil {
		t.Errorf("no stats read, raw_stats = %#v, want nil", m.RawStats)
	}
}

func TestBuildBodyInfoPreservesFieldsAndEmptySemantics(t *testing.T) {
	if got := buildBodyInfo(&AlertFull{}); got != nil {
		t.Fatalf("nil body produced %#v", got)
	}
	if got := buildBodyInfo(&AlertFull{Body: &api.AlertBodySchema{}}); got != nil {
		t.Fatalf("empty body produced %#v", got)
	}

	recommended := interface{}("utf-8")
	body := &api.AlertBodySchema{
		FileName:                     alertPtr("data.csv"),
		AnomalyScore:                 alertPtr(85),
		AllowedValues:                alertPtr([]string{"a", "b"}),
		TopSuggestedValues:           alertPtr([]api.ValueRecommendationTinySchema{{Value: "a", Strong: alertPtr(true)}}),
		RulePath:                     alertPtr([]string{"schema", "email"}),
		DetectedHeaderLineNo:         alertPtr(3),
		DialectInfo:                  &api.DialectInfo{CsvDelimiter: alertPtr(","), CsvQuotechar: alertPtr("\"")},
		DuplicatesHeaders:            alertPtr(map[string]string{"email": "email_2"}),
		InvalidChars:                 alertPtr([]string{"?"}),
		RecommendedSettingsFieldName: alertPtr("encoding"),
		RecommendedSettingsValue:     &recommended,
	}

	got := buildBodyInfo(&AlertFull{Body: body})
	if got == nil {
		t.Fatal("populated body returned nil")
	}
	if got.FileName != "data.csv" || got.AnomalyScore == nil || *got.AnomalyScore != 85 {
		t.Fatalf("scalar body mapping = %#v", got)
	}
	if !reflect.DeepEqual(got.AllowedValues, []string{"a", "b"}) || !reflect.DeepEqual(got.RulePath, []string{"schema", "email"}) {
		t.Fatalf("collection body mapping = %#v", got)
	}
	if len(got.TopSuggestedValues) != 1 || got.TopSuggestedValues[0].Value != "a" || !boolValue(got.TopSuggestedValues[0].Strong) {
		t.Fatalf("suggestion mapping = %#v", got.TopSuggestedValues)
	}
	if got.DialectInfo == nil || got.DialectInfo.Delimiter == nil || *got.DialectInfo.Delimiter != "," {
		t.Fatalf("dialect mapping = %#v", got.DialectInfo)
	}
	if got.RecommendedSettingsValue != "utf-8" {
		t.Fatalf("recommended settings value = %#v", got.RecommendedSettingsValue)
	}

	// A present dialect object counts as body content even if each member is nil.
	if got := buildBodyInfo(&AlertFull{Body: &api.AlertBodySchema{DialectInfo: &api.DialectInfo{}}}); got == nil {
		t.Fatal("present empty dialect was discarded")
	}
}

// related_alert_ids identify other alert resources this alert is about. They
// are actionable context rather than diagnostic detail, so describe carries
// them at the default tier while keeping their original body nesting.
func TestRelatedAlertIDsAreShownAtDefaultVerbosity(t *testing.T) {
	ids := []int{7, 9}
	a := sampleAlertFull()
	a.Body = &api.AlertBodySchema{RelatedAlertIds: &ids}

	for _, verbosity := range []int{0, 1} {
		manifest := APIResponseToManifest(&a, nil, verbosity)
		if manifest.Spec.Body == nil || !reflect.DeepEqual(manifest.Spec.Body.RelatedAlertIDs, ids) {
			t.Errorf("verbosity %d body = %#v, want related ids %v", verbosity, manifest.Spec.Body, ids)
		}
	}

	for name, body := range map[string]*api.AlertBodySchema{
		"absent": nil,
		"nil":    {RelatedAlertIds: nil},
		"empty":  {RelatedAlertIds: &[]int{}},
	} {
		t.Run(name, func(t *testing.T) {
			a.Body = body
			if got := APIResponseToManifest(&a, nil, 0).Spec.Body; got != nil {
				t.Errorf("default body = %#v, want nil", got)
			}
		})
	}
}

// prefix_candidates are the values accept_gtin_prefix takes as its required
// new_value, so describe carries them at the default tier beside
// related_alert_ids, in the backend's order, and -v keeps them.
func TestPrefixCandidatesAreShownAtDefaultVerbosity(t *testing.T) {
	want := []string{"061414100", "06141410", "0614141"}
	cands := append([]string(nil), want...)
	a := sampleAlertFull()
	a.Body = &api.AlertBodySchema{PrefixCandidates: &cands, FileName: alertPtr("upcs.csv")}

	base := APIResponseToManifest(&a, nil, 0)
	cands[0] = "mutated"
	if base.Spec.Body == nil || !reflect.DeepEqual(base.Spec.Body.PrefixCandidates, want) {
		t.Fatalf("default body = %#v, want prefix candidates %q", base.Spec.Body, want)
	}
	if base.Spec.Body.FileName != "" {
		t.Errorf("default tier leaked a -v body field: %#v", base.Spec.Body)
	}

	cands[0] = want[0]
	verbose := APIResponseToManifest(&a, nil, 1)
	if verbose.Spec.Body == nil || !reflect.DeepEqual(verbose.Spec.Body.PrefixCandidates, want) || verbose.Spec.Body.FileName != "upcs.csv" {
		t.Errorf("-v body = %#v, want prefix candidates %q beside file_name", verbose.Spec.Body, want)
	}

	// A body holding nothing else still counts as -v body content.
	a.Body = &api.AlertBodySchema{PrefixCandidates: &cands}
	if got := APIResponseToManifest(&a, nil, 1).Spec.Body; got == nil || !reflect.DeepEqual(got.PrefixCandidates, want) {
		t.Errorf("-v body = %#v, want prefix candidates %q", got, want)
	}
}

// The two default-tier body keys share one body: adding the candidates must
// not replace the related ids.
func TestPrefixCandidatesKeepRelatedAlertIDsAtDefaultVerbosity(t *testing.T) {
	ids := []int{7, 9}
	cands := []string{"0614141"}
	a := sampleAlertFull()
	a.Body = &api.AlertBodySchema{RelatedAlertIds: &ids, PrefixCandidates: &cands}

	got := APIResponseToManifest(&a, nil, 0).Spec.Body
	if got == nil || !reflect.DeepEqual(got.RelatedAlertIDs, ids) || !reflect.DeepEqual(got.PrefixCandidates, cands) {
		t.Errorf("default body = %#v, want related ids %v and prefix candidates %q", got, ids, cands)
	}
}

// Every issue type but upc_unknown_gtin_prefix has no candidates, and its
// default view must not gain an empty body.
func TestPrefixCandidatesAbsentLeaveTheDefaultBodyOut(t *testing.T) {
	for name, body := range map[string]*api.AlertBodySchema{
		"absent": nil,
		"nil":    {PrefixCandidates: nil, FileName: alertPtr("d.csv")},
		"empty":  {PrefixCandidates: &[]string{}, FileName: alertPtr("d.csv")},
	} {
		t.Run(name, func(t *testing.T) {
			a := sampleAlertFull()
			a.Body = body
			m := APIResponseToManifest(&a, nil, 0)
			if m.Spec.Body != nil {
				t.Fatalf("default body = %#v, want nil", m.Spec.Body)
			}
			out, err := yaml.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(out), "body:") || strings.Contains(string(out), "prefix_candidates") {
				t.Errorf("default render gained a body:\n%s", out)
			}

			if body == nil {
				return
			}
			out, err = yaml.Marshal(APIResponseToManifest(&a, nil, 1))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(out), "prefix_candidates") {
				t.Errorf("-v render printed an empty prefix_candidates:\n%s", out)
			}
		})
	}
}

// The rendered key is the wire name in both encoders, and a leading-zero
// candidate reads back as the same string rather than a number.
func TestPrefixCandidatesRenderUnderTheWireKey(t *testing.T) {
	cands := []string{"061414100", "06141410", "0614141"}
	a := sampleAlertFull()
	a.Body = &api.AlertBodySchema{PrefixCandidates: &cands}
	m := APIResponseToManifest(&a, nil, 0)

	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("yaml marshal: %v", err)
	}
	var fromYAML struct {
		Spec struct {
			Body map[string]interface{} `yaml:"body"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(out, &fromYAML); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if got := fromYAML.Spec.Body["prefix_candidates"]; !reflect.DeepEqual(got, []interface{}{"061414100", "06141410", "0614141"}) {
		t.Errorf("yaml spec.body.prefix_candidates = %#v, want the three candidates as strings:\n%s", got, out)
	}

	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"prefix_candidates":["061414100","06141410","0614141"]`) {
		t.Errorf("json lacks prefix_candidates: %s", raw)
	}
}

// A severe_rule_bug alert exists to say a specific rule crashed. Before this,
// `describe alert` printed everything about such an alert except which rule —
// dataset_rule_name reached the terminal only in the -vv raw dump.
func TestAlertSpecNamesTheRuleWhenTheBackendSendsIt(t *testing.T) {
	a := sampleAlertFull()
	a.DatasetRuleID = alertPtr(uuid.MustParse("44444444-4444-4444-4444-444444444444"))
	a.DatasetRuleName = alertPtr("row_count_check")

	// Tier 0: the point is that this needs no -v.
	m := APIResponseToManifest(&a, nil, 0)

	if m.Spec.DatasetRuleID != "44444444-4444-4444-4444-444444444444" {
		t.Errorf("dataset_rule_id = %q, want the rule's UUID", m.Spec.DatasetRuleID)
	}
	if m.Spec.DatasetRuleName != "row_count_check" {
		t.Errorf("dataset_rule_name = %q, want row_count_check", m.Spec.DatasetRuleName)
	}
}

// Most alert types are not rule findings at all — header sniffing, sheet
// choice, datetime inference and dozens more come from ingestion. An empty
// dataset_rule_id key on every one of them is noise in a manifest read by eye.
func TestAlertSpecOmitsTheRuleWhenTheBackendSendsNone(t *testing.T) {
	a := sampleAlertFull()
	a.DatasetRuleID = nil
	a.DatasetRuleName = nil

	m := APIResponseToManifest(&a, nil, 0)

	if m.Spec.DatasetRuleID != "" || m.Spec.DatasetRuleName != "" {
		t.Errorf("rule fields should be empty, got %q / %q", m.Spec.DatasetRuleID, m.Spec.DatasetRuleName)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "dataset_rule_id") {
		t.Errorf("omitempty should have dropped the key:\n%s", out)
	}
}

// resolve_after_migration is the whole explanation for an alert with no
// actions, so it must be visible without -v. It is omitempty on a bool, so
// this only ever ADDS a line — the same "shown in all tiers when present"
// treatment spec.dependent_alert already gets.
func TestResolveAfterMigrationIsShownAtDefaultVerbosity(t *testing.T) {
	resp := sampleAlertFull()
	parked := true
	resp.ResolveAfterMigration = &parked

	spec := buildAlertSpec(&resp, 0)
	if !spec.ResolveAfterMigration {
		t.Error("spec.resolve_after_migration must be populated at verbosity 0")
	}
}

func TestResolveAfterMigrationIsAbsentWhenFalse(t *testing.T) {
	resp := sampleAlertFull()
	no := false
	resp.ResolveAfterMigration = &no

	out, err := yaml.Marshal(buildAlertSpec(&resp, 0))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "resolve_after_migration") {
		t.Errorf("omitempty must drop it when false:\n%s", out)
	}
}

// declared_severity is the one body key whose bare value misreads where it
// lands: on an ALERT it can only say `warning`, which looks like the alert's
// own severity until the footer's note says whose it is. The body tier carries
// the value so `-o json` has it; this pins that it is carried at all, and that
// a body holding nothing else still counts as body content.
func TestBuildBodyInfoCarriesDeclaredSeverity(t *testing.T) {
	sev := api.IssueSeverityWarning
	got := buildBodyInfo(&AlertFull{Body: &api.AlertBodySchema{DeclaredSeverity: &sev}})
	if got == nil {
		t.Fatal("a body carrying only declared_severity was discarded")
	}
	if got.DeclaredSeverity != "warning" {
		t.Fatalf("DeclaredSeverity = %q, want %q", got.DeclaredSeverity, "warning")
	}

	// Absent stays absent: the field is omitempty, and the backend sets it
	// only when the policy overrode something, so an empty string here would
	// print a key on every alert that ever had a body.
	if got := buildBodyInfo(&AlertFull{Body: &api.AlertBodySchema{FileName: alertPtr("d.csv")}}); got.DeclaredSeverity != "" {
		t.Fatalf("DeclaredSeverity = %q on a body without one", got.DeclaredSeverity)
	}
}

// The -v tier is where body fields live, so the value must not leak into the
// default one -- and must not be dropped from -v either. Both directions,
// because a field added to the wrong populate* helper fails only one of them.
func TestDeclaredSeverityIsATierTwoBodyField(t *testing.T) {
	sev := api.IssueSeverityWarning
	a := sampleAlertFull()
	a.Body = &api.AlertBodySchema{DeclaredSeverity: &sev}

	if base := APIResponseToManifest(&a, nil, 0); base.Spec.Body != nil {
		t.Errorf("default tier exposed the body: %#v", base.Spec.Body)
	}
	verbose := APIResponseToManifest(&a, nil, 1)
	if verbose.Spec.Body == nil || verbose.Spec.Body.DeclaredSeverity != "warning" {
		t.Errorf("-v tier body = %#v, want declared_severity warning", verbose.Spec.Body)
	}
}

// The recommendation is a TRIPLE and qctl used to print two thirds of it. The
// module is what says where the field lives, and the two producers disagree:
// migration_frozen recommends `migration_policy` on the DataSet, bad_settings
// recommends `max_boundary_element_length` on the SettingsModel. Both land in
// one `kind: Table` manifest, at different depths, so the field name alone
// does not say which half to edit.
func TestBuildBodyInfoCarriesTheRecommendedModule(t *testing.T) {
	value := interface{}("ask_user")
	module := api.SettingsModulesDataSet
	got := buildBodyInfo(&AlertFull{Body: &api.AlertBodySchema{
		RecommendedSettingsFieldName:      alertPtr("migration_policy"),
		RecommendedSettingsValue:          &value,
		RecommendedSettingsModuleToUpdate: &module,
	}})
	if got == nil {
		t.Fatal("a body carrying only the recommendation was discarded")
	}
	if got.RecommendedSettingsModuleToUpdate != "DataSet" {
		t.Errorf("module = %q, want DataSet", got.RecommendedSettingsModuleToUpdate)
	}
	if got.RecommendedSettingsFieldName != "migration_policy" || got.RecommendedSettingsValue != "ask_user" {
		t.Errorf("the other two thirds regressed: %#v", got)
	}

	// Absent stays absent: most alerts carry no recommendation at all.
	if got := buildBodyInfo(&AlertFull{Body: &api.AlertBodySchema{FileName: alertPtr("d.csv")}}); got.RecommendedSettingsModuleToUpdate != "" {
		t.Errorf("module = %q on a body without a recommendation", got.RecommendedSettingsModuleToUpdate)
	}
}

func TestAlertStatusTimesAreRFC3339InUTCAndAbsentWhenUnset(t *testing.T) {
	createdAt := time.Date(2025, 1, 15, 12, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	resp := &AlertFull{ID: 1, CreatedAt: &createdAt, ExpiredAt: &time.Time{}}

	status := APIResponseToManifest(resp, nil, 1).Status
	if status.CreatedAt != "2025-01-15T10:00:00Z" {
		t.Errorf("created_at = %q, want 2025-01-15T10:00:00Z", status.CreatedAt)
	}
	// A nil and a zero time both leave the field empty, so omitempty drops it.
	if status.ResolvedAt != "" || status.ExpiredAt != "" {
		t.Errorf("resolved_at = %q, expired_at = %q, want both empty", status.ResolvedAt, status.ExpiredAt)
	}
	out, err := yaml.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "resolved_at") || strings.Contains(string(out), "expired_at") {
		t.Errorf("unset times reached the document:\n%s", out)
	}
}

// A user's role reaches assigned_user, whodunit and submitted_by in both
// encoders when the backend sends it, and the key is absent when it does not.
func TestAlertManifestCarriesUserRoles(t *testing.T) {
	user := func(n int, role *api.RoleEnum) api.UserInfoTinySchema {
		return api.UserInfoTinySchema{
			ID:    uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", n)),
			Email: fmt.Sprintf("u%d@x.io", n),
			Role:  role,
		}
	}
	build := func(role *api.RoleEnum) *AlertManifestWithStatus {
		a := sampleAlertFull()
		assigned, whodunit := user(1, role), user(2, role)
		a.AssignedUser = &assigned
		a.Whodunit = &whodunit
		a.UserSubmittedIssues = &[]api.UserSubmittedIssueReadSchema{{
			ID:          uuid.MustParse("00000000-0000-4000-8000-000000000009"),
			RowID:       1042,
			FieldNames:  []string{"price"},
			SubmittedBy: user(3, role),
			CreatedAt:   time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		}}
		return APIResponseToManifest(&a, nil, 1)
	}

	t.Run("set", func(t *testing.T) {
		m := build(alertPtr(api.RoleEnumExternal))
		if m.Spec.AssignedUser.Role != "external" || m.Spec.Whodunit.Role != "external" || m.Spec.UserSubmittedIssues[0].SubmittedBy.Role != "external" {
			t.Fatalf("roles = %q, %q, %q", m.Spec.AssignedUser.Role, m.Spec.Whodunit.Role, m.Spec.UserSubmittedIssues[0].SubmittedBy.Role)
		}
		out, err := yaml.Marshal(m)
		if err != nil {
			t.Fatalf("yaml marshal: %v", err)
		}
		if got := strings.Count(string(out), "role: external"); got != 3 {
			t.Errorf("yaml has %d role keys, want 3:\n%s", got, out)
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("json marshal: %v", err)
		}
		if got := strings.Count(string(raw), `"role":"external"`); got != 3 {
			t.Errorf("json has %d role keys, want 3: %s", got, raw)
		}
	})

	t.Run("nil", func(t *testing.T) {
		m := build(nil)
		out, err := yaml.Marshal(m)
		if err != nil {
			t.Fatalf("yaml marshal: %v", err)
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("json marshal: %v", err)
		}
		if strings.Contains(string(out), "role:") || strings.Contains(string(raw), `"role"`) {
			t.Errorf("a nil role rendered a key:\n%s\n%s", out, raw)
		}
	})
}

// status.affected_files names each linked file once, so `alert resolve
// --action trash_file --file-id` has ids to choose from; a file linked by
// several rows is not listed twice.
func TestAPIResponseToManifestListsEachAffectedFileOnce(t *testing.T) {
	links := []api.StoredItemToAlertSchema{
		{StoredItemID: 5, FileName: alertPtr("jan.csv"), RowNumber: alertPtr(3)},
		{StoredItemID: 5, FileName: alertPtr("jan.csv"), RowNumber: alertPtr(9)},
		{StoredItemID: 6},
	}
	got := APIResponseToManifest(&AlertFull{ID: 9, StoredItemsToAlerts: &links}, nil, 0).Status.AffectedFiles
	want := []AlertAffectedFile{{ID: 5, FileName: "jan.csv"}, {ID: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("affected_files = %+v, want %+v", got, want)
	}
}
