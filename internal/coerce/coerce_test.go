package coerce

import (
	"reflect"
	"testing"
)

// TestCoerceValueIntegerFormats covers the numeric formats a model writes for an
// integer field: the plain ones, the signed zero, and the decimal zero (".0"), which
// strconv.ParseInt refuses but is still the integer 0.
func TestCoerceValueIntegerFormats(t *testing.T) {
	intType := reflect.TypeOf(0)
	cases := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{".0", 0},
		{"-0", 0},
		{"+0", 0},
		{" 42 ", 42},
		{"-3", -3},
		{"+7", 7},
		{"1.0", 1},
		{"-2.000", -2},
	}
	for _, c := range cases {
		got := CoerceValue(c.in, intType)
		if got != c.want {
			t.Errorf("CoerceValue(%q, int) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

// TestCoerceValueRejectsNonNumeric checks that an empty string, a blank one and text
// that is not a whole number are left exactly as they came, so the decode reports a
// type error instead of a guessed value.
func TestCoerceValueRejectsNonNumeric(t *testing.T) {
	intType := reflect.TypeOf(0)
	for _, in := range []string{"", "   ", "abc", "1.5", "-0.5", "0x10", "1e", "--1", "12abc", "NaN", "Inf"} {
		if got := CoerceValue(in, intType); got != in {
			t.Errorf("CoerceValue(%q, int) = %#v, want it unchanged", in, got)
		}
	}
}

// TestCoerceValueUnsigned covers the unsigned destination, where a negative value must
// be refused rather than wrapped around.
func TestCoerceValueUnsigned(t *testing.T) {
	uintType := reflect.TypeOf(uint(0))
	if got := CoerceValue(".0", uintType); got != uint64(0) {
		t.Errorf("CoerceValue(%q, uint) = %#v, want uint64(0)", ".0", got)
	}
	if got := CoerceValue("7", uintType); got != uint64(7) {
		t.Errorf("CoerceValue(%q, uint) = %#v, want uint64(7)", "7", got)
	}
	if got := CoerceValue("-1", uintType); got != "-1" {
		t.Errorf("CoerceValue(%q, uint) = %#v, want it unchanged", "-1", got)
	}
}

// TestCoerceValueStructAndBool checks that the walk follows the destination type: a
// field declared as an integer is converted, a string field keeps its text, and a bool
// accepts the text form.
func TestCoerceValueStructAndBool(t *testing.T) {
	type args struct {
		Count int    `json:"count"`
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	got := CoerceValue(map[string]any{
		"count": ".0",
		"name":  "123",
		"force": "TRUE",
	}, reflect.TypeOf(args{}))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("CoerceValue returned %T, want map[string]any", got)
	}
	if m["count"] != int64(0) {
		t.Errorf("count = %#v, want int64(0)", m["count"])
	}
	if m["name"] != "123" {
		t.Errorf("name = %#v, want the string \"123\"", m["name"])
	}
	if m["force"] != true {
		t.Errorf("force = %#v, want true", m["force"])
	}
}
