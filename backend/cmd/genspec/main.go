// Command genspec writes the code-first OpenAPI document produced by
// apispec.Build() to disk. It is invoked via `go generate` (see
// internal/httpd/apispec/gen.go); the output openapi.yaml is committed and
// embedded by the apispec package.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apispec/specgen"
)

func main() {
	out := flag.String("out", "openapi.yaml", "output path for the generated OpenAPI document")
	flag.Parse()

	doc, err := specgen.Build()
	if err != nil {
		log.Fatalf("genspec: build openapi: %v", err)
	}
	if err := os.WriteFile(*out, doc, 0o600); err != nil {
		log.Fatalf("genspec: write %s: %v", *out, err)
	}
}
