package chat

import (
	"encoding/json"
	"testing"
)

func TestDropEmptyArchiveFieldsKeepsZeroBoolsNumbersAndStructs(t *testing.T) {
	type pair struct{ A, B int }
	record := map[string]any{
		"exitCode":  0,
		"ok":        false,
		"ratio":     0.0,
		"count":     uint8(0),
		"origin":    pair{},
		"kept":      "x",
		"nothing":   nil,
		"noString":  "",
		"noPointer": (*int)(nil),
		"noSlice":   []string(nil),
		"emptySl":   []string{},
		"emptyMap":  map[string]int{},
		"noMap":     map[string]int(nil),
		"nullRaw":   json.RawMessage("null"),
		"blankRaw":  json.RawMessage(nil),
	}

	dropEmptyArchiveFields(record)

	want := map[string]bool{"exitCode": true, "ok": true, "ratio": true, "count": true, "origin": true, "kept": true}
	for key := range want {
		if _, ok := record[key]; !ok {
			t.Errorf("%s was dropped but holds a real value", key)
		}
	}
	for key := range record {
		if !want[key] {
			t.Errorf("%s = %#v was kept but is empty", key, record[key])
		}
	}
}
