package opencodev2acp

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestLiveOpenCodeV2ACPSteering proves AO's Steer against the real `opencode
// acp` (2.2.7 or later). Run explicitly with AO_LIVE_OPENCODE_V2=1; set
// AO_LIVE_OPENCODE_V2_BIN to pick a binary, otherwise AO's own resolver picks
// the installed OpenCode 2. The model is a scripted OpenAI-compatible server and
// HOME and the XDG directories are temporary, so it needs no account and never
// touches the operator's OpenCode data. CI never depends on it. It runs on
// macOS and Linux only.
//
// It proves: the handshake turns Steer on; guidance sent while a command runs
// joins that turn, reaches the model right after the tool result and the turn
// completes once with nothing left to start on its own; an image in the
// guidance reaches the model; and a steer after the turn ended is refused as
// "send it as a new turn" without starting work.
func TestLiveOpenCodeV2ACPSteering(t *testing.T) {
	if os.Getenv("AO_LIVE_OPENCODE_V2") != "1" {
		t.Skip("set AO_LIVE_OPENCODE_V2=1 to run against the local OpenCode 2 with a scripted model")
	}
	if runtime.GOOS == "windows" {
		// The fixture symlinks the binary as `opencode`, sets a POSIX PATH and has
		// the model run `sleep`, so it only runs on macOS and Linux.
		t.Skip("POSIX-only live fixture; run it on macOS or Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	binary := os.Getenv("AO_LIVE_OPENCODE_V2_BIN")
	if binary == "" {
		resolved, err := opencodev2.New().ResolveBinary(ctx)
		if err != nil {
			t.Skipf("no OpenCode 2 installed: %v", err)
		}
		binary = resolved
	}
	model := newScriptedModel(t)
	isolateOpenCodeV2(t, binary, model.url)

	conversation, err := New(opencodev2.New(), nil).Start(ctx, ports.ChatStartConfig{
		SessionID: "live-opencode-v2-steer", DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
		Env: liveEnv(), Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conversation.(ports.ChatProviderTerminator).Terminate() }()
	if !conversation.Capabilities()[ports.ChatCapabilitySteer] {
		t.Skipf("%s does not advertise ACP steering; OpenCode 2.2.7 or later is required", binary)
	}
	steerer := conversation.(ports.ChatSteerer)

	steerDuringCommand := func(t *testing.T, guidance ports.ChatUserMessage) (string, int) {
		t.Helper()
		before := model.count()
		ref := sendLiveTurn(ctx, t, conversation, "Run the slow check first.")
		for event := nextLiveEvent(ctx, t, conversation); event.Kind != ports.ChatEventActivityStarted; event = nextLiveEvent(ctx, t, conversation) {
			if event.Kind == ports.ChatEventTurnCompleted {
				t.Fatalf("turn ended before its command started: %q", event.TurnState)
			}
		}
		time.Sleep(time.Second)
		landed, err := steerer.Steer(ctx, ref.ProviderTurnID, guidance)
		if err != nil {
			t.Fatalf("Steer: %v", err)
		}
		if landed.ProviderTurnID != ref.ProviderTurnID {
			t.Fatalf("steer landed in %q, want the running turn %q", landed.ProviderTurnID, ref.ProviderTurnID)
		}
		var reply strings.Builder
		for {
			event := nextLiveEvent(ctx, t, conversation)
			if event.Kind == ports.ChatEventMessageDelta {
				reply.WriteString(event.Delta)
			}
			if event.Kind == ports.ChatEventTurnCompleted {
				if event.TurnState != domain.TurnStateCompleted {
					t.Fatalf("steered turn state = %q (%v)", event.TurnState, event.Err)
				}
				break
			}
		}
		// The steer must not leave work behind that starts on its own.
		time.Sleep(2 * time.Second)
		if after := model.count(); after != before+2 {
			t.Fatalf("model requests for one steered turn = %d, want 2 (command, then the reply to the guidance)", after-before)
		}
		return reply.String(), before + 1
	}

	t.Run("guidance joins the running command's turn", func(t *testing.T) {
		reply, index := steerDuringCommand(t, ports.ChatUserMessage{Text: "STEER-ME: edit staging.yaml instead"})
		if !strings.Contains(reply, "Switching to staging.yaml") {
			t.Fatalf("reply %q did not follow the guidance", reply)
		}
		request := model.request(index)
		if !request.toolBeforeLastUser || !strings.Contains(request.lastUser, "STEER-ME") {
			t.Fatalf("model did not see the guidance right after the tool result: %+v", request)
		}
	})

	t.Run("an image in the guidance reaches the model", func(t *testing.T) {
		_, index := steerDuringCommand(t, ports.ChatUserMessage{
			Text:    "STEER-ME: look at this screenshot too",
			Content: []ports.ChatContent{{Type: "image", MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(tinyPNG())}},
		})
		request := model.request(index)
		if len(request.images) != 1 || !bytes.Equal(request.images[0], tinyPNG()) {
			t.Fatalf("the steered PNG did not reach the model intact: got %d image(s) in the guidance message", len(request.images))
		}
	})

	t.Run("guidance after the turn ended goes back as a new message", func(t *testing.T) {
		before := model.count()
		ref := sendLiveTurn(ctx, t, conversation, "Say hello.")
		waitForTurnEnd(ctx, t, conversation)
		if _, err := steerer.Steer(ctx, ref.ProviderTurnID, ports.ChatUserMessage{Text: "STEER-ME: too late"}); !errors.Is(err, ports.ErrChatNoSteerableTurn) {
			t.Fatalf("late steer error = %v, want ErrChatNoSteerableTurn", err)
		}
		time.Sleep(2 * time.Second)
		if got := model.count() - before; got != 1 {
			t.Fatalf("model requests after a refused late steer = %d, want only the finished turn's 1", got)
		}
	})
}

type scriptedRequest struct {
	lastUser           string
	toolBeforeLastUser bool
	// images holds the decoded bytes of each image_url data URL in the newest
	// user message, so a test can require the exact PNG it sent.
	images [][]byte
}

type scriptedModel struct {
	url      string
	mu       sync.Mutex
	requests []scriptedRequest
}

func (m *scriptedModel) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *scriptedModel) request(index int) scriptedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= len(m.requests) {
		return scriptedRequest{}
	}
	return m.requests[index]
}

// newScriptedModel answers OpenAI-compatible chat completions. "Run the slow
// check first." gets one six-second shell command; guidance marked STEER-ME
// gets a reply that proves the model saw it. Requests without tools are AO's
// and OpenCode's helper calls (titles) and are not counted.
func newScriptedModel(t *testing.T) *scriptedModel {
	t.Helper()
	model := &scriptedModel{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools    []json.RawMessage `json:"tools"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		last := -1
		for index, message := range body.Messages {
			if message.Role == "user" {
				last = index
			}
		}
		var request scriptedRequest
		toolAfterLastUser := false
		if last >= 0 {
			request.lastUser = string(body.Messages[last].Content)
			request.images = dataURLImages(body.Messages[last].Content)
			request.toolBeforeLastUser = last > 0 && body.Messages[last-1].Role == "tool"
			for _, message := range body.Messages[last+1:] {
				toolAfterLastUser = toolAfterLastUser || message.Role == "tool"
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(chunk map[string]any) {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		delta := func(d map[string]any, finish any) map[string]any {
			return map[string]any{"choices": []any{map[string]any{"index": 0, "delta": d, "finish_reason": finish}}}
		}
		text := "Lab title"
		if len(body.Tools) > 0 {
			model.mu.Lock()
			model.requests = append(model.requests, request)
			calls := len(model.requests)
			model.mu.Unlock()
			switch {
			case strings.Contains(request.lastUser, "STEER-ME"):
				text = "Switching to staging.yaml as you asked."
			case strings.Contains(request.lastUser, "Run the slow check first") && !toolAfterLastUser:
				arguments, _ := json.Marshal(map[string]string{"command": "sleep 6; echo slow-check-done", "description": "slow check"})
				send(delta(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
					"index": 0, "id": fmt.Sprintf("call_%d", calls), "type": "function",
					"function": map[string]any{"name": "shell", "arguments": string(arguments)},
				}}}, nil))
				send(delta(map[string]any{}, "tool_calls"))
				send(map[string]any{"choices": []any{}, "usage": map[string]int{"prompt_tokens": 1000, "completion_tokens": 10, "total_tokens": 1010}})
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				return
			default:
				text = "Edited prod.yaml."
			}
		}
		send(delta(map[string]any{"role": "assistant", "content": text}, nil))
		send(delta(map[string]any{}, "stop"))
		send(map[string]any{"choices": []any{}, "usage": map[string]int{"prompt_tokens": 1000, "completion_tokens": 10, "total_tokens": 1010}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	model.url = server.URL
	return model
}

// isolateOpenCodeV2 gives OpenCode a temporary HOME and XDG directories holding
// only the scripted provider, and puts the chosen binary first on PATH.
func isolateOpenCodeV2(t *testing.T, binary, modelURL string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	config := filepath.Join(root, "config", "opencode")
	for _, dir := range []string{bin, config, filepath.Join(root, "home"), filepath.Join(root, "data"), filepath.Join(root, "state"), filepath.Join(root, "cache")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(binary, filepath.Join(bin, "opencode")); err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]any{
		"update": "disable",
		"model":  "lab/m",
		"providers": map[string]any{"lab": map[string]any{
			"name": "Lab", "package": "aisdk:@ai-sdk/openai-compatible",
			"settings": map[string]string{"apiKey": "lab", "baseURL": modelURL + "/v1"},
			"models": map[string]any{"m": map[string]any{
				"name": "Lab", "capabilities": map[string]any{"tools": true, "input": []string{"text", "image"}, "output": []string{"text"}},
				"cost": map[string]int{"input": 0, "output": 0}, "limit": map[string]int{"context": 100000, "output": 10000},
			}},
		}},
	})
	if err := os.WriteFile(filepath.Join(config, "opencode.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	models := filepath.Join(root, "models.json")
	if err := os.WriteFile(models, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("OPENCODE_DISABLE_AUTOUPDATE", "true")
	t.Setenv("OPENCODE_MODELS_PATH", models)
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
}

func liveEnv() map[string]string {
	out := make(map[string]string)
	for _, pair := range os.Environ() {
		if name, value, ok := strings.Cut(pair, "="); ok && value != "" {
			out[name] = value
		}
	}
	return out
}

func sendLiveTurn(ctx context.Context, t *testing.T, conversation ports.ChatConversation, text string) ports.ChatTurnRef {
	t.Helper()
	ref, err := conversation.SendTurn(ctx, ports.ChatUserMessage{
		Text: text, ClientMessageID: "live-" + time.Now().Format("150405.000000"), Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if starter, ok := conversation.(ports.ChatDeferredTurnStarter); ok {
		if err := starter.StartDeferredTurn(ref.ProviderTurnID); err != nil {
			t.Fatalf("StartDeferredTurn: %v", err)
		}
	}
	return ref
}

func nextLiveEvent(ctx context.Context, t *testing.T, conversation ports.ChatConversation) ports.ChatEvent {
	t.Helper()
	select {
	case event, ok := <-conversation.Events():
		if !ok {
			t.Fatal("conversation closed")
		}
		if event.Kind == ports.ChatEventError {
			t.Fatalf("conversation error: %v", event.Err)
		}
		if event.ProviderEventID != "" {
			if acknowledger, ok := conversation.(ports.ChatProviderEventAcknowledger); ok {
				if err := acknowledger.AcknowledgeProviderEvent(ctx, event.ProviderEventID); err != nil {
					t.Fatalf("AcknowledgeProviderEvent: %v", err)
				}
			}
		}
		return event
	case <-ctx.Done():
		t.Fatalf("live test timed out: %v", ctx.Err())
	}
	return ports.ChatEvent{}
}

func waitForTurnEnd(ctx context.Context, t *testing.T, conversation ports.ChatConversation) {
	t.Helper()
	for nextLiveEvent(ctx, t, conversation).Kind != ports.ChatEventTurnCompleted {
		continue
	}
}

// dataURLImages decodes the base64 payload of every image_url part in an
// OpenAI chat message's content. A part that is not a base64 data URL, or
// whose payload does not decode, contributes an empty entry, so a lost or
// corrupted image never matches the PNG a test sent.
func dataURLImages(content json.RawMessage) [][]byte {
	var parts []struct {
		Type     string `json:"type"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return nil // plain string content has no images
	}
	var images [][]byte
	for _, part := range parts {
		if part.Type != "image_url" {
			continue
		}
		_, payload, ok := strings.Cut(part.ImageURL.URL, ";base64,")
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if !ok || err != nil {
			decoded = nil
		}
		images = append(images, decoded)
	}
	return images
}

// tinyPNG is a valid 8x8 red PNG.
func tinyPNG() []byte {
	chunk := func(kind string, data []byte) []byte {
		var out bytes.Buffer
		_ = binary.Write(&out, binary.BigEndian, uint32(len(data)))
		out.WriteString(kind)
		out.Write(data)
		_ = binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
		return out.Bytes()
	}
	var header bytes.Buffer
	_ = binary.Write(&header, binary.BigEndian, struct {
		Width, Height                                uint32
		Depth, Color, Compression, Filter, Interlace uint8
	}{Width: 8, Height: 8, Depth: 8, Color: 2})
	var rows bytes.Buffer
	for y := 0; y < 8; y++ {
		rows.WriteByte(0)
		rows.Write(bytes.Repeat([]byte{0xff, 0, 0}, 8))
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	_, _ = writer.Write(rows.Bytes())
	_ = writer.Close()
	png := []byte("\x89PNG\r\n\x1a\n")
	png = append(png, chunk("IHDR", header.Bytes())...)
	png = append(png, chunk("IDAT", compressed.Bytes())...)
	return append(png, chunk("IEND", nil)...)
}
