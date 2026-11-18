package deliverables

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"gopkg.in/yaml.v3"
)

// RunParams maps run parameter names to scalar values.
type RunParams map[string]interface{}

const yamlStringTag = "!!str"

// ReadRunParams reads a YAML parameter mapping from path, or stdin for "-".
func ReadRunParams(path string, stdin io.Reader) (RunParams, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, qerrors.NewBadUsagef("read --params-file %q: %v", path, err)
	}
	return ParseRunParams(data)
}

// RunParamInput is the run parameters a command was given.
type RunParamInput struct {
	// Params are the parameters file's, nil without one.
	Params RunParams
	// Period is the --period value, nil when it was not given.
	Period *string
}

// ForDeliverable returns a copy of the parameters with Period set as d's
// period parameter. A Period for a deliverable without a period parameter is a
// bad-usage error.
func (in RunParamInput) ForDeliverable(d *Deliverable) (RunParams, error) {
	params := make(RunParams, len(in.Params)+1)
	for name, value := range in.Params {
		params[name] = value
	}
	if in.Period != nil {
		if d.PeriodParam == nil || strings.TrimSpace(*d.PeriodParam) == "" {
			return nil, qerrors.NewBadUsage("--period requires a deliverable with a period parameter; use --params-file for other parameters")
		}
		params[*d.PeriodParam] = *in.Period
	}
	return params, nil
}

// ParseRunParams decodes one YAML mapping of named scalars.
func ParseRunParams(data []byte) (RunParams, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, qerrors.NewBadUsagef("--params-file must contain a YAML mapping: %v", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, qerrors.NewBadUsage("--params-file must contain exactly one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, qerrors.NewBadUsage("--params-file must contain a YAML mapping of parameter names to scalars")
	}
	params := RunParams{}
	nodes := doc.Content[0].Content
	for i := 0; i < len(nodes); i += 2 {
		key := nodes[i]
		if key.Kind != yaml.ScalarNode || key.Tag != yamlStringTag || strings.TrimSpace(key.Value) == "" {
			return nil, qerrors.NewBadUsage("parameter names must be nonblank strings")
		}
		if _, exists := params[key.Value]; exists {
			return nil, qerrors.NewBadUsagef("duplicate parameter %q", key.Value)
		}
		value, err := runParamScalar(nodes[i+1])
		if err != nil {
			return nil, qerrors.NewBadUsagef("parameter %q: %v", key.Value, err)
		}
		params[key.Value] = value
	}
	return params, nil
}

func runParamScalar(node *yaml.Node) (interface{}, error) {
	if node.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("must be a string, integer, finite float, boolean or null")
	}
	if (node.Style == 0 || node.Tag == "!!int") && leadingZeroLiteral.MatchString(node.Value) {
		return nil, fmt.Errorf("%s has a leading zero; quote it for text, or drop the leading zero for a number", node.Value)
	}
	switch node.Tag {
	case yamlStringTag, "!!bool", "!!null", "!!int":
		if node.Tag == yamlStringTag && node.Style == 0 && numericLiteral.MatchString(node.Value) {
			if _, err := strconv.ParseFloat(strings.ReplaceAll(node.Value, "_", ""), 64); err != nil {
				return nil, fmt.Errorf("number is outside the supported range")
			}
		}
		var value interface{}
		if err := node.Decode(&value); err != nil {
			return nil, err
		}
		// yaml.v3 falls back to float64 for integers outside its integer range.
		if _, overflow := value.(float64); overflow {
			return nil, fmt.Errorf("integer must be between -2**63 and 2**64 - 1")
		}
		return value, nil
	case "!!float":
		return runParamFloat(node)
	}
	return nil, fmt.Errorf("unsupported YAML scalar; quote dates and text")
}

func runParamFloat(node *yaml.Node) (interface{}, error) {
	if node.Style&yaml.TaggedStyle == 0 && integerLiteral.MatchString(node.Value) {
		return nil, fmt.Errorf("integer must be between -2**63 and 2**64 - 1")
	}
	var value float64
	if err := node.Decode(&value); err != nil {
		return nil, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, fmt.Errorf("float must be finite")
	}
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return json.Number(text), nil
}

// TypedRunParams encodes params as type/text pairs whose text is the value's Python str().
func TypedRunParams(params RunParams) (map[string]api.RunParameterValue, error) {
	typed := make(map[string]api.RunParameterValue, len(params))
	for name, value := range params {
		var kind api.RunParameterValueType
		var text string
		switch v := value.(type) {
		case nil:
			typed[name] = api.RunParameterValue{Type: api.Null}
			continue
		case string:
			kind, text = api.String, v
		case bool:
			kind, text = api.Boolean, "False"
			if v {
				text = "True"
			}
		case int:
			kind, text = api.Integer, strconv.Itoa(v)
		case int64:
			kind, text = api.Integer, strconv.FormatInt(v, 10)
		case uint64:
			kind, text = api.Integer, strconv.FormatUint(v, 10)
		case json.Number:
			f, err := strconv.ParseFloat(string(v), 64)
			if err != nil {
				return nil, fmt.Errorf("parameter %q: %w", name, err)
			}
			kind, text = api.Float, pythonFloatRepr(f)
		default:
			return nil, fmt.Errorf("parameter %q: unsupported value type %T", name, value)
		}
		typed[name] = api.RunParameterValue{Type: kind, Text: &text}
	}
	return typed, nil
}

// pythonFloatRepr formats a finite v as Python's repr(float).
func pythonFloatRepr(v float64) string {
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(v, 'e', -1, 64), "e")
	exp, _ := strconv.Atoi(exponent)
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	digits := strings.Replace(mantissa, ".", "", 1)
	switch {
	case exp < -4 || exp >= 16:
		expSign := "+"
		if exp < 0 {
			expSign, exp = "-", -exp
		}
		return fmt.Sprintf("%s%se%s%02d", sign, mantissa, expSign, exp)
	case exp < 0:
		return sign + "0." + strings.Repeat("0", -exp-1) + digits
	case len(digits) <= exp+1:
		return sign + digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
	default:
		return sign + digits[:exp+1] + "." + digits[exp+1:]
	}
}

// SameRunParamTexts reports whether a and b have the same names and texts, ignoring type.
func SameRunParamTexts(a, b map[string]api.RunParameterValue) bool {
	if len(a) != len(b) {
		return false
	}
	for name, left := range a {
		right, ok := b[name]
		if !ok || (left.Text == nil) != (right.Text == nil) {
			return false
		}
		if left.Text != nil && *left.Text != *right.Text {
			return false
		}
	}
	return true
}

var filenameToken = regexp.MustCompile(`\{([^}]+)\}`)
var leadingZeroLiteral = regexp.MustCompile(`^[+-]?0[0-9_]+$`)
var integerLiteral = regexp.MustCompile(`^[+-]?[0-9][0-9_]*$`)
var numericLiteral = regexp.MustCompile(`^[+-]?[0-9][0-9_]*(\.[0-9_]*)?([eE][+-]?[0-9]+)?$`)

// RequiredRunParams lists declared names in period, cell, column and filename order.
func RequiredRunParams(revision *api.DeliverableRevisionResponse, filenamePattern string, periodParam *string) []string {
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if periodParam != nil && strings.TrimSpace(*periodParam) != "" {
		add(*periodParam)
	}
	for _, cell := range copied(revision.CellBindings) {
		if string(cell.SourceType) == "run_param" && cell.RunParam != nil {
			add(*cell.RunParam)
		}
	}
	for _, sheet := range copied(revision.SheetBindings) {
		for _, column := range copied(sheet.ColumnBindings) {
			if column.SourceType == api.DeliverableColumnSourceTypeRunParam && column.RunParam != nil {
				add(*column.RunParam)
			}
		}
	}
	for _, match := range filenameToken.FindAllStringSubmatch(filenamePattern, -1) {
		add(match[1])
	}
	return names
}

// ValidateRequiredRunParams rejects missing, null or blank required values.
func ValidateRequiredRunParams(params RunParams, names []string) error {
	var missing []string
	for _, name := range names {
		value := params[name]
		text, isString := value.(string)
		if value == nil || (isString && strings.TrimSpace(text) == "") {
			missing = append(missing, strconv.Quote(name))
		}
	}
	if len(missing) > 0 {
		return qerrors.NewBadUsagef("missing or blank run parameters: %s; supply them with --params-file (or --period for the period parameter)", strings.Join(missing, ", "))
	}
	return nil
}
