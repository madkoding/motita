package llm

import (
	"encoding/json"
	"testing"
)

// DecodeArguments applies the same coercion as the plan tools: a model that sends a
// number as text ("5") still fills an int field, and a decimal ("1.5") is refused
// instead of being truncated to 1 or rounded to 2.
func TestDecodeArgumentsCoercesNumericText(t *testing.T) {
	fc := FunctionCall{
		Name:      "search",
		Arguments: json.RawMessage(`{"limit":"5","ratio":"1.5","literal":"true"}`),
	}
	var dest struct {
		Limit   int     `json:"limit"`
		Ratio   float64 `json:"ratio"`
		Literal bool    `json:"literal"`
	}
	if err := fc.DecodeArguments(&dest); err != nil {
		t.Fatalf("error: %v", err)
	}
	if dest.Limit != 5 {
		t.Errorf("limit = %d, want 5", dest.Limit)
	}
	if dest.Ratio != 1.5 {
		t.Errorf("ratio = %v, want 1.5", dest.Ratio)
	}
	if !dest.Literal {
		t.Errorf("literal = %v, want true", dest.Literal)
	}
}

// A decimal is not an integer: "1.5" must be rejected, not truncated to 1 or rounded
// to 2. The coercion only accepts a lossless conversion, and ParseInt refuses it.
func TestDecodeArgumentsRejectsDecimalTextForInt(t *testing.T) {
	fc := FunctionCall{
		Name:      "search",
		Arguments: json.RawMessage(`{"limit":"1.5"}`),
	}
	var dest struct {
		Limit int `json:"limit"`
	}
	if err := fc.DecodeArguments(&dest); err == nil {
		t.Fatalf("limit = %d, want a type error for \"1.5\"", dest.Limit)
	}
}
