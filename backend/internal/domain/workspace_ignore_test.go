package domain

import (
	"runtime"
	"testing"
)

func TestWorkspaceIgnorePatternEscapesGlobCharacters(t *testing.T) {
	cases := map[string]string{
		"notes":     "/notes/",
		"notes[1]":  `/notes\[1]/`,
		"a*b?c":     `/a\*b\?c/`,
		"my notes ": `/my\ notes\ /`,
	}
	// A backslash is a path separator on Windows and a literal elsewhere.
	if runtime.GOOS == "windows" {
		cases[`back\slash`] = "/back/slash/"
	} else {
		cases[`back\slash`] = `/back\\slash/`
	}
	for rel, want := range cases {
		if got := WorkspaceIgnorePattern(rel); got != want {
			t.Errorf("WorkspaceIgnorePattern(%q) = %q, want %q", rel, got, want)
		}
	}
}
