package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/gmi"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

// gmiToolState returns a state whose client talks to the given server, so a
// tool call can be exercised without reaching GMI Cloud.
func gmiToolState(t *testing.T, handler http.HandlerFunc) *mcpState {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := gmi.NewWithBaseURL(server.URL, "test-key")
	if err != nil {
		t.Fatalf("NewWithBaseURL: %v", err)
	}
	return &mcpState{gmiClient: client}
}

// gmiResultText returns the text of one tool result.
func gmiResultText(t *testing.T, result map[string]any) string {
	t.Helper()
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result has no content: %v", result)
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content block = %v, want an object", content[0])
	}
	text, _ := block["text"].(string)
	return text
}

// gmiResultJSON reads the JSON one tool result carries.
func gmiResultJSON(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	text := gmiResultText(t, result)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("result text is not JSON (%v): %s", err, text)
	}
	return decoded
}

// gmiToolFailed reports whether a tool result is an error.
func gmiToolFailed(result map[string]any) bool {
	failed, _ := result["isError"].(bool)
	return failed
}

func TestGMIToolsAreOfferedOnlyWhenAKeyIsSaved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv(config.EnvMCPToolGroups, config.MCPToolGroupGMI)
	state := &mcpState{}
	if tools := (&server{}).gmiTools(state); tools != nil {
		t.Fatalf("gmiTools = %d tools without a key, want none", len(tools))
	}
	if err := providerkeys.Save(context.Background(), dir, gmiAPIKeyName, "gmi-key"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	tools := (&server{}).gmiTools(state)
	if len(tools) != len(gmiToolDefs) {
		t.Fatalf("gmiTools = %d tools, want %d", len(tools), len(gmiToolDefs))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		names[name] = true
	}
	for _, want := range []string{
		"gmi_search_models", "gmi_get_model", "gmi_estimate_generation", "gmi_submit_generation",
		"gmi_list_generations", "gmi_get_generation", "gmi_upload_media", "gmi_get_account_summary",
	} {
		if !names[want] {
			t.Fatalf("the tool list has no %q", want)
		}
	}
}

// The GMI Cloud tools spend the account's money, so a server that was not told
// it may use them does not offer them, even with a key in the store.
func TestGMIToolsAreWithheldWhenTheGroupIsNotNamed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dir)
	if err := providerkeys.Save(context.Background(), dir, gmiAPIKeyName, "gmi-key"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, groups := range []string{"", "html,cua"} {
		t.Setenv(config.EnvMCPToolGroups, groups)
		if tools := (&server{}).gmiTools(&mcpState{}); tools != nil {
			t.Fatalf("gmiTools = %d tools for AO_MCP_TOOLS=%q, want none", len(tools), groups)
		}
	}
}

func TestGMIToolsAreNotOfferedWhenTheStoreCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv(config.EnvMCPToolGroups, config.MCPToolGroupGMI)
	if err := os.WriteFile(filepath.Join(dir, "provider-api-keys.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write store: %v", err)
	}
	if tools := (&server{}).gmiTools(&mcpState{}); tools != nil {
		t.Fatalf("gmiTools = %d tools with an unreadable store, want none", len(tools))
	}
}

func TestGMISearchModelsFiltersAndDescribesTheCatalog(t *testing.T) {
	state := gmiToolState(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"model_id":"kling-video"},{"model_id":"seedream-image"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"model_id":"kling-video","model_type":"video","provider":"kuaishou","price_info":{"price":100000,"unit":"second"}}`))
	})
	result := (&server{}).callGMISearchModels(context.Background(), state, json.RawMessage(`{"query":"kling"}`))
	if gmiToolFailed(result) {
		t.Fatalf("the call failed: %s", gmiResultText(t, result))
	}
	decoded := gmiResultJSON(t, result)
	models, ok := decoded["models"].([]any)
	if !ok || len(models) != 1 {
		t.Fatalf("models = %v, want the one matching model", decoded["models"])
	}
	entry, _ := models[0].(map[string]any)
	if entry["model_id"] != "kling-video" {
		t.Fatalf("model id = %v, want kling-video", entry["model_id"])
	}
	if entry["modality"] != "video" {
		t.Fatalf("modality = %v, want video", entry["modality"])
	}
}

func TestGMISubmitGenerationSendsTheModelsOwnParameters(t *testing.T) {
	var submitted map[string]any
	state := gmiToolState(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/models/seedream-4"):
			_, _ = w.Write([]byte(`{"model_id":"seedream-4","model_type":"image","parameters":[
				{"name":"prompt","type":"string","required":true},
				{"name":"image_size","type":"string","options":["1024x1024","1024x1536"]}
			]}`))
		case r.URL.Path == "/requests":
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			_, _ = w.Write([]byte(`{"request_id":"req-9"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	args := json.RawMessage(`{"model_id":"seedream-4","prompt":"a cat","parameters":{"aspect_ratio":"3:4","nonsense":true}}`)
	result := (&server{}).callGMISubmitGeneration(context.Background(), state, args)
	if gmiToolFailed(result) {
		t.Fatalf("the call failed: %s", gmiResultText(t, result))
	}
	decoded := gmiResultJSON(t, result)
	if decoded["request_id"] != "req-9" {
		t.Fatalf("request id = %v, want req-9", decoded["request_id"])
	}
	dropped, ok := decoded["dropped_parameters"].([]any)
	if !ok || len(dropped) != 1 || dropped[0] != "nonsense" {
		t.Fatalf("dropped_parameters = %v, want the parameter the model does not take", decoded["dropped_parameters"])
	}
	if submitted["model"] != "seedream-4" {
		t.Fatalf("submitted model = %v, want seedream-4", submitted["model"])
	}
	payload, ok := submitted["payload"].(map[string]any)
	if !ok {
		t.Fatalf("submitted payload = %v, want an object", submitted["payload"])
	}
	if payload["prompt"] != "a cat" {
		t.Fatalf("payload prompt = %v, want the prompt", payload["prompt"])
	}
	if payload["image_size"] != "1024x1536" {
		t.Fatalf("payload image_size = %v, want the size that matches 3:4", payload["image_size"])
	}
}

func TestGMISubmitGenerationRefusesAMissingPrompt(t *testing.T) {
	state := gmiToolState(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model_id":"seedream-4"}`))
	})
	result := (&server{}).callGMISubmitGeneration(context.Background(), state, json.RawMessage(`{"model_id":"seedream-4"}`))
	if !gmiToolFailed(result) {
		t.Fatal("a generation without a prompt must fail")
	}
}

func TestGMIEstimateGenerationReportsAnUnpublishedPriceAsUnknown(t *testing.T) {
	state := gmiToolState(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model_id":"unpriced","model_type":"image"}`))
	})
	result := (&server{}).callGMIEstimateGeneration(context.Background(), state, json.RawMessage(`{"model_id":"unpriced"}`))
	if !gmiToolFailed(result) {
		t.Fatalf("the call succeeded: %s", gmiResultText(t, result))
	}
	if !strings.Contains(gmiResultText(t, result), "no price") {
		t.Fatalf("the failure says nothing about the missing price: %s", gmiResultText(t, result))
	}
}

func TestGMIUploadMediaSendsTheFileAndReturnsItsPublicURL(t *testing.T) {
	var uploaded []byte
	var contentType string
	var uploadServer *httptest.Server
	uploadServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			contentType = r.Header.Get("Content-Type")
			data, _ := io.ReadAll(r.Body)
			uploaded = data
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"upload_url":"` + uploadServer.URL + `/upload","public_url":"https://cdn.example/a.png"}`))
	}))
	defer uploadServer.Close()
	client, err := gmi.NewWithBaseURL(uploadServer.URL, "test-key")
	if err != nil {
		t.Fatalf("NewWithBaseURL: %v", err)
	}
	state := &mcpState{gmiClient: client}
	file := filepath.Join(t.TempDir(), "cat.png")
	if err := os.WriteFile(file, []byte("bytes"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	arguments, err := json.Marshal(map[string]any{"path": file})
	if err != nil {
		t.Fatalf("encode arguments: %v", err)
	}
	result := (&server{}).callGMIUploadMedia(context.Background(), state, arguments)
	if gmiToolFailed(result) {
		t.Fatalf("the call failed: %s", gmiResultText(t, result))
	}
	decoded := gmiResultJSON(t, result)
	if decoded["public_url"] != "https://cdn.example/a.png" {
		t.Fatalf("public_url = %v, want the uploaded file's url", decoded["public_url"])
	}
	if decoded["file_type"] != "png" {
		t.Fatalf("file_type = %v, want png", decoded["file_type"])
	}
	if string(uploaded) != "bytes" {
		t.Fatalf("uploaded %q, want the file's bytes", uploaded)
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", contentType)
	}
}

func TestGMIAccountSummaryCountsTheRequestsAndSaysTheBalanceIsNotAvailable(t *testing.T) {
	state := gmiToolState(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/requests":
			_, _ = w.Write([]byte(`{"data":[
				{"request_id":"a","model":"seedream-4","status":"success","payload":{"prompt":"a cat"}},
				{"request_id":"b","model":"seedream-4","status":"failed","payload":{"prompt":"a dog"}},
				{"request_id":"c","model":"kling-video","status":"processing","payload":{}}
			]}`))
		case strings.HasSuffix(r.URL.Path, "/models/seedream-4"):
			_, _ = w.Write([]byte(`{"model_id":"seedream-4","price_info":{"price":85000,"unit":"image"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	result := (&server{}).callGMIGetAccountSummary(context.Background(), state, json.RawMessage(`{}`))
	if gmiToolFailed(result) {
		t.Fatalf("the call failed: %s", gmiResultText(t, result))
	}
	decoded := gmiResultJSON(t, result)
	if decoded["requests_examined"] != float64(3) {
		t.Fatalf("requests_examined = %v, want 3", decoded["requests_examined"])
	}
	byStatus, ok := decoded["requests_by_status"].(map[string]any)
	if !ok || byStatus["success"] != float64(1) || byStatus["failed"] != float64(1) {
		t.Fatalf("requests_by_status = %v, want one of each", decoded["requests_by_status"])
	}
	if decoded["distinct_models"] != float64(2) {
		t.Fatalf("distinct_models = %v, want 2", decoded["distinct_models"])
	}
	if _, ok := decoded["balance_not_available"]; !ok {
		t.Fatal("the summary does not say that the balance is not available")
	}
}

func TestGMIGetGenerationReturnsTheMediaOfAFinishedRequest(t *testing.T) {
	state := gmiToolState(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"req-1","status":"success","outcome":{"media_urls":["https://cdn/a.png"]}}`))
	})
	result := (&server{}).callGMIGetGeneration(context.Background(), state, json.RawMessage(`{"request_id":"req-1"}`))
	if gmiToolFailed(result) {
		t.Fatalf("the call failed: %s", gmiResultText(t, result))
	}
	decoded := gmiResultJSON(t, result)
	urls, ok := decoded["media_urls"].([]any)
	if !ok || len(urls) != 1 || urls[0] != "https://cdn/a.png" {
		t.Fatalf("media_urls = %v, want the finished media", decoded["media_urls"])
	}
}

func TestGMIUnknownToolIsReported(t *testing.T) {
	result := (&server{}).callGMI(context.Background(), &mcpState{}, "gmi_nope", nil)
	if !gmiToolFailed(result) {
		t.Fatal("an unknown tool must fail")
	}
}
