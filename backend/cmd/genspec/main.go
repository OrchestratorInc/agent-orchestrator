package main

import (
	"flag"
	"log"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec/specgen"
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
