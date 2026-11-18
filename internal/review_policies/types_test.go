package review_policies

import (
	"strings"
	"testing"

	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

func boolP(b bool) *bool { return &b }
func intP(i int) *int    { return &i }

func TestRuleReviewPolicyValidate(t *testing.T) {
	tests := []struct {
		name    string
		spec    RuleReviewPolicySpec
		wantErr string // substring of first error; "" means valid
	}{
		{"empty spec is valid", RuleReviewPolicySpec{}, ""},
		{"full valid spec", RuleReviewPolicySpec{
			DatasetID:             42,
			RequiresReview:        boolP(true),
			MinReviewers:          intP(2),
			DesignatedReviewerIDs: []string{"7f3c2a9e-1111-2222-3333-444455556666"},
		}, ""},
		{"min_reviewers too low", RuleReviewPolicySpec{MinReviewers: intP(0)}, "between 1 and 10"},
		{"min_reviewers too high", RuleReviewPolicySpec{MinReviewers: intP(11)}, "between 1 and 10"},
		{"negative dataset_id", RuleReviewPolicySpec{DatasetID: -1}, "positive table ID"},
		{"bad reviewer uuid", RuleReviewPolicySpec{DesignatedReviewerIDs: []string{"not-a-uuid"}}, "invalid UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := RuleReviewPolicyManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "RuleReviewPolicy",
				Metadata:   ReviewPolicyMetadata{Name: "p"},
				Spec:       tt.spec,
			}
			errs := m.Validate()
			if tt.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected valid, got %v", errs)
				}
				return
			}
			if len(errs) == 0 {
				t.Fatalf("expected error containing %q, got none", tt.wantErr)
			}
			if !strings.Contains(errs[0].Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, errs[0].Error())
			}
		})
	}
}

func TestDataReviewPolicyValidate(t *testing.T) {
	tests := []struct {
		name    string
		spec    DataReviewPolicySpec
		wantErr string
	}{
		{"requires_review missing", DataReviewPolicySpec{}, "requires_review is required"},
		{"valid minimal", DataReviewPolicySpec{RequiresReview: boolP(false)}, ""},
		{"min_reviewers zero", DataReviewPolicySpec{RequiresReview: boolP(true), MinReviewers: intP(0)}, "at least 1"},
		{"negative dataset_id", DataReviewPolicySpec{RequiresReview: boolP(true), DatasetID: -3}, "positive table ID"},
		{"bad reviewer uuid", DataReviewPolicySpec{RequiresReview: boolP(true), DesignatedReviewerIDs: []string{"xyz"}}, "invalid UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := DataReviewPolicyManifest{
				APIVersion: pkgmanifest.APIVersionV1,
				Kind:       "DataReviewPolicy",
				Metadata:   ReviewPolicyMetadata{Name: "p"},
				Spec:       tt.spec,
			}
			errs := m.Validate()
			if tt.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected valid, got %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs[0].Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, errs)
			}
		})
	}
}

func TestRuleReviewPolicyToRequestBody(t *testing.T) {
	m := RuleReviewPolicyManifest{
		Spec: RuleReviewPolicySpec{
			RequiresReview:        boolP(true),
			MinReviewers:          intP(3),
			DesignatedReviewerIDs: []string{"7f3c2a9e-1111-2222-3333-444455556666"},
		},
	}
	body, err := m.ToRequestBody()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.RequiresReview == nil || !*body.RequiresReview {
		t.Errorf("requires_review not mapped")
	}
	if body.MinReviewers == nil || *body.MinReviewers != 3 {
		t.Errorf("min_reviewers not mapped")
	}
	if body.DesignatedReviewerIds == nil || len(*body.DesignatedReviewerIds) != 1 {
		t.Errorf("designated_reviewer_ids not mapped")
	}
	// Omitted fields stay nil so they are absent from the JSON payload.
	if body.DismissStaleReviews != nil || body.RequireDryRun != nil ||
		body.RequireDryRunSuccess != nil || body.AutoEnableOnApproval != nil {
		t.Errorf("omitted fields must remain nil, got %+v", body)
	}
}

func TestDataReviewPolicyToRequestBody(t *testing.T) {
	m := DataReviewPolicyManifest{
		Spec: DataReviewPolicySpec{RequiresReview: boolP(true)},
	}
	body, err := m.ToRequestBody()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !body.RequiresReview {
		t.Errorf("requires_review not mapped")
	}
	if body.MinReviewers != nil || body.DesignatedReviewerIds != nil || body.AutoReleaseOnApproval != nil {
		t.Errorf("omitted fields must remain nil, got %+v", body)
	}
}

func TestStrictUnmarshalRejectsUnknownSpecField(t *testing.T) {
	doc := `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: p
spec:
  requires_reviews: true
`
	var m RuleReviewPolicyManifest
	err := pkgmanifest.StrictUnmarshal([]byte(doc), &m)
	if err == nil {
		t.Fatal("expected unknown-field error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown field") && !strings.Contains(err.Error(), "not found in type") {
		t.Fatalf("expected unknown-field error, got: %v", err)
	}
}

func TestStrictUnmarshalAcceptsStatusBlock(t *testing.T) {
	// "get -o yaml" output includes a status block; apply must parse it.
	doc := `
apiVersion: qluster.ai/v1
kind: RuleReviewPolicy
metadata:
  name: p
spec:
  requires_review: true
status:
  source: organization
  policy_id: 7f3c2a9e-1111-2222-3333-444455556666
`
	var m RuleReviewPolicyManifest
	if err := pkgmanifest.StrictUnmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("status block must round-trip: %v", err)
	}
	if m.Status == nil || m.Status.Source != "organization" {
		t.Fatalf("status not parsed: %+v", m.Status)
	}
}

func TestDataReviewPolicyToRequestBodyRequiresReviewNil(t *testing.T) {
	m := DataReviewPolicyManifest{Spec: DataReviewPolicySpec{}}
	if _, err := m.ToRequestBody(); err == nil {
		t.Fatal("expected error for nil requires_review, got nil")
	}
}

func TestRuleReviewPolicyToRequestBodyInvalidUUID(t *testing.T) {
	m := RuleReviewPolicyManifest{
		Spec: RuleReviewPolicySpec{DesignatedReviewerIDs: []string{"not-a-uuid"}},
	}
	if _, err := m.ToRequestBody(); err == nil {
		t.Fatal("expected error for invalid UUID, got nil")
	}
}

func TestDataReviewPolicyToRequestBodyInvalidUUID(t *testing.T) {
	m := DataReviewPolicyManifest{
		Spec: DataReviewPolicySpec{
			RequiresReview:        boolP(true),
			DesignatedReviewerIDs: []string{"not-a-uuid"},
		},
	}
	if _, err := m.ToRequestBody(); err == nil {
		t.Fatal("expected error for invalid UUID, got nil")
	}
}
