package chat

import (
	"encoding/json"
	"reflect"
)

// dropEmptyArchiveFields removes the entries of a provider-event archive record
// that hold nothing: nil, an empty string, slice or map, a nil pointer, a JSON null.
// A false, a 0 or a zero struct is a value and stays.
// The record has 26 keys and a streamed delta sets about five of them, so the rest
// used to be stored as null or "" on every row.
//
// An absent key reads exactly like an empty one. The only SQL that reads the
// payload, SelectVerifiedAuthenticationTurn, uses json_extract, which returns NULL
// for both, and its one comparison with the empty string goes through COALESCE. A Go
// decoder gives both the zero value.
//
// Call it before adding a key whose presence means something. "error" is one: that
// query tests "$.error IS NULL", so an error with an empty message has to stay in
// the row for the turn to remain a failure.
func dropEmptyArchiveFields(record map[string]any) {
	for key, value := range record {
		if emptyArchiveField(value) {
			delete(record, key)
		}
	}
}

// emptyArchiveField is true for nil, an empty string, slice or map, a nil pointer or
// interface, and a JSON null. It deliberately does not use reflect's IsZero: a bool,
// a number or a struct holding its zero value is a real value, not an empty one.
func emptyArchiveField(value any) bool {
	if raw, ok := value.(json.RawMessage); ok {
		return len(raw) == 0 || string(raw) == "null"
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Invalid:
		return true
	case reflect.String, reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}
