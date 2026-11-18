package change_proposals //nolint:revive // Established package-path style in this repo (rule_reviews, privilege_requests).

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/warnings"
	"gopkg.in/yaml.v3"
)

// Document is one AlertResolution or WarningResolution manifest read as a
// proposal payload. Exactly one of Alert and Warning is set.
type Document struct {
	Operation Operation
	TargetID  int
	Alert     *alerts.AlertResolutionSpec
	Warning   *warnings.WarningResolutionSpec
}

// AlertDocument is the document for alertID with spec.
func AlertDocument(alertID int, spec alerts.AlertResolutionSpec) *Document {
	return &Document{Operation: api.ChangeReviewOperationAlertResolve, TargetID: alertID, Alert: &spec}
}

// WarningDocument is the document for warningID with spec.
func WarningDocument(warningID int, spec warnings.WarningResolutionSpec) *Document {
	return &Document{Operation: api.ChangeReviewOperationWarningResolve, TargetID: warningID, Warning: &spec}
}

// ParseDocument strict-parses one AlertResolution or WarningResolution
// document and checks its apiVersion, metadata.id and spec against the
// action's field contract. Every failure is bad usage (exit 2).
func ParseDocument(data []byte) (*Document, error) {
	var envelope struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &envelope); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse manifest")
	}
	if envelope.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef(
			"expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, envelope.APIVersion)
	}
	switch envelope.Kind {
	case alerts.AlertResolutionKind:
		res, err := alerts.ParseResolution(data)
		if err != nil {
			return nil, err
		}
		id, err := res.AlertID()
		if err != nil {
			return nil, err
		}
		if err := res.Spec.Validate(); err != nil {
			return nil, err
		}
		return AlertDocument(id, res.Spec), nil
	case warnings.WarningResolutionKind:
		res, err := warnings.ParseResolution(data)
		if err != nil {
			return nil, err
		}
		id, err := res.WarningID()
		if err != nil {
			return nil, err
		}
		if err := res.Spec.Validate(); err != nil {
			return nil, err
		}
		return WarningDocument(id, res.Spec), nil
	default:
		return nil, qerrors.NewBadUsagef("kind %q cannot be proposed: a proposal takes %s or %s",
			envelope.Kind, alerts.AlertResolutionKind, warnings.WarningResolutionKind)
	}
}

// Kind is the document's manifest kind.
func (d *Document) Kind() string {
	if d.Alert != nil {
		return alerts.AlertResolutionKind
	}
	return warnings.WarningResolutionKind
}

// Target names the document's target issue as alert/<id> or warning/<id>.
func (d *Document) Target() string {
	if d.Alert != nil {
		return fmt.Sprintf("alert/%d", d.TargetID)
	}
	return fmt.Sprintf("warning/%d", d.TargetID)
}

// Action is the spec's action, "" for an alert's column mappings.
func (d *Document) Action() string {
	if d.Alert != nil {
		return d.Alert.Action
	}
	return d.Warning.Action
}

// Payload is the resolve request body the spec's ToRequest builds, as the
// map a proposal carries. Numbers keep their JSON literals.
func (d *Document) Payload() (map[string]interface{}, error) {
	var body interface{}
	if d.Alert != nil {
		body = d.Alert.ToRequest()
	} else {
		body = d.Warning.ToRequest()
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", d.Kind(), err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var payload map[string]interface{}
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", d.Kind(), err)
	}
	return payload, nil
}

// CreateRequest builds the create body for the document, with the addressed
// issue lists and submit flag.
func (d *Document) CreateRequest(alsoAlerts, alsoWarnings []int, submit bool) (CreateRequest, error) {
	payload, err := d.Payload()
	if err != nil {
		return CreateRequest{}, err
	}
	body := CreateRequest{Operation: d.Operation, Payload: payload, Submit: &submit}
	id := d.TargetID
	if d.Alert != nil {
		body.AlertItemID = &id
	} else {
		body.WarningItemID = &id
	}
	setAddressed(&body, alsoAlerts, alsoWarnings)
	return body, nil
}

// RuleParamsRequest builds the create body of a dataset_rule_patch proposal
// that replaces ruleID's params with params, with the addressed issue lists
// and submit flag.
func RuleParamsRequest(ruleID uuid.UUID, params map[string]interface{}, alsoAlerts, alsoWarnings []int, submit bool) CreateRequest {
	id := ruleID
	body := CreateRequest{
		Operation:     api.ChangeReviewOperationDatasetRulePatch,
		DatasetRuleID: &id,
		Payload:       map[string]interface{}{"params": params},
		Submit:        &submit,
	}
	setAddressed(&body, alsoAlerts, alsoWarnings)
	return body
}

func setAddressed(body *CreateRequest, alsoAlerts, alsoWarnings []int) {
	if len(alsoAlerts) > 0 {
		ids := append([]int(nil), alsoAlerts...)
		body.AddressedAlertItemIds = &ids
	}
	if len(alsoWarnings) > 0 {
		ids := append([]int(nil), alsoWarnings...)
		body.AddressedWarningItemIds = &ids
	}
}
