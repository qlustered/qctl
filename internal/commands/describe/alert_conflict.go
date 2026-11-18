package describe

import (
	"fmt"
	"slices"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/dataview"
	"github.com/spf13/cobra"
)

func formatAlertConflict(cmd *cobra.Command, ctx *cmdutil.CommandContext, alert *alerts.AlertFull, manifest *alerts.AlertManifestWithStatus) {
	if alert.IssueType != api.AlertTypeCellOverrideConflict || alert.FieldName == nil {
		return
	}
	if alert.MaskedFields != nil && slices.Contains(*alert.MaskedFields, *alert.FieldName) {
		// A masked value is text, whatever the column's type.
		return
	}
	schema, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		GetSchemaDetails(ctx.Credential.AccessToken, alert.DatasetID)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: conflict values use stored units; column display format unavailable: %v\n", err)
		return
	}
	field := schema.Fields[*alert.FieldName].Current
	if field == nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: conflict values use stored units; column has no applied schema definition")
		return
	}
	format := conflictDisplayFormat(field)
	if fixed := manifest.Spec.FieldValue; fixed != nil {
		if value, ok := dataview.FormatNumberForDisplay(*fixed, format); ok {
			manifest.Spec.FieldValue = &value
		}
	}
	body := manifest.Spec.Body
	if body == nil {
		return
	}
	// Older composite alerts omit the numeric flag; their pipeline text is
	// stored data. An explicit false marks unconverted upstream text.
	if body.PipelineValueIsNumber == nil || *body.PipelineValueIsNumber {
		formatConflictValue(body.PipelineValue, format, true)
	} else if body.PipelineValue != nil {
		if _, text := (*body.PipelineValue).(string); !text {
			formatConflictValue(body.PipelineValue, format, true)
		}
	}
	// Baseline text on a percent column has no stored-units marker.
	formatConflictValue(body.BaseDisplacedValue, format, false)
}

func formatConflictValue(value *any, format api.DisplayFormat, storedText bool) {
	if value == nil {
		return
	}
	if _, text := (*value).(string); text && !storedText && format.Kind == api.DisplayFormatKindPercent {
		return
	}
	if displayed, ok := dataview.FormatNumberForDisplay(*value, format); ok {
		*value = displayed
	}
}

func conflictDisplayFormat(field *datasets.FieldSchemaDetail) api.DisplayFormat {
	switch datasets.SchemaTypeName(field) {
	case "SmallInteger", "Integer", "BigInteger", "Decimal", "Float":
	default:
		return api.DisplayFormat{}
	}
	format := api.DisplayFormat{Kind: api.DisplayFormatKindNumber, Decimals: field.DisplayDecimals}
	switch {
	case field.IsDollar != nil && *field.IsDollar:
		format.Kind = api.DisplayFormatKindCurrency
	case field.IsPercent != nil && *field.IsPercent:
		format.Kind = api.DisplayFormatKindPercent
	}
	if format.Decimals == nil && format.Kind != api.DisplayFormatKindNumber {
		two := 2
		format.Decimals = &two
	}
	return format
}
