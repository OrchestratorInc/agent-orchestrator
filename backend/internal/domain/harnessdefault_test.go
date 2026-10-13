package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestHarnessDefaultIgnoresRetiredModelAndEffortKeys(t *testing.T) {
	var got HarnessDefault
	if err := json.Unmarshal([]byte(`{"model":"opus","effort":"high","enabledModels":["opus"]}`), &got); err != nil {
		t.Fatal(err)
	}
	if want := (HarnessDefault{EnabledModels: []string{"opus"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if !(HarnessDefault{}).IsZero() || got.IsZero() {
		t.Fatalf("IsZero misreports %+v", got)
	}
}
