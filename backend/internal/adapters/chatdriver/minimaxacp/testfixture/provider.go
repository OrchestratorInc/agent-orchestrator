// Package testfixture supplies a credential-free model endpoint for MiniMax
// native and real-daemon conformance tests. It never emulates the ACP server.
package testfixture

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// New starts an isolated scripted Anthropic endpoint and writes a native source
// profile containing only a synthetic key. Cleanup belongs to the calling test.
func New(t *testing.T) *Provider {
	t.Helper()
	p := &Provider{t: t, SourceDir: t.TempDir(), home: t.TempDir(), protected: t.TempDir()}
	server := httptest.NewServer(p)
	t.Cleanup(server.Close)
	p.URL = server.URL
	config := fmt.Sprintf(`defaultModel: custom_provider:ao/native-test
permissionMode: default
custom_provider:
  ao:
    enabled: true
    api: anthropic-messages
    options:
      baseURL: %q
      apiKey: ao-fixture-key
    models:
      native-test:
        name: AO native conformance
        context_window: 65536
        max_output_tokens: 4096
`, server.URL)
	if err := os.WriteFile(filepath.Join(p.SourceDir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Env selects only the isolated source profile and home for a native launch.
func (e *Provider) Env() map[string]string {
	return map[string]string{
		"MINIMAX_DATA_DIR": e.SourceDir, "MAVIS_DATA_DIR": e.SourceDir,
		"HOME": e.home, "USERPROFILE": e.home, "XDG_CONFIG_HOME": e.home,
		"MCODE_LLM_FIRST_EVENT_TIMEOUT_MS": "10000", "MCODE_LLM_STREAM_IDLE_TIMEOUT_MS": "10000",
	}
}

// ProtectedPath names a controlled target outside the native workspace so
// MiniMax's default permission policy has to ask before writing it.
func (e *Provider) ProtectedPath(name string) string {
	return filepath.Join(e.protected, name)
}

// SameLinuxProcess verifies both process identity and a non-zombie running state.
func SameLinuxProcess(pid int, start string) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return false
	}
	fields := strings.Fields(string(b)[end+1:])
	return len(fields) > 19 && fields[0] != "Z" && fields[19] == start
}

// Provider speaks only the Anthropic stream used by the explicitly
// configured test model. Tool selection comes from the native request catalog.
type Provider struct {
	SourceDir string
	URL       string
	home      string
	t         *testing.T
	protected string
	mu        sync.Mutex
	requests  map[string][]byte
	counts    map[string]int
}

// AssertRequestContains checks the full actual native request for a synthetic marker.
func (e *Provider) AssertRequestContains(t *testing.T, prompt, text string) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !strings.Contains(string(e.requests[prompt]), text) {
		keys := make([]string, 0, len(e.requests))
		for key := range e.requests {
			if len(key) > 160 {
				key = key[:160] + "..."
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		t.Fatalf("native request for %q missing synthetic marker %q; captured prompt keys=%q; matching request bytes=%d", prompt, text, keys, len(e.requests[prompt]))
	}
}

func (e *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/messages") {
		http.Error(w, "unexpected native endpoint", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "read request", http.StatusBadRequest)
		return
	}
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal(body, &request) != nil || len(request.Messages) == 0 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	latest := request.Messages[len(request.Messages)-1].Content
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(latest, &blocks)
	prompt := ""
	toolResult := false
	for _, block := range blocks {
		if block.Type == "text" {
			prompt += block.Text
		}
		if block.Type == "tool_result" {
			toolResult = true
		}
	}
	if prompt == "" {
		_ = json.Unmarshal(latest, &prompt)
	}
	// MiniMax enriches a user message with native agent context. Correlate the
	// final standalone synthetic task label, retaining the full actual request
	// body for instruction/history assertions. A tool result is never a task.
	if !toolResult {
		if label := promptLabel(prompt); label != "" {
			prompt = label
		}
	}
	e.mu.Lock()
	if e.requests == nil {
		e.requests = map[string][]byte{}
		e.counts = map[string]int{}
	}
	e.requests[prompt] = append([]byte(nil), body...)
	e.counts[prompt]++
	e.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	flush := w.(http.Flusher)
	emit := func(name string, data any) {
		encoded, _ := json.Marshal(data)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, encoded)
		flush.Flush()
	}
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "native-fixture", "type": "message", "role": "assistant", "model": "native-test", "content": []any{}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}})
	command := ""
	if !toolResult {
		switch strings.TrimSpace(prompt) {
		case "ao-approve":
			command = fmt.Sprintf("printf approved > %q", filepath.Join(e.protected, "approved.txt"))
		case "ao-reject":
			command = fmt.Sprintf("printf rejected > %q", filepath.Join(e.protected, "rejected.txt"))
		case "ao-edit":
			command = "printf native-proof >> ao-native-proof.txt"
		case "ao-after-restore":
			command = "printf restored >> ao-native-proof.txt"
		case "ao-recovery":
			command = `python3 -c 'import os,json,time; s=open("/proc/self/stat").read().rsplit(")",1)[1].split()[19]; open("recovery-started.json","w").write(json.dumps({"pid":os.getpid(),"start":s})); time.sleep(3); open("recovery-finished.txt","w").write("completed")'`
		case "ao-cancel":
			command = `python3 -c 'import os,json,time; s=open("/proc/self/stat").read().rsplit(")",1)[1].split()[19]; open("started.json","w").write(json.dumps({"pid":os.getpid(),"start":s})); time.sleep(12); open("late.txt","w").write("late")'`
		}
	}
	stop := "end_turn"
	if command != "" {
		tool := ""
		for _, candidate := range request.Tools {
			if strings.EqualFold(candidate.Name, "bash") {
				tool = candidate.Name
			}
		}
		if tool == "" {
			e.t.Error("released native request did not advertise Bash")
			return
		}
		input, _ := json.Marshal(map[string]any{"command": command, "timeout": 60000, "description": "AO controlled native conformance"})
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "tool-" + strings.TrimSpace(prompt), "name": tool, "input": map[string]any{}}})
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}})
		stop = "tool_use"
	} else {
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		for _, delta := range []string{"native streamed ", "answer"} {
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": delta}})
			time.Sleep(250 * time.Millisecond)
		}
	}
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 10}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

// RequestCount counts actual native model requests for one synthetic task.
func (e *Provider) RequestCount(prompt string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[prompt]
}

var knownPrompt = regexp.MustCompile(`ao-(?:after-restore|after-cancel|approve|recovery|reject|cancel|resume|seed|edit)`)

func promptLabel(text string) string {
	label := ""
	for _, location := range knownPrompt.FindAllStringIndex(text, -1) {
		start, end := location[0], location[1]
		if start > 0 && labelCharacter(text[start-1]) || end < len(text) && labelCharacter(text[end]) {
			continue
		}
		label = text[start:end]
	}
	return label
}

func labelCharacter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}
