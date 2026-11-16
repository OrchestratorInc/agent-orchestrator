package chattraffic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxJSONDepth   = 16
	maxArrayLength = 512
	maxStringBytes = 256
)

// decodeStrict accepts data only if it is exactly one JSON value whose shape
// matches target's type: no duplicate or unknown keys, no missing keys, keys
// matched case-sensitively, strings and booleans where the type has them, and
// integers written as plain non-negative decimals no larger than MaxMetric.
// encoding/json alone accepts duplicate keys (last wins), case-variant keys,
// and floats for integer fields, so the shape is checked on a token tree first.
func decodeStrict(data []byte, target any) error {
	switch {
	case len(data) == 0:
		return errors.New("input is empty")
	case len(data) > MaxArtifactBytes:
		return fmt.Errorf("input is %d bytes; the limit is %d", len(data), MaxArtifactBytes)
	case bytes.HasPrefix(data, []byte("\xef\xbb\xbf")):
		return errors.New("input starts with a byte order mark")
	case !utf8.Valid(data):
		return errors.New("input is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tree, err := readJSONValue(dec, 0)
	if err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("input has data after its JSON value")
	}
	if err := checkShape(tree, reflect.TypeOf(target).Elem(), "$"); err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func readJSONValue(dec *json.Decoder, depth int) (any, error) {
	if depth > maxJSONDepth {
		return nil, errors.New("JSON nesting is too deep")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("invalid JSON: %w", err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, errors.New("invalid JSON: object key is not a string")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate key %q", key)
			}
			value, err := readJSONValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return object, nil
	case '[':
		array := []any{}
		for dec.More() {
			if len(array) == maxArrayLength {
				return nil, fmt.Errorf("array has more than %d entries", maxArrayLength)
			}
			value, err := readJSONValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return array, nil
	}
	return nil, errors.New("invalid JSON: unexpected delimiter")
}

func checkShape(value any, t reflect.Type, path string) error {
	switch t.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want an object", path)
		}
		fields := jsonFieldTypes(t)
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if _, known := fields[key]; !known {
				return fmt.Errorf("%s: unknown key %q", path, key)
			}
		}
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			child, present := object[name]
			if !present {
				return fmt.Errorf("%s: missing key %q", path, name)
			}
			if err := checkShape(child, fields[name], path+"."+name); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice:
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: want an array", path)
		}
		for i, child := range array {
			if err := checkShape(child, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case reflect.String:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: want a string", path)
		}
		if len(s) > maxStringBytes {
			return fmt.Errorf("%s: string is longer than %d bytes", path, maxStringBytes)
		}
		return nil
	case reflect.Bool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: want true or false", path)
		}
		return nil
	case reflect.Int64:
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%s: want an integer", path)
		}
		if _, err := parseMetric(string(number)); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf("%s: unsupported schema type %s", path, t)
}

// parseMetric accepts only 0 or a decimal without sign, fraction, exponent or
// leading zero, no larger than MaxMetric.
func parseMetric(raw string) (int64, error) {
	if raw == "" || len(raw) > 10 || (len(raw) > 1 && raw[0] == '0') ||
		strings.TrimLeft(raw, "0123456789") != "" {
		return 0, errors.New("want a non-negative integer without sign, fraction or exponent")
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value > MaxMetric {
		return 0, fmt.Errorf("integer is larger than %d", MaxMetric)
	}
	return value, nil
}

// jsonFieldTypes maps each json key of struct type t to its field type. Every
// schema field carries a json tag; the schema has no optional keys.
func jsonFieldTypes(t reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !field.IsExported() || name == "" || name == "-" {
			continue
		}
		fields[name] = field.Type
	}
	return fields
}
