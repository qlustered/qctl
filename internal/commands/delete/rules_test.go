package delete

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/rule_versions"
)

func uuidPtr(s string) *openapi_types.UUID {
	u := openapi_types.UUID(uuid.MustParse(s))
	return &u
}

const testRevisionUUID = "aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"

// ShortID for testRevisionUUID: first 8 hex chars of "aaaa0000bbbbccccddddeeeeeeee0001"
const testRevisionShortID = "aaaa0000"

func TestDisplayUnsubmitResult_DeletedShortID(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "2 rules deleted",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "email_validator", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
			{Name: "phone_validator", Release: "2.0.0", RevisionID: uuidPtr("aaaa0000-bbbb-cccc-dddd-eeeeeeee0002")},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "2 rules deleted") {
		t.Error("Expected message in output")
	}
	if !strings.Contains(out, "Deleted:") {
		t.Error("Expected 'Deleted:' header")
	}
	if !strings.Contains(out, "REVISION ID") {
		t.Error("Expected REVISION ID column when revision IDs are present")
	}
	if !strings.Contains(out, testRevisionShortID) {
		t.Errorf("Expected short ID %s in output, got:\n%s", testRevisionShortID, out)
	}
	// Full UUID should NOT appear at verbosity 0
	if strings.Contains(out, testRevisionUUID) {
		t.Error("Full UUID should not appear at verbosity 0")
	}
}

func TestDisplayUnsubmitResult_DeletedFullIDWithVerbose(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "1 rule deleted",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "email_validator", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 1)
	out := buf.String()

	if !strings.Contains(out, testRevisionUUID) {
		t.Errorf("Expected full UUID %s at verbosity >= 1, got:\n%s", testRevisionUUID, out)
	}
}

func TestDisplayUnsubmitResult_DeletedWithoutRevisionID(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "1 rule deleted",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "email_validator", Release: "1.0.0", RevisionID: nil},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "Deleted:") {
		t.Error("Expected 'Deleted:' header")
	}
	if strings.Contains(out, "REVISION ID") {
		t.Error("REVISION ID column should be hidden when all revision IDs are nil")
	}
	if !strings.Contains(out, "email_validator") {
		t.Error("Expected email_validator in output")
	}
	if !strings.Contains(out, "1.0.0") {
		t.Error("Expected version in output")
	}
}

func TestDisplayUnsubmitResult_NotFound(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "0 rules deleted",
		NotFound: []rule_versions.UnsubmitResultItem{
			{Name: "missing_rule", Release: "1.0.0", RevisionID: nil},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "Not found:") {
		t.Error("Expected 'Not found:' header")
	}
	if strings.Contains(out, "REVISION ID") {
		t.Error("REVISION ID column should be hidden for not-found items with nil revision IDs")
	}
	if !strings.Contains(out, "missing_rule") {
		t.Error("Expected missing_rule in output")
	}
}

func TestDisplayUnsubmitResult_SkippedGroups(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "0 rules deleted",
		Skipped: []rule_versions.UnsubmitSkippedGroup{
			{
				Reason: "rule is enabled",
				Rules: []rule_versions.UnsubmitResultItem{
					{Name: "active_rule", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
				},
			},
			{
				Reason: "no metadata.release",
				Rules: []rule_versions.UnsubmitResultItem{
					{Name: "bad_rule", Release: "unknown", RevisionID: nil},
				},
			},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "Skipped:") {
		t.Error("Expected 'Skipped:' header")
	}
	if !strings.Contains(out, "Reason: rule is enabled") {
		t.Error("Expected first reason")
	}
	if !strings.Contains(out, "Reason: no metadata.release") {
		t.Error("Expected second reason")
	}
	if !strings.Contains(out, "active_rule") {
		t.Error("Expected active_rule in output")
	}
	if !strings.Contains(out, "bad_rule") {
		t.Error("Expected bad_rule in output")
	}
	// Short ID at verbosity 0
	if !strings.Contains(out, testRevisionShortID) {
		t.Errorf("Expected short ID %s in output", testRevisionShortID)
	}
}

func TestDisplayUnsubmitResult_Mixed(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "1 rule deleted, 1 not found, 1 skipped",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "good_rule", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
		},
		NotFound: []rule_versions.UnsubmitResultItem{
			{Name: "gone_rule", Release: "2.0.0", RevisionID: nil},
		},
		Skipped: []rule_versions.UnsubmitSkippedGroup{
			{
				Reason: "rule is builtin",
				Rules: []rule_versions.UnsubmitResultItem{
					{Name: "builtin_rule", Release: "1.0.0", RevisionID: uuidPtr("aaaa0000-bbbb-cccc-dddd-eeeeeeee0003")},
				},
			},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "Deleted:") {
		t.Error("Expected 'Deleted:' section")
	}
	if !strings.Contains(out, "Not found:") {
		t.Error("Expected 'Not found:' section")
	}
	if !strings.Contains(out, "Skipped:") {
		t.Error("Expected 'Skipped:' section")
	}
	if !strings.Contains(out, "good_rule") {
		t.Error("Expected good_rule in output")
	}
	if !strings.Contains(out, "gone_rule") {
		t.Error("Expected gone_rule in output")
	}
	if !strings.Contains(out, "builtin_rule") {
		t.Error("Expected builtin_rule in output")
	}
}

func TestDisplayUnsubmitResult_EmptyResult(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "No rules matched",
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	if !strings.Contains(out, "No rules matched") {
		t.Error("Expected message in output")
	}
	if strings.Contains(out, "Deleted:") {
		t.Error("Should not show Deleted section when empty")
	}
	if strings.Contains(out, "Not found:") {
		t.Error("Should not show Not found section when empty")
	}
	if strings.Contains(out, "Skipped:") {
		t.Error("Should not show Skipped section when empty")
	}
}

func TestDisplayUnsubmitResult_MixedRevisionIDs(t *testing.T) {
	result := &rule_versions.RuleVersionUnsubmitResponse{
		Message: "2 rules deleted",
		Deleted: []rule_versions.UnsubmitResultItem{
			{Name: "rule_a", Release: "1.0.0", RevisionID: uuidPtr(testRevisionUUID)},
			{Name: "rule_b", Release: "2.0.0", RevisionID: nil},
		},
	}

	var buf bytes.Buffer
	displayUnsubmitResult(&buf, result, 0)
	out := buf.String()

	// Should show REVISION ID column because at least one item has it
	if !strings.Contains(out, "REVISION ID") {
		t.Error("Expected REVISION ID column when at least one item has a revision ID")
	}
	if !strings.Contains(out, testRevisionShortID) {
		t.Errorf("Expected short ID %s for rule_a", testRevisionShortID)
	}
}
