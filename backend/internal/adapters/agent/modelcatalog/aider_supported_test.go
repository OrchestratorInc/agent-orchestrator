package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAiderProviderListSkipsProvidersGroupedUnderQualifiedNames(t *testing.T) {
	output := []byte("LiteLLM notice\n[\"openai\", \"vertex_ai\", \"bedrock\", \"\"]\n")
	got := aiderProviderList(output, []string{"openai", "vertex_ai-language-models", "bedrock_converse"})
	if want := []string{"openai", "bedrock"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}
	if got := aiderProviderList([]byte("not json"), nil); got != nil {
		t.Fatalf("unparseable output = %v", got)
	}
}

func TestAiderInterpreterReadsLauncherShebang(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	python := filepath.Join(dir, "venv", "bin", "python3")
	if got := aiderInterpreter(write("aider", "#!"+python+"\nimport aider\n")); got != python {
		t.Fatalf("interpreter = %q, want %q", got, python)
	}
	for name, body := range map[string]string{
		"env-launcher": "#!/usr/bin/env python3\n",
		"shell":        "#!/bin/sh\nexec aider\n",
		"binary":       "\x7fELF",
	} {
		if got := aiderInterpreter(write(name, body)); got != "" {
			t.Fatalf("%s interpreter = %q", name, got)
		}
	}
}
