package actionfields

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestValidateAcceptsACompleteAction(t *testing.T) {
	p := Present{}
	p.Set("new_value", true)
	if err := Validate("map_value", api.AlertActionFields["map_value"], p); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsAMissingRequiredField(t *testing.T) {
	err := Validate("map_value", api.AlertActionFields["map_value"], Present{})
	if err == nil {
		t.Fatal("want an error for a missing required field")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "new_value") {
		t.Errorf("message must name the missing field, got %q", err)
	}
}

func TestValidateRejectsAnUnsatisfiedOneOfGroup(t *testing.T) {
	p := Present{}
	p.Set("new_value", true)
	err := Validate("overwrite_value", api.AlertActionFields["overwrite_value"], p)
	if err == nil {
		t.Fatal("want an error: overwrite_value needs a row selector")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "ids|initial_signatures") {
		t.Errorf("message must render the one-of group, got %q", err)
	}
}

func TestValidateRejectsAFieldTheActionDoesNotRead(t *testing.T) {
	p := Present{}
	p.Set("new_value", true)
	p.Set("sheet_name", true)
	err := Validate("map_value", api.AlertActionFields["map_value"], p)
	if err == nil {
		t.Fatal("want an error: map_value does not read sheet_name")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "sheet_name") {
		t.Errorf("message must name the unread field, got %q", err)
	}
}

// One error, not three: reporting only the first would send the user round the
// loop once per mistake.
func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	p := Present{}
	p.Set("sheet_name", true)
	err := Validate("overwrite_value", api.AlertActionFields["overwrite_value"], p)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"sheet_name", "new_value", "ids|initial_signatures"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message must mention %q, got %q", want, err)
		}
	}
}

// A parameterless action with a stray field is still a refusal, and the
// "it reads:" line must not claim the action reads something.
func TestValidateRejectsAnyFieldOnAParameterlessAction(t *testing.T) {
	p := Present{}
	p.Set("new_value", true)
	err := Validate("disable_rule", api.AlertActionFields["disable_rule"], p)
	if err == nil {
		t.Fatal("want an error: disable_rule reads no fields")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "reads no fields") {
		t.Errorf("message should say the action reads no fields, got %q", err)
	}
}
