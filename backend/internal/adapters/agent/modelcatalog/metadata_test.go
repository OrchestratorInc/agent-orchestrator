package modelcatalog

import (
	"reflect"
	"testing"
)

func TestCatalogMetadataUsesOnlyReportedValues(t *testing.T) {
	models, err := parseJSONModels([]byte(`{"models":[
 {"id":"rich","contextWindow":200000,"input":["text","image","audio","text"]},
 {"id":"unknown"},
 {"id":"invalid","contextWindow":-1,"inputs":["audio"]},
 {"id":"nested","limit":{"context":1000000},"modalities":{"input":["text"]}}
 ]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		switch m.ID {
		case "rich":
			if m.ContextWindow != 200000 || !reflect.DeepEqual(m.Inputs, []string{"text", "image"}) {
				t.Fatalf("rich: %+v", m)
			}
		case "nested":
			if m.ContextWindow != 1000000 || !reflect.DeepEqual(m.Inputs, []string{"text"}) {
				t.Fatalf("nested: %+v", m)
			}
		default:
			if m.ContextWindow != 0 || len(m.Inputs) != 0 {
				t.Fatalf("guessed: %+v", m)
			}
		}
	}
}
func TestCatalogTokenUnits(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  int64
	}{
		{"1.0M", 1000000}, {"272K", 272000}, {float64(200000), 200000}, {"unknown", 0}, {"Infinity", 0}, {"NaN", 0}, {-1.0, 0}, {"200.5", 0},
	} {
		if got := catalogTokenCount(tc.value); got != tc.want {
			t.Fatalf("%v: %d, want %d", tc.value, got, tc.want)
		}
	}
}
func TestModelAliasMetadata(t *testing.T) {
	models, err := parseJSONModels([]byte(`{"models":{"fast":{"model":"native","context_window":64000,"inputs":["text"]}}}`))
	if err != nil || len(models) != 1 || models[0].ID != "fast" || models[0].ContextWindow != 64000 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}
