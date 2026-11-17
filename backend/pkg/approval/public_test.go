package approval

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AO Cloud imports this package from another module, which cannot reach any
// internal package of this one.
func TestImportsNothingInternal(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			if path := strings.Trim(spec.Path.Value, `"`); strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal") {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}
