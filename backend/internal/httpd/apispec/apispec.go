// Package apispec embeds the OpenAPI document, looks up per-operation
// slices, and writes the locked 501 envelope. The 501 body carries the
// operation's slice of the OpenAPI document so consumers discover the
// contract from the endpoint itself — no duplicate planned/contract
// metadata lives in code.
//
// The same document is served verbatim at /api/v1/openapi.yaml so
// tooling that wants the whole spec can fetch it once.
package apispec

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5/middleware"
	yaml "gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var openapiYAML []byte

// Spec is the parsed, in-memory view of the embedded OpenAPI document. It
// preserves the YAML shape verbatim so the JSON we emit on 501 responses
// matches the on-disk source.
type Spec struct {
	doc     map[string]any
	rawYAML []byte
}

var (
	defaultOnce sync.Once
	defaultSpec *Spec
	defaultErr  error
)

// Default returns the process-wide spec parsed from the embedded YAML. It
// panics on a malformed embed — that is a build-time bug, not a runtime
// one, so failing fast at first use is the right call.
func Default() *Spec {
	defaultOnce.Do(func() {
		s, err := New(openapiYAML)
		defaultSpec = s
		defaultErr = err
	})
	if defaultErr != nil {
		panic(fmt.Sprintf("apispec: embedded openapi.yaml failed to parse: %v", defaultErr))
	}
	return defaultSpec
}

// New parses the supplied YAML bytes. Exposed so tests can construct an
// independent spec without touching the embedded default.
func New(yamlBytes []byte) (*Spec, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("parse openapi: empty document")
	}
	return &Spec{doc: doc, rawYAML: yamlBytes}, nil
}

// YAML returns the raw YAML bytes this spec was built from.
func (s *Spec) YAML() []byte {
	return s.rawYAML
}

// Operation returns the spec slice for a single (method, path) pair, ready
// to be JSON-serialised. The slice is the OpenAPI Operation object (the
// inner block under e.g. paths./projects.get), with parent path-level
// parameters merged in for completeness.
//
// Returns nil if the path or method is not in the spec; that is treated as
// a developer error (route registered without spec coverage) — callers
// log/fail loudly rather than silently writing a partial 501 body.
func (s *Spec) Operation(method, path string) map[string]any {
	paths, _ := s.doc["paths"].(map[string]any)
	if paths == nil {
		return nil
	}
	pathItem, _ := paths[path].(map[string]any)
	if pathItem == nil {
		return nil
	}
	op, _ := pathItem[strings.ToLower(method)].(map[string]any)
	if op == nil {
		return nil
	}

	// Path-level parameters apply to every method on that path; merge them
	// in so the slice is self-contained.
	out := make(map[string]any, len(op)+1)
	for k, v := range op {
		out[k] = v
	}
	if params, ok := pathItem["parameters"]; ok {
		// Prefer the operation's own parameters when both are present;
		// otherwise inherit from the path level.
		if _, exists := out["parameters"]; !exists {
			out["parameters"] = params
		}
	}
	return out
}

// Inline returns node with each local $ref replaced by the schema it names.
func (s *Spec) Inline(node any) any {
	return s.inline(node, 0)
}

const maxInlineDepth = 8

func (s *Spec) inline(node any, depth int) any {
	switch value := node.(type) {
	case map[string]any:
		if ref, ok := value["$ref"].(string); ok && strings.HasPrefix(ref, "#/") && depth < maxInlineDepth {
			target := any(s.doc)
			for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
				fields, _ := target.(map[string]any)
				target = fields[part]
			}
			if target != nil {
				return s.inline(target, depth+1)
			}
			return value
		}
		out := make(map[string]any, len(value))
		for key, child := range value {
			out[key] = s.inline(child, depth)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = s.inline(child, depth)
		}
		return out
	}
	return node
}

// notImplementedResponse is the wire shape for 501 — APIError envelope
// plus a `spec` field carrying the operation slice. Mirrors the
// NotImplementedResponse schema in openapi.yaml.
type notImplementedResponse struct {
	Error     string         `json:"error"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId,omitempty"`
	Spec      map[string]any `json:"spec"`
}

// NotImplemented writes the locked 501 envelope, embedding the OpenAPI
// Operation slice for the capability that is currently unavailable.
func NotImplemented(w http.ResponseWriter, r *http.Request, method, path string) {
	op := Default().Operation(method, path)
	if op == nil {
		panic(fmt.Sprintf("apispec: missing operation for %s %s", method, path))
	}
	body := notImplementedResponse{
		Error:     "not_implemented",
		Code:      "NOT_IMPLEMENTED",
		Message:   method + " " + path + " is unavailable in this daemon",
		RequestID: middleware.GetReqID(r.Context()),
		Spec:      op,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	// A write error here means the client went away mid-response.
	_ = json.NewEncoder(w).Encode(body)
}

// ServeYAML serves the embedded OpenAPI document for SDK generators, tests, and
// developer tooling.
func ServeYAML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(openapiYAML)
}
