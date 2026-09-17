package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Decoding: generic map (coming out of the YAML parser) -> typed structs.
//
// It is done with reflection to keep a single place holding the conversion
// rules and so that the errors say exactly which field is wrong, instead of
// failing silently the way a "look up the key and move on" would.
// ---------------------------------------------------------------------------

var durationType = reflect.TypeOf(time.Duration(0))

// Decode dumps a YAML map into a struct whose fields carry the `yaml:"..."`
// tag. Missing fields keep their current value (that is why the default values
// are loaded first).
func Decode(m map[string]any, dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return fmt.Errorf("the decode destination must be a non-nil pointer")
	}
	return decodeMap(m, v.Elem(), "")
}

func decodeMap(m map[string]any, dst reflect.Value, prefix string) error {
	if dst.Kind() != reflect.Struct {
		return fmt.Errorf("%s: expected a struct and found %s", pathOrName(prefix), dst.Kind())
	}

	fields := yamlFields(dst)
	for key, value := range m {
		field, ok := fields[key]
		if !ok {
			return fmt.Errorf("unknown key %q%s (check the example configs/agent.yaml.example)", key, inPath(prefix))
		}
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if err := assign(value, field, path); err != nil {
			return err
		}
	}
	return nil
}

// yamlFields indexes the fields by their yaml tag.
func yamlFields(v reflect.Value) map[string]reflect.Value {
	t := v.Type()
	out := make(map[string]reflect.Value, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		out[name] = v.Field(i)
	}
	return out
}

func assign(value any, field reflect.Value, path string) error {
	// A nested block arrives as a map.
	if m, ok := value.(map[string]any); ok {
		switch field.Kind() {
		case reflect.Struct:
			return decodeMap(m, field, path)
		case reflect.Map:
			// map[string]string exactly as it arrives from the YAML: {key: value}.
			if field.Type().Key().Kind() != reflect.String || field.Type().Elem().Kind() != reflect.String {
				return fmt.Errorf("%s: only text-to-text maps are supported", path)
			}
			fresh := reflect.MakeMap(field.Type())
			for k, v := range m {
				fresh.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(toText(v)))
			}
			field.Set(fresh)
			return nil
		default:
			return fmt.Errorf("%s: expected a simple value and found a nested block", path)
		}
	}

	if value == nil {
		// Explicit null: the field's default value is respected.
		return nil
	}

	switch field.Type() {
	case durationType:
		d, err := toDuration(value, path)
		if err != nil {
			return err
		}
		field.SetInt(int64(d))
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		// A list where text is expected is REJECTED. It used to be formatted as
		// "[a b]" and accepted silently, so the real error showed up much later
		// and with a bewildering message ("unknown kind: \"[a b]\"" instead of
		// "a value was expected here, not a list").
		if _, isList := value.([]any); isList {
			return fmt.Errorf("%s: expected a text value and found a list", path)
		}
		// A boolean where text is expected does get normalized ("true"/"false"):
		// that is what the parser returns for a boolean value and there is no
		// ambiguity.
		field.SetString(toText(value))

	case reflect.Bool:
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%s: expected true/false and found %v", path, value)
		}
		field.SetBool(b)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := toInteger(value)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		if field.OverflowInt(n) {
			return fmt.Errorf("%s: the value %d does not fit in %s", path, n, field.Kind())
		}
		field.SetInt(n)

	case reflect.Float32, reflect.Float64:
		f, err := toFloat(value)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		field.SetFloat(f)

	case reflect.Slice:
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: expected a list and found %v", path, value)
		}
		fresh := reflect.MakeSlice(field.Type(), 0, len(items))
		for i, item := range items {
			element := reflect.New(field.Type().Elem()).Elem()
			if err := assign(item, element, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
			fresh = reflect.Append(fresh, element)
		}
		field.Set(fresh)

	case reflect.Map:
		// A block value (map[string]any) is turned into the map at the top of
		// assign, so what reaches this point cannot fill the field: it is a
		// plain value where a {key: value} block was expected.
		return fmt.Errorf("%s: expected a map and found %v", path, value)

	case reflect.Struct:
		// A simple value where a nested block is expected (for example
		// `llm: text` instead of `llm:` with its indented keys).
		return fmt.Errorf("%s: expected a configuration block and found a simple value (%v)", path, value)

	default:
		return fmt.Errorf("%s: unsupported field type (%s)", path, field.Kind())
	}
	return nil
}

// toDuration accepts numbers (seconds) and texts like "30s", "5m", "1h30m".
func toDuration(value any, path string) (time.Duration, error) {
	switch v := value.(type) {
	case int64:
		return time.Duration(v) * time.Second, nil
	case float64:
		return time.Duration(v * float64(time.Second)), nil
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return 0, fmt.Errorf("%s: empty duration", path)
		}
		d, err := time.ParseDuration(text)
		if err != nil {
			// Without a suffix it is interpreted as seconds, which is what
			// anyone writing "timeout: 30" expects.
			if seconds, errSec := strconv.Atoi(text); errSec == nil {
				return time.Duration(seconds) * time.Second, nil
			}
			return 0, fmt.Errorf("%s: invalid duration %q (use 30, \"30s\", \"5m\", \"1h\")", path, text)
		}
		return d, nil
	default:
		return 0, fmt.Errorf("%s: expected a duration and found %v", path, value)
	}
}

func toInteger(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("expected an integer and found %v", v)
		}
		return int64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		text := strings.TrimSpace(v)
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("expected an integer and found %q", v)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("expected an integer and found %v", value)
	}
}

func toFloat(value any) (float64, error) {
	switch v := value.(type) {
	case int64:
		return float64(v), nil
	case float64:
		return v, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("expected a number and found %q", v)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("expected a number and found %v", value)
	}
}

func toText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func inPath(prefix string) string {
	if prefix == "" {
		return ""
	}
	return " inside " + prefix
}

func pathOrName(prefix string) string {
	if prefix == "" {
		return "the configuration"
	}
	return prefix
}
