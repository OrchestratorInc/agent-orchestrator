package chat

import (
	"encoding/json"
	"reflect"
)

func dropEmptyArchiveFields(record map[string]any) {
	for key, value := range record {
		if emptyArchiveField(value) {
			delete(record, key)
		}
	}
}

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
