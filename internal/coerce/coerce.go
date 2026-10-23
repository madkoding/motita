// Package coerce turns the values a model sent as text into the type a destination
// field declares, when the conversion loses nothing.
//
// Providers disagree about types: a field declared as a number arrives as the string
// "50", and a boolean arrives as "true". Decoding that straight into a Go struct fails
// with a type error the model cannot act on, so the value is converted first — but only
// where the destination really asks for that type and the conversion is lossless. A
// string field keeps a numeric-looking string ("123" stays "123").
package coerce

import (
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Bounds of the integer types, as float64, so a decimal that is really an integer can be
// checked against them before it is converted.
const (
	minInt64AsFloat  = -9223372036854775808.0  // -2^63
	maxInt64AsFloat  = 9223372036854775808.0   // 2^63
	maxUint64AsFloat = 18446744073709551616.0  // 2^64
)

// CoerceValue turns the values a model sent as text into the type the destination
// field declares, when the conversion loses nothing. It walks the destination type,
// not the JSON, so a string field keeps a numeric-looking string ("123" stays "123")
// and only a field that is really a number or a bool is converted.
//
// The accepted formats are:
//   - int, int8..int64: a base-10 integer in text ("50", "-3", "+7"), trimmed of
//     spaces. A decimal with no fractional part (".0", "1.0") is taken too, because a
//     model writes zero that way and the value is still an integer; a decimal with a
//     fraction ("1.5") or anything else is left as it came, so the decode reports a
//     type error instead of truncating or rounding.
//   - uint, uint8..uint64: the same, for a value that is not negative.
//   - float32, float64: a decimal number in text ("1.5", "2"), trimmed.
//   - bool: "true" or "false" in any case, trimmed.
//   - struct: each exported field is coerced by its json tag name (or Go name).
//   - slice, array: each element is coerced by the element type.
//   - map: each value is coerced by the value type.
//
// Anything else is returned unchanged.
func CoerceValue(v any, t reflect.Type) any {
	if t == nil {
		return v
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[k] = val
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name := f.Name
			if tag := f.Tag.Get("json"); tag != "" {
				if tag == "-" {
					continue
				}
				if n, _, _ := strings.Cut(tag, ","); n != "" {
					name = n
				}
			}
			if val, ok := out[name]; ok {
				out[name] = CoerceValue(val, f.Type)
			}
		}
		return out
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if s, ok := v.(string); ok {
			if n, ok := parseInt(s); ok {
				return n
			}
		}
		return v
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if s, ok := v.(string); ok {
			if n, ok := parseUint(s); ok {
				return n
			}
		}
		return v
	case reflect.Float32, reflect.Float64:
		if s, ok := v.(string); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				return f
			}
		}
		return v
	case reflect.Bool:
		if s, ok := v.(string); ok {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "true":
				return true
			case "false":
				return false
			}
		}
		return v
	case reflect.Slice, reflect.Array:
		arr, ok := v.([]any)
		if !ok {
			return v
		}
		out := make([]any, len(arr))
		for i, e := range arr {
			out[i] = CoerceValue(e, t.Elem())
		}
		return out
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		out := make(map[string]any, len(m))
		for k, e := range m {
			out[k] = CoerceValue(e, t.Elem())
		}
		return out
	}
	return v
}

// parseInt reads a base-10 integer from text. Besides the forms strconv accepts
// ("50", "-3", "+7"), it takes a decimal with no fractional part (".0", "1.0"): a
// model writes zero that way and the value is still an integer. A decimal with a
// fraction ("1.5") is refused, so nothing is truncated or rounded.
func parseInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.Trunc(f) != f || f < minInt64AsFloat || f >= maxInt64AsFloat {
		return 0, false
	}
	return int64(f), true
}

// parseUint is parseInt for an unsigned destination: the same formats, and a negative
// value is refused rather than wrapped around.
func parseUint(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.Trunc(f) != f || f < 0 || f >= maxUint64AsFloat {
		return 0, false
	}
	return uint64(f), true
}
