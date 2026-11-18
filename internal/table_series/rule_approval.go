package table_series

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/terminal"
)

// RuleApproval is a series' rule-set approval state and the response body it
// was decoded from.
type RuleApproval struct {
	Status api.SeriesRuleApprovalStatusSchema
	Body   []byte
}

var fingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// NormalizeFingerprint lowercases fingerprint and rejects anything but 64
// hex digits with exit 2.
func NormalizeFingerprint(fingerprint string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(fingerprint))
	if !fingerprintPattern.MatchString(normalized) {
		return "", qerrors.NewBadUsagef("--fingerprint must be the 64-hex-digit fingerprint that get table-series --rule-approval prints, got %q",
			terminal.Sanitize(fingerprint))
	}
	return normalized, nil
}

// GetRuleApproval reads a series' current rule set and its approvals.
func (c *Client) GetRuleApproval(token string, id uuid.UUID) (*RuleApproval, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetTableSeriesRuleApprovalWithResponse(context.Background(), c.orgID, id,
		&api.GetTableSeriesRuleApprovalParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "get rule-set approval failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "get rule-set approval failed")
	}
	return decodeRuleApproval(resp.JSON200, resp.Body)
}

// ApproveRules approves the series rule set whose fingerprint is fingerprint.
func (c *Client) ApproveRules(token string, id uuid.UUID, fingerprint string) (*RuleApproval, error) {
	apiClient, err := c.newAPIClient(token)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.ApproveTableSeriesRulesWithResponse(context.Background(), c.orgID, id,
		&api.ApproveTableSeriesRulesParams{}, api.SeriesRuleApprovalRequestSchema{Fingerprint: fingerprint})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "approve series rules failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "approve series rules failed")
	}
	return decodeRuleApproval(resp.JSON200, resp.Body)
}

// decodeRuleApproval decodes body again with jsonnum, so rule-set integers
// such as table IDs stay exact.
func decodeRuleApproval(parsed *api.SeriesRuleApprovalStatusSchema, body []byte) (*RuleApproval, error) {
	if parsed == nil {
		return nil, fmt.Errorf("unexpected empty rule-set approval response")
	}
	r := &RuleApproval{Body: body}
	if err := jsonnum.Unmarshal(body, &r.Status); err != nil {
		return nil, fmt.Errorf("decode rule-set approval response: %w", err)
	}
	return r, nil
}

// WriteRuleApproval writes r's response body as JSON or YAML, keeping its
// integers exact.
func WriteRuleApproval(w io.Writer, format string, r *RuleApproval) error {
	if output.Format(format) == output.FormatYAML {
		return output.WriteJSONAsYAML(w, r.Body)
	}
	return output.WriteIndentedJSON(w, r.Body)
}

// RuleCount is the number of rules across every table of the rule set.
func RuleCount(status *api.SeriesRuleApprovalStatusSchema) int {
	count := 0
	for _, table := range status.RuleSet {
		count += len(tableRules(table))
	}
	return count
}

func tableRules(table map[string]interface{}) []interface{} {
	rules, _ := table["rules"].([]interface{})
	return rules
}

// RuleApprovalColumns are the default columns for a series' rule-set approval.
const RuleApprovalColumns = "approved,fingerprint,tables,rules,approved_by,approved_at,approvals"

// RuleApprovalDisplay is a series' rule-set approval as one table row.
type RuleApprovalDisplay struct {
	ID          string `json:"id"`
	Approved    bool   `json:"approved"`
	Fingerprint string `json:"fingerprint"`
	Tables      int    `json:"tables"`
	Rules       int    `json:"rules"`
	ApprovedBy  string `json:"approved_by"`
	ApprovedAt  string `json:"approved_at"`
	Approvals   int    `json:"approvals"`
}

// RuleApprovalRows returns series id's approval state, with the latest
// approval's approver and time, or "-" without one.
func RuleApprovalRows(id uuid.UUID, status *api.SeriesRuleApprovalStatusSchema) []RuleApprovalDisplay {
	row := RuleApprovalDisplay{
		ID: id.String(), Approved: status.Approved, Fingerprint: status.Fingerprint,
		Tables: len(status.RuleSet), Rules: RuleCount(status),
		ApprovedBy: "-", ApprovedAt: "-", Approvals: len(status.Approvals),
	}
	if len(status.Approvals) > 0 {
		row.ApprovedBy = status.Approvals[0].ApprovedByID.String()
		row.ApprovedAt = status.Approvals[0].ApprovedAt.UTC().Format(time.RFC3339)
	}
	return []RuleApprovalDisplay{row}
}

// FormatRuleApprovalPreview describes the rule set an approval of series ref
// would approve: its fingerprint, each table's rule count and the latest
// approval.
func FormatRuleApprovalPreview(ref string, status *api.SeriesRuleApprovalStatusSchema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Rule set of table series %q\n", terminal.Sanitize(ref))
	fmt.Fprintf(&b, "  Fingerprint: %s\n", terminal.Sanitize(status.Fingerprint))
	fmt.Fprintf(&b, "  Tables:      %d, %d rules\n", len(status.RuleSet), RuleCount(status))
	for _, table := range status.RuleSet {
		fmt.Fprintf(&b, "    table %s: %d rules\n", tableID(table["dataset_id"]), len(tableRules(table)))
	}
	if len(status.Approvals) == 0 {
		b.WriteString("  Last approval: none\n")
		return b.String()
	}
	latest := status.Approvals[0]
	fmt.Fprintf(&b, "  Last approval: %s by %s, fingerprint %s\n", latest.ApprovedAt.UTC().Format(time.RFC3339),
		latest.ApprovedByID, terminal.Sanitize(latest.Fingerprint))
	return b.String()
}

func tableID(value interface{}) string {
	switch v := value.(type) {
	case int64:
		return strconv.FormatInt(v, 10)
	case nil:
		return "-"
	default:
		return terminal.Sanitize(fmt.Sprint(v))
	}
}
