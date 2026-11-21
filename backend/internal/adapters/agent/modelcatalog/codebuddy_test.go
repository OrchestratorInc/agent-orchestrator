package modelcatalog

import (
	"reflect"
	"testing"
)

// CodeBuddy Code 2.164 prints its runnable models in the --model help; account
// models and models.json custom models both appear, and wrapping is tolerated.
func TestCodeBuddyDiscoveryReadsSupportedModelsFromHelp(t *testing.T) {
	spec, ok := commandSpecs["codebuddy"]
	if !ok || !reflect.DeepEqual(spec.args, []string{"--help"}) {
		t.Fatalf("codebuddy discovery = %#v, want --help", spec)
	}
	help := "Options:\n" +
		"  --model <model>   Model for the current session. Please provide the model ID. Currently supported: (default-model,\n" +
		"                    glm-5.3, custom-local:gpt-4.1-mini)\n" +
		"  --fallback-model <model>  Enable automatic fallback (default: (none))\n"
	models, err := spec.parser([]byte(help))
	if err != nil {
		t.Fatal(err)
	}
	var got [][2]string
	for _, m := range models {
		got = append(got, [2]string{m.ID, m.Label})
	}
	want := [][2]string{{"default-model", "default-model"}, {"glm-5.3", "glm-5.3"}, {"custom-local:gpt-4.1-mini", "gpt-4.1-mini (models.json)"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	if _, err := spec.parser([]byte("Usage: codebuddy [options]\n")); err == nil {
		t.Fatal("help without a model list parsed as an empty catalog")
	}
	if base := Base("codebuddy"); !base.AllowCustom {
		t.Fatalf("codebuddy must keep typed model IDs: %#v", base)
	}
}
