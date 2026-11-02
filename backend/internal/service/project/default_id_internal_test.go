package project

import (
	"path/filepath"
	"testing"
)

func TestDefaultProjectIDFromFolderName(t *testing.T) {
	cases := map[string]string{
		"agent-orchestrator": "agent-orchestrator",
		"ronish.dev":         "ronish.dev",
		"My_Repo":            "my_repo",
		"My App":             "my-app",
		"app+web":            "app-web",
		"app (v2)":           "app-v2",
		"app@2":              "app-2",
		"app#1":              "app-1",
		"a, b & c":           "a-b-c",
		"café":               "cafe",
		"Résumé_Tool":        "resume_tool",
		"über.dev":           "uber.dev",
		"_tools":             "tools",
		".dotfiles":          "dotfiles",
		"a..b":               "a.b",
		"日本語":                "project",
		"+++":                "project",
	}
	for folder, want := range cases {
		got := defaultProjectID(filepath.Join("/repos", folder))
		if string(got) != want {
			t.Errorf("defaultProjectID(%q) = %q, want %q", folder, got, want)
		}
		if err := validateProjectID(got); err != nil {
			t.Errorf("defaultProjectID(%q) = %q fails validation: %v", folder, got, err)
		}
	}
}
