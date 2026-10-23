package plan

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A model sometimes sends a number as text ("50" instead of 50). That used to fail the
// decode, which failed the tool call and spent an attempt on an argument that was
// perfectly understandable. decodeArgs accepts the lossless conversions and still
// rejects a value that is really the wrong type.
func TestDecodeArgsCoercesNumericText(t *testing.T) {
	var args searchIndexArgs
	if err := decodeArgs(raw(`{"query":"x","limit":"50"}`), &args); err != nil {
		t.Fatalf("a numeric string must decode: %v", err)
	}
	if args.Limit != 50 {
		t.Errorf("limit = %d, want 50", args.Limit)
	}
}

func TestDecodeArgsCoercesBooleanText(t *testing.T) {
	var args struct {
		Literal bool `json:"literal"`
	}
	if err := decodeArgs(raw(`{"literal":"true"}`), &args); err != nil {
		t.Fatalf("a boolean string must decode: %v", err)
	}
	if !args.Literal {
		t.Error("literal = false, want true")
	}
}

func TestDecodeArgsKeepsStringFieldsAsStrings(t *testing.T) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(raw(`{"path":"123"}`), &args); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if args.Path != "123" {
		t.Errorf("path = %q, want the string \"123\"", args.Path)
	}
}

func TestDecodeArgsRejectsRealTypeErrors(t *testing.T) {
	for _, in := range []string{
		`{"query":"x","limit":"many"}`,
		`{"query":"x","limit":true}`,
		`{"query":"x","limit":[1]}`,
	} {
		var args searchIndexArgs
		if err := decodeArgs(raw(in), &args); err == nil {
			t.Errorf("%s decoded, want a type error", in)
		}
	}
}

// The point of the coercion: the tool call itself goes through instead of failing on an
// argument the model sent as text.
func TestSearchIndexAcceptsANumericLimitAsText(t *testing.T) {
	dir := t.TempDir()
	f := &fakeIndex{}
	p := (&Planner{}).WithIndex(f)
	out := p.toolSearchIndex(context.Background(),
		raw(`{"query":"x","limit":"50","path":"`+filepath.ToSlash(dir)+`"}`))
	if strings.HasPrefix(out, errParsingArguments) {
		t.Fatalf("a numeric string failed the tool call: %q", out)
	}
	if f.limit != 50 {
		t.Errorf("limit = %d, want 50", f.limit)
	}
}
