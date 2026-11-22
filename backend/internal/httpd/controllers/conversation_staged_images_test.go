package controllers_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// A native image may name a file the session already staged instead of carrying
// its bytes, so a client on a slow link sends a pasted image once. These tests
// pin what the agent receives (the staged file's bytes) and what is refused.

const stagedImageName = "attachment-0123abcd.png"

var stagedImageBytes = []byte("\x89PNG\r\n\x1a\nstaged image bytes")

// stagedImagesStub records what the steer and queued-edit routes hand the service.
type stagedImagesStub struct {
	*fakeConversationService
	steered ports.ChatUserMessage
	edited  chatsvc.QueuedMessageEdit
}

func (s *stagedImagesStub) Steer(_ context.Context, _ domain.SessionID, message ports.ChatUserMessage) (chatsvc.SteerResult, error) {
	s.steered = message
	return chatsvc.SteerResult{}, nil
}

func (s *stagedImagesStub) EditQueuedTurn(_ context.Context, _ domain.SessionID, _ string, edit chatsvc.QueuedMessageEdit) error {
	s.edited = edit
	return nil
}

func stagedImagesServer(t *testing.T, stub *stagedImagesStub) *httptest.Server {
	t.Helper()
	return stagedImagesServerWith(t, stub, map[string][]byte{stagedImageName: stagedImageBytes})
}

func stagedImagesServerWith(t *testing.T, stub *stagedImagesStub, files map[string][]byte) *httptest.Server {
	t.Helper()
	dataDir := t.TempDir()
	for name, data := range files {
		if err := attachmentstore.New(dataDir).PutCanonical(context.Background(), "p1-1", name, data); err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpd.NewRouterWithControl(config.Config{DataDir: dataDir}, log, nil, httpd.APIDeps{
		Sessions: newFakeSessionService(), Conversations: stub,
	}, httpd.ControlDeps{}))
	t.Cleanup(server.Close)
	return server
}

func postStagedImageJSON(t *testing.T, server *httptest.Server, path string, body any) (int, []byte) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(server.URL+path, "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, got
}

func stagedImage(path string) map[string]string {
	return map[string]string{"mimeType": "image/png", "path": path}
}

func wantStagedImageContent(t *testing.T, content []ports.ChatContent) {
	t.Helper()
	want := base64.StdEncoding.EncodeToString(stagedImageBytes)
	if len(content) != 1 || content[0].Type != "image" || content[0].MIMEType != "image/png" || content[0].Data != want {
		t.Fatalf("content = %#v, want the staged file's bytes as one png image", content)
	}
}

func TestSendConversationLoadsStagedImagePath(t *testing.T) {
	stub := &stagedImagesStub{fakeConversationService: &fakeConversationService{}}
	server := stagedImagesServer(t, stub)
	status, body := postStagedImageJSON(t, server, "/api/v1/sessions/p1-1/conversation/messages", map[string]any{
		"text":        "then i got this .ao/attachments/" + stagedImageName,
		"attachments": []map[string]string{stagedImage(".ao/attachments/" + stagedImageName)},
	})
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	wantStagedImageContent(t, stub.sent.Content)
}

func TestSteerAndQueuedEditLoadStagedImagePath(t *testing.T) {
	stub := &stagedImagesStub{fakeConversationService: &fakeConversationService{}}
	server := stagedImagesServer(t, stub)
	image := []map[string]string{stagedImage(".ao/attachments/" + stagedImageName)}

	if status, body := postStagedImageJSON(t, server, "/api/v1/sessions/p1-1/conversation/steer",
		map[string]any{"text": "look", "attachments": image}); status != http.StatusAccepted {
		t.Fatalf("steer status = %d, body = %s", status, body)
	}
	wantStagedImageContent(t, stub.steered.Content)

	if status, body := postStagedImageJSON(t, server, "/api/v1/sessions/p1-1/conversation/turns/turn-queued/queue/edit",
		map[string]any{"text": "look again", "attachments": image}); status != http.StatusNoContent {
		t.Fatalf("edit status = %d, body = %s", status, body)
	}
	wantStagedImageContent(t, stub.edited.Content)
}

func TestStagedImagePathRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		image map[string]string
		code  string
	}{
		{"not staged by this session", stagedImage(".ao/attachments/attachment-ffff.png"), "ATTACHMENT_NOT_FOUND"},
		{"outside the attachment folder", stagedImage("src/" + stagedImageName), "INVALID_ATTACHMENT_PATH"},
		{"traversal", stagedImage(".ao/attachments/../../etc/passwd"), "INVALID_ATTACHMENT_PATH"},
		{"nested", stagedImage(".ao/attachments/nested/" + stagedImageName), "INVALID_ATTACHMENT_PATH"},
		{"both data and path", map[string]string{"mimeType": "image/png", "data": "aW1hZ2U=", "path": ".ao/attachments/" + stagedImageName}, "INVALID_ATTACHMENT_DATA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stagedImagesStub{fakeConversationService: &fakeConversationService{}}
			server := stagedImagesServer(t, stub)
			status, body := postStagedImageJSON(t, server, "/api/v1/sessions/p1-1/conversation/messages", map[string]any{
				"text": "inspect", "attachments": []map[string]string{tc.image},
			})
			assertErrorCode(t, body, status, http.StatusBadRequest, tc.code)
			if stub.sent.Text != "" {
				t.Fatalf("service called with %#v, want refusal before delivery", stub.sent)
			}
		})
	}
}

// A file staged by one session is not readable through another session's route.
func TestStagedImagePathIsScopedToItsSession(t *testing.T) {
	stub := &stagedImagesStub{fakeConversationService: &fakeConversationService{}}
	server := stagedImagesServer(t, stub)
	status, body := postStagedImageJSON(t, server, "/api/v1/sessions/p1-2/conversation/messages", map[string]any{
		"text": "inspect", "attachments": []map[string]string{stagedImage(".ao/attachments/" + stagedImageName)},
	})
	assertErrorCode(t, body, status, http.StatusBadRequest, "ATTACHMENT_NOT_FOUND")
}

// Review conversations have no session store to read a path from.
func TestReviewSendRefusesStagedImagePath(t *testing.T) {
	stub := &stagedImagesStub{fakeConversationService: &fakeConversationService{}}
	server := stagedImagesServer(t, stub)
	status, body := postStagedImageJSON(t, server, "/api/v1/reviews/review-1/conversation/messages", map[string]any{
		"text": "inspect", "attachments": []map[string]string{stagedImage(".ao/attachments/" + stagedImageName)},
	})
	assertErrorCode(t, body, status, http.StatusBadRequest, "ATTACHMENT_PATH_UNSUPPORTED")
}

func TestConversationSnapshotAdvertisesStagedImages(t *testing.T) {
	body := conversationSnapshotBody(t, chatsvc.Snapshot{
		Conversation: domain.ConversationRecord{ID: "conversation-1"},
		SessionID:    domain.SessionID("p1-1"),
	})
	if body["supportsStagedImages"] != true {
		t.Fatalf("supportsStagedImages = %#v, want true", body["supportsStagedImages"])
	}
}

// A path is a few bytes that expand to a whole file, so the limits on decoded
// images are applied before any file is read, not after every one has been.
func TestStagedImagePathsAreBoundedBeforeTheyAreRead(t *testing.T) {
	const nineMiB, tenMiB = 9 << 20, 10 << 20
	big := "attachment-0123abce.png"
	oversized := "attachment-0123abcf.png"
	files := map[string][]byte{big: make([]byte, nineMiB), oversized: make([]byte, tenMiB+1)}
	path := func(name string) map[string]string { return stagedImage(".ao/attachments/" + name) }
	repeat := func(image map[string]string, n int) []map[string]string {
		out := make([]map[string]string, n)
		for i := range out {
			out[i] = image
		}
		return out
	}
	eightMiB := map[string]string{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(make([]byte, 8<<20))}
	for _, tc := range []struct {
		name   string
		images []map[string]string
		code   string
	}{
		// None of these files exist: refusing for the count shows nothing was opened first.
		{"more images than a message takes", repeat(path("attachment-missing.png"), 9), "TOO_MANY_ATTACHMENTS"},
		{"one file over the per-image limit", []map[string]string{path(oversized)}, "ATTACHMENT_TOO_LARGE"},
		{"the same file named until it passes the total", repeat(path(big), 3), "ATTACHMENTS_TOO_LARGE"},
		{"inline bytes and staged paths together over the total", []map[string]string{eightMiB, path(big), path(big)}, "ATTACHMENTS_TOO_LARGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stagedImagesStub{fakeConversationService: &fakeConversationService{}}
			server := stagedImagesServerWith(t, stub, files)
			status, body := postStagedImageJSON(t, server, "/api/v1/sessions/p1-1/conversation/messages", map[string]any{
				"text": "inspect", "attachments": tc.images,
			})
			assertErrorCode(t, body, status, http.StatusBadRequest, tc.code)
			if stub.sent.Text != "" {
				t.Fatalf("service called with %d content blocks, want refusal before delivery", len(stub.sent.Content))
			}
		})
	}

	// Two 9 MiB files fit the 25 MiB total and still go through.
	stub := &stagedImagesStub{fakeConversationService: &fakeConversationService{}}
	server := stagedImagesServerWith(t, stub, files)
	if status, body := postStagedImageJSON(t, server, "/api/v1/sessions/p1-1/conversation/messages", map[string]any{
		"text": "inspect", "attachments": repeat(path(big), 2),
	}); status != http.StatusAccepted {
		t.Fatalf("status = %d, body = %.200s", status, body)
	}
	if len(stub.sent.Content) != 2 {
		t.Fatalf("content = %d blocks, want 2", len(stub.sent.Content))
	}
}

// The decoder skips CR and LF in inline base64, so the budget does too: a valid
// mixed request keeps the whole 25 MiB.
func TestStagedImageBudgetCountsInlineBytesAsDecoded(t *testing.T) {
	const eightMiB = 8 << 20
	staged := "attachment-0123abd0.png"
	encoded := base64.StdEncoding.EncodeToString(make([]byte, eightMiB))
	wrapped := encoded[:1000] + "\r\n" + encoded[1000:]
	stub := &stagedImagesStub{fakeConversationService: &fakeConversationService{}}
	server := stagedImagesServerWith(t, stub, map[string][]byte{staged: make([]byte, eightMiB)})
	status, body := postStagedImageJSON(t, server, "/api/v1/sessions/p1-1/conversation/messages", map[string]any{
		"text": "inspect",
		"attachments": []map[string]string{
			{"mimeType": "image/png", "data": wrapped},
			stagedImage(".ao/attachments/" + staged),
			stagedImage(".ao/attachments/" + staged),
		},
	})
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, body = %.200s; want 24 MiB of images accepted", status, body)
	}
	if len(stub.sent.Content) != 3 {
		t.Fatalf("content = %d blocks, want 3", len(stub.sent.Content))
	}
}
