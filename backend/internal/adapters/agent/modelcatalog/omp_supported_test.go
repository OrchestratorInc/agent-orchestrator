package modelcatalog

import (
	"reflect"
	"testing"
)

func TestParseOMPProviderListKeepsModelProviders(t *testing.T) {
	output := []byte(`[{"id":"openai-codex","name":"ChatGPT"},{"id":"openai-codex-device","name":"ChatGPT device"},{"id":"zai","name":"Z.AI"},{"id":"tavily","name":"Tavily"},{"id":"exa","name":"Exa"},{"id":" "}]`)
	if got, want := parseOMPProviderList(output), []string{"openai-codex", "zai"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}
	if got := parseOMPProviderList([]byte("not json")); got != nil {
		t.Fatalf("unparseable output = %v", got)
	}
}
