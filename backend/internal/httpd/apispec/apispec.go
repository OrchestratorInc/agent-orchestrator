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

type Spec struct {
	doc     map[string]any
	rawYAML []byte
}

var (
	defaultOnce sync.Once
	defaultSpec *Spec
	defaultErr  error
)

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

func (s *Spec) YAML() []byte {
	return s.rawYAML
}

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

	out := make(map[string]any, len(op)+1)
	for k, v := range op {
		out[k] = v
	}
	if params, ok := pathItem["parameters"]; ok {
		if _, exists := out["parameters"]; !exists {
			out["parameters"] = params
		}
	}
	return out
}

type notImplementedResponse struct {
	Error     string         `json:"error"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId,omitempty"`
	Spec      map[string]any `json:"spec"`
}

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
	_ = json.NewEncoder(w).Encode(body)
}

func ServeYAML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(openapiYAML)
}
