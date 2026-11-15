package approval

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// AO Cloud imports this package from another module, which cannot reach any
// internal package of this one.
func TestImportsNothingInternal(t *testing.T) {
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, spec := range file.Imports {
				if path := strings.Trim(spec.Path.Value, `"`); strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal") {
					t.Errorf("%s imports %s", name, path)
				}
			}
		}
	}
}
