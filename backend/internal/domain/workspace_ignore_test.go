package domain

import "testing"

func TestWorkspaceIgnorePatternEscapesGlobCharacters(t *testing.T) {
	for rel, want := range map[string]string{
		"notes":      "/notes/",
		"notes[1]":   `/notes\[1]/`,
		"a*b?c":      `/a\*b\?c/`,
		"my notes ":  `/my\ notes\ /`,
		`back\slash`: `/back\\slash/`,
	} {
		if got := WorkspaceIgnorePattern(rel); got != want {
			t.Errorf("WorkspaceIgnorePattern(%q) = %q, want %q", rel, got, want)
		}
	}
}
