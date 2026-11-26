package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestHarnessDefaultIgnoresRetiredKeys(t *testing.T) {
	var got HarnessDefault
	if err := json.Unmarshal([]byte(`{"model":"opus","effort":"high","enabledModels":["sonnet"],"disabledModels":["opus"]}`), &got); err != nil {
		t.Fatal(err)
	}
	if want := (HarnessDefault{DisabledModels: []string{"opus"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if !(HarnessDefault{}).IsZero() || got.IsZero() {
		t.Fatalf("IsZero misreports %+v", got)
	}
}
