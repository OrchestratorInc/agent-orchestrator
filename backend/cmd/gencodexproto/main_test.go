package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestEnumIdentifierCollisionsPreserveWireValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"enum", `{"type":"string","enum":["openai/form","openaiForm"]}`},
		{"enum union", `{"oneOf":[{"type":"string","enum":["openai/form"]},{"type":"string","enum":["openaiForm"]}]}`},
		{"tagged union", `{"oneOf":[{"type":"object","required":["type"],"properties":{"type":{"type":"string","enum":["openai/form"]}}},{"type":"object","required":["type"],"properties":{"type":{"type":"string","enum":["openaiForm"]}}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var definition schema
			if err := json.Unmarshal([]byte(tc.body), &definition); err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			var inline []inlineType
			g := &generator{}
			g.renderDef(&output, "Collision", &definition, &inline, map[string]bool{})
			for _, value := range []string{`"openai/form"`, `"openaiForm"`} {
				if strings.Count(output.String(), value) != 1 {
					t.Fatalf("wire value %s lost or duplicated:\n%s", value, output.String())
				}
			}
			files := token.NewFileSet()
			file, err := parser.ParseFile(files, "generated.go", "package generated\n"+output.String(), 0)
			if err != nil {
				t.Fatal(err)
			}
			config := &types.Config{}
			if _, err := config.Check("generated", files, []*ast.File{file}, nil); err != nil {
				t.Fatalf("generated enum does not compile: %v\n%s", err, output.String())
			}
		})
	}
}
