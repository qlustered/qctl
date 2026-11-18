package dataview

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
)

// formatDisplayCell applies the server-declared display format to a converted
// numeric cell. A quarantined cell listed in not_converted still contains its
// source text, so formatting it would invent a numeric interpretation the
// cleaner explicitly did not make.
func formatDisplayCell(v interface{}, format api.DisplayFormat, unconverted bool) string {
	if !unconverted {
		if displayed, ok := formatNumberForDisplay(v, format); ok {
			return escapeCell(displayed)
		}
	}
	return formatCell(v)
}

// numericTextPattern is atlas's NUMERIC_TEXT_PATTERN (common/common/
// search_terms.py), the spelling a stored number has: an optional minus,
// digits, an optional fraction and an exponent of at most three digits. It is
// what the quarantine "converted" predicate and the web grid both accept, so a
// cell outside it is shown as it came. The three-digit exponent is also what
// keeps a formatted cell's length bounded.
var numericTextPattern = regexp.MustCompile(`^(-?)([0-9]+)(?:\.([0-9]+))?(?:[eE]([-+]?[0-9]{1,3}))?$`)

// maxNumericTextLen is the length past which atlas's numeric-text check and
// the web grid both stop reading a value as a number.
const maxNumericTextLen = 400

type decimalValue struct {
	negative    bool
	coefficient *big.Int
	scale       int
}

// formatNumberForDisplay is atlas's format_number_for_display for a format
// that states its decimals, checked against atlas's parity fixture in
// numeric_test.go. A number format WITHOUT decimals declines, and the cell
// prints as it came, as the web grid prints it: the server has already
// normalized a clean Decimal before sending it, while a quarantine cell under
// such a format is never listed in not_converted, so rewriting it would
// reformat source text the cleaner did not convert. Atlas's own branch for
// that case feeds only its cleaner messages.
func formatNumberForDisplay(v interface{}, format api.DisplayFormat) (string, bool) {
	return FormatNumberForDisplay(v, format)
}

// FormatNumberForDisplay formats a stored number using the column's display units.
func FormatNumberForDisplay(v interface{}, format api.DisplayFormat) (string, bool) {
	if format.Decimals == nil || *format.Decimals < 0 {
		// Covers the null entry too: it decodes as the zero DisplayFormat.
		// Atlas refuses a currency or percent format without decimals.
		return "", false
	}
	decimals := *format.Decimals

	shift := 0
	switch format.Kind {
	case api.DisplayFormatKindCurrency, api.DisplayFormatKindNumber:
	case api.DisplayFormatKindPercent:
		shift = 2
	default:
		return "", false
	}
	value, ok := parseDecimal(numericText(v))
	if !ok {
		return "", false
	}

	rounded := value.roundedMagnitude(decimals, shift)
	digits := rounded.String()
	if decimals > 0 {
		if len(digits) <= decimals {
			digits = strings.Repeat("0", decimals-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-decimals] + "." + digits[len(digits)-decimals:]
	}
	switch format.Kind {
	case api.DisplayFormatKindCurrency:
		whole, fraction, _ := strings.Cut(digits, ".")
		whole = groupThousands(whole)
		digits = "$" + whole
		if fraction != "" {
			digits += "." + fraction
		}
	case api.DisplayFormatKindPercent:
		digits += "%"
	}
	if value.negative && rounded.Sign() != 0 {
		digits = "-" + digits
	}
	return digits, true
}

func numericText(v interface{}) string {
	switch n := v.(type) {
	case string:
		return n
	case json.Number:
		if isIntegerLiteral(n) {
			return n.String()
		}
		// A float column's value: normalize it as the float64 case does.
		f, err := n.Float64()
		if err != nil {
			return ""
		}
		return numericText(f)
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return ""
		}
		// Atlas casts double precision to numeric at 15 significant digits
		// before applying the same display format. Match that normalization so
		// a binary-float tail cannot move a value across a rounding boundary.
		return strconv.FormatFloat(n, 'g', 15, 64)
	case float32:
		if math.IsInf(float64(n), 0) || math.IsNaN(float64(n)) {
			return ""
		}
		return strconv.FormatFloat(float64(n), 'g', 15, 32)
	case int, int64, int32, uint, uint64, uint32:
		return fmt.Sprint(n)
	default:
		return ""
	}
}

// parseDecimal reads text that matches numericTextPattern; anything else is
// not a number here.
func parseDecimal(text string) (decimalValue, bool) {
	if len(text) > maxNumericTextLen {
		return decimalValue{}, false
	}
	m := numericTextPattern.FindStringSubmatch(text)
	if m == nil {
		return decimalValue{}, false
	}
	exponent := 0
	if m[4] != "" {
		var err error
		if exponent, err = strconv.Atoi(m[4]); err != nil {
			return decimalValue{}, false
		}
	}
	coefficient, ok := new(big.Int).SetString(m[2]+m[3], 10)
	if !ok {
		return decimalValue{}, false
	}
	return decimalValue{negative: m[1] == "-", coefficient: coefficient, scale: len(m[3]) - exponent}, true
}

// roundedMagnitude returns abs(value * 10^shift), rounded to decimals places
// with ties away from zero. Keeping sign separate makes ROUND_HALF_UP and
// PostgreSQL round agree for negative values too.
func (d decimalValue) roundedMagnitude(decimals, shift int) *big.Int {
	coefficient := new(big.Int).Set(d.coefficient)
	sourceScale := d.scale - shift
	if sourceScale < 0 {
		coefficient.Mul(coefficient, powerOfTen(-sourceScale))
		sourceScale = 0
	}
	if sourceScale <= decimals {
		return coefficient.Mul(coefficient, powerOfTen(decimals-sourceScale))
	}
	divisor := powerOfTen(sourceScale - decimals)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(coefficient, divisor, remainder)
	if remainder.Lsh(remainder, 1).Cmp(divisor) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

func powerOfTen(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

func groupThousands(whole string) string {
	if len(whole) <= 3 {
		return whole
	}
	first := len(whole) % 3
	if first == 0 {
		first = 3
	}
	var b strings.Builder
	b.WriteString(whole[:first])
	for i := first; i < len(whole); i += 3 {
		b.WriteByte(',')
		b.WriteString(whole[i : i+3])
	}
	return b.String()
}
