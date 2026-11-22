package payments

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Money stores exact cents. JSON uses BOB numbers, not strings or binary float arithmetic.
type Money int64

const MaxCents int64 = 9007199254740991

func (m *Money) UnmarshalJSON(raw []byte) error {
	text := string(bytes.TrimSpace(raw))
	invalid := errors.New("importe no negativo con máximo dos decimales")
	if len(text) == 0 || len(text) > 64 || text[0] < '0' || text[0] > '9' || !json.Valid(raw) {
		return invalid
	}
	mantissa := text
	exponent := 0
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		mantissa = text[:i]
		parsed, err := strconv.Atoi(text[i+1:])
		if err != nil || parsed < -100 || parsed > 16 {
			return invalid
		}
		exponent = parsed
	}
	decimals := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		decimals = len(mantissa) - i - 1
	}
	digits := strings.TrimLeft(strings.ReplaceAll(mantissa, ".", ""), "0")
	if digits == "" {
		*m = 0
		return nil
	}
	shift := exponent + 2 - decimals
	if shift < 0 {
		remove := -shift
		if remove > len(digits) || strings.Trim(digits[len(digits)-remove:], "0") != "" {
			return invalid
		}
		digits = digits[:len(digits)-remove]
	} else {
		if len(digits)+shift > 16 {
			return invalid
		}
		digits += strings.Repeat("0", shift)
	}
	cents, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || cents > MaxCents {
		return invalid
	}
	*m = Money(cents)
	return nil
}
func (m Money) MarshalJSON() ([]byte, error) {
	if m < 0 || int64(m) > MaxCents {
		return nil, errors.New("importe fuera de rango")
	}
	return []byte(fmt.Sprintf("%d.%02d", m/100, m%100)), nil
}
