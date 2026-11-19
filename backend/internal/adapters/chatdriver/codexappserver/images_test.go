package codexappserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// openImageConversation starts a conversation against the scripted server with a
// real workspace directory, so the payload checks work on every platform.
func openImageConversation(t *testing.T) (*conversation, *scriptedServer) {
	t.Helper()
	d, srv := newTestDriver(t)
	opened, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	conv, ok := opened.(*conversation)
	if !ok {
		t.Fatalf("Start returned %T, want *conversation", opened)
	}
	return conv, srv
}

// The user-visible claim of issue #8421: an attached image reaches the provider as
// an image, not as a staged path in the prompt text.
func TestSendTurnCarriesNativeImages(t *testing.T) {
	conv, srv := openImageConversation(t)

	if _, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text:   "what is wrong in this screenshot?",
		Origin: domain.MessageOriginHuman,
		Content: []ports.ChatContent{{
			Type: "image", Data: "aGVsbG8=", MIMEType: "image/png", Name: "shot.png",
		}},
	}); err != nil {
		t.Fatalf("SendTurn: %v", err)
	}

	sent := srv.awaitFrame(func(f frame) bool { return f.Method == codexproto.MethodTurnStart })
	var params codexproto.TurnStartParams
	if err := json.Unmarshal(sent.Params, &params); err != nil {
		t.Fatalf("turn/start params: %v", err)
	}
	if len(params.Input) != 2 || params.Input[0].Type != codexproto.UserInputTypeText {
		t.Fatalf("input = %+v, want text followed by image", params.Input)
	}
	if params.Input[0].Text == nil || *params.Input[0].Text != "what is wrong in this screenshot?" {
		t.Errorf("input text = %v", params.Input[0].Text)
	}
	if params.Input[1].Type != codexproto.UserInputTypeImage || params.Input[1].URL == nil ||
		*params.Input[1].URL != "data:image/png;base64,aGVsbG8=" {
		t.Errorf("image input = %+v", params.Input[1])
	}
	// The image is the attachment, not a workspace path the agent has to read.
	for _, item := range params.Input {
		if item.Text != nil && strings.Contains(*item.Text, "shot.png") {
			t.Errorf("prompt text still names the file: %q", *item.Text)
		}
	}
}

// A block the app-server cannot represent is refused before the turn starts. A
// prompt that silently drops an attachment is worse than one that says so.
func TestSendTurnRefusesContentItCannotCarry(t *testing.T) {
	conv, srv := openImageConversation(t)

	_, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text:    "summarize this",
		Content: []ports.ChatContent{{Type: "resource_link", URI: "file:///tmp/a.ts", Name: "a.ts"}},
	})
	if err == nil || !strings.Contains(err.Error(), "resource_link") {
		t.Fatalf("err = %v, want a refusal naming the unsupported block", err)
	}
	if srv.sentMethod(codexproto.MethodTurnStart) {
		t.Error("started a turn whose attachment would have been dropped")
	}
}

// An image without data would have reached the provider as an empty data URL.
func TestSendTurnRefusesAnImageWithoutData(t *testing.T) {
	conv, srv := openImageConversation(t)

	_, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{
		Text:    "look",
		Content: []ports.ChatContent{{Type: "image", MIMEType: "image/png"}},
	})
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("err = %v, want a refusal naming the image", err)
	}
	if srv.sentMethod(codexproto.MethodTurnStart) {
		t.Error("sent a turn with an empty image")
	}
}

// The capability and the protocol have to agree: advertising images while the
// generated protocol has no image input would put an attachment chip in the
// composer that the provider rejects on use.
func TestImagesCapabilityMatchesTheDeclaredProtocol(t *testing.T) {
	if !codexproto.Declares(codexproto.MethodTurnStart) {
		t.Fatalf("generated protocol (%s) does not declare %s",
			codexproto.ProviderVersion, codexproto.MethodTurnStart)
	}
	url := "data:image/png;base64," + redPixelPNG
	got, err := json.Marshal(codexproto.UserInput{Type: codexproto.UserInputTypeImage, URL: &url})
	want := `{"type":"image","url":"` + url + `"}`
	if err != nil || string(got) != want {
		t.Fatalf("generated image input = %s, err = %v; want %s", got, err, want)
	}
	if !capabilities().Has(ports.ChatCapabilityImages) {
		t.Error("the driver can send image input but does not advertise the images capability")
	}
}

func TestTurnInputNormalizesJPEGAndRejectsUnsupportedMIME(t *testing.T) {
	for _, mime := range []string{"IMAGE/JPG", "image/jpeg"} {
		got, err := turnInput(ports.ChatUserMessage{Content: []ports.ChatContent{{Type: "image", MIMEType: mime, Data: "aGVsbG8="}}})
		if err != nil || len(got) != 1 || got[0].URL == nil || *got[0].URL != "data:image/jpeg;base64,aGVsbG8=" {
			t.Fatalf("turnInput(%q) = %v, %v", mime, got, err)
		}
	}
	for _, mime := range []string{"image/bmp", "text/plain"} {
		if _, err := turnInput(ports.ChatUserMessage{Content: []ports.ChatContent{{Type: "image", MIMEType: mime, Data: "aGVsbG8="}}}); err == nil {
			t.Fatalf("turnInput(%q) accepted unsupported input", mime)
		}
	}
}

// redPixelPNG is a 1x1 red PNG, small enough to inline in a live prompt.
const redPixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// TestLiveTurnCarriesNativeImages drives a real `codex app-server`. Skipped unless
// AO_CODEX_LIVE=1: it needs a local Codex install, working auth, and it makes a
// real model call. This is the test that earns the capability: the model can only
// answer the colour in the prompt if the image arrived as an image.
//
//	AO_CODEX_LIVE=1 go test ./internal/adapters/chatdriver/codexappserver/ -run LiveTurnCarriesNativeImages -v
func TestLiveTurnCarriesNativeImages(t *testing.T) {
	if os.Getenv("AO_CODEX_LIVE") != "1" {
		t.Skip("set AO_CODEX_LIVE=1 to run against a real codex app-server")
	}
	bin := os.Getenv("AO_CODEX_BIN")
	if bin == "" {
		bin = "codex"
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("codex binary %q not on PATH: %v", bin, err)
	}

	workspace := t.TempDir()
	dataDir := t.TempDir()
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, "ao-live-images") })
	seedGitWorkspace(t, workspace)

	d := New(livePlugin{bin: bin}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	opened, err := d.Start(ctx, ports.ChatStartConfig{
		SessionID:     "ao-live-images",
		DataDir:       dataDir,
		WorkspacePath: workspace,
		Env:           envMap(),
		// Reading the supplied image needs no full-access sandbox.
		Permissions:  ports.PermissionModeAcceptEdits,
		SystemPrompt: "You are in an automated test. Follow instructions literally.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = opened.Close() }()

	if _, err := opened.SendTurn(ctx, ports.ChatUserMessage{
		Text: "What is the single dominant colour of the attached image? " +
			"Reply with one word and nothing else.",
		ClientMessageID: "live-image-turn",
		Origin:          domain.MessageOriginHuman,
		Content: []ports.ChatContent{{
			Type: "image", Data: redPixelPNG, MIMEType: "image/png", Name: "red.png",
		}},
	}); err != nil {
		t.Fatalf("SendTurn: %v", err)
	}

	var text strings.Builder
	for {
		select {
		case ev, live := <-opened.Events():
			if !live {
				t.Fatal("event stream closed before the turn completed")
			}
			switch ev.Kind {
			case ports.ChatEventMessageCompleted:
				text.WriteString(ev.Text)
				text.WriteString("\n")
			case ports.ChatEventTurnCompleted:
				if ev.TurnState != domain.TurnStateCompleted {
					t.Fatalf("turn state = %q: %v", ev.TurnState, ev.Err)
				}
				if !strings.Contains(strings.ToLower(text.String()), "red") {
					t.Errorf("the model never saw a red image; final text was:\n%s", text.String())
				}
				t.Logf("image turn: %s", strings.TrimSpace(text.String()))
				return
			case ports.ChatEventControllerState:
				if ev.ControllerState == ports.ChatControllerStopped {
					t.Fatalf("controller stopped before the turn completed: %v", ev.Err)
				}
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for the image turn: %v", ctx.Err())
		}
	}
}
