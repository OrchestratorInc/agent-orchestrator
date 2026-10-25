package domain

import (
	"errors"
	"testing"
)

func TestCleanArtifactPath(t *testing.T) {
	valid := map[string]string{
		"report.html":           "report.html",
		"assets/chart.png":      "assets/chart.png",
		"caf\u0065\u0301.md":    "caf\u00e9.md",
		"nested/.hidden/x.txt":  "nested/.hidden/x.txt",
		"docs/.versions/a.html": "docs/.versions/a.html",
		"console.md":            "console.md",
		"con-notes.md":          "con-notes.md",
	}
	for input, want := range valid {
		got, err := CleanArtifactPath(input)
		if err != nil || got != want {
			t.Errorf("CleanArtifactPath(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	invalid := []string{
		"", "/etc/passwd", "../secret", "a/../../b", "a/./b", "a//b", "a/",
		`a\b`, `..\..\x`, "C:/x", "c:x", ".versions/report.html", "..", ".",
		"bad\x00name", "line\nbreak", string([]byte{0xff, 0xfe}),
		".Versions/report.html", ".VERSIONS/a.md", "evil\u202egnp.html", "zero\u200bwidth.md",
		"CON", "nul.txt", "docs/com1.md", "LPT9.log",
	}
	for _, input := range invalid {
		if got, err := CleanArtifactPath(input); !errors.Is(err, ErrInvalidArtifactPath) {
			t.Errorf("CleanArtifactPath(%q) = %q, %v; want ErrInvalidPath", input, got, err)
		}
	}
}
