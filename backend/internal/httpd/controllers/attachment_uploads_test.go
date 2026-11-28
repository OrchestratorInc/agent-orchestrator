package controllers_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/config"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd"
)

func newUploadTestServer(t *testing.T, svc *fakeSessionService) (*httptest.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{DataDir: dataDir, AllowedOrigins: []string{"app://renderer"}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(cfg, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, dataDir
}

func TestAttachmentUploadStreamsBodyAndSpawnNamesIt(t *testing.T) {
	svc := newFakeSessionService()
	srv, dataDir := newUploadTestServer(t, svc)

	// Larger than the old 50 MiB per-file cap, and than the inline body limit.
	const size = 150 << 20
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/attachment-uploads", io.LimitReader(zeros{}, size))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201; body=%s", resp.StatusCode, body)
	}
	var uploaded struct {
		UploadID string `json:"uploadId"`
		Bytes    int64  `json:"bytes"`
	}
	mustJSON(t, body, &uploaded)
	if uploaded.Bytes != size || !strings.HasPrefix(uploaded.UploadID, "upload-") {
		t.Fatalf("upload = %+v, want %d bytes", uploaded, size)
	}
	info, err := os.Stat(filepath.Join(dataDir, "attachment-uploads", uploaded.UploadID))
	if err != nil || info.Size() != size {
		t.Fatalf("uploaded file = %v, %v", info, err)
	}

	spawn, _ := json.Marshal(map[string]any{
		"projectId": "ao", "kind": "worker", "harness": "codex", "prompt": "inspect the video",
		"attachments": []map[string]string{{"mimeType": "video/mp4", "uploadId": uploaded.UploadID}},
	})
	resBody, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", string(spawn))
	if status != http.StatusCreated {
		t.Fatalf("spawn naming an upload = %d, want 201; body=%s", status, resBody)
	}
	if got := svc.lastSpawn.Attachments; len(got) != 1 || got[0].Upload != uploaded.UploadID || got[0].Ext != ".mp4" || got[0].Data != nil {
		t.Fatalf("spawn attachments = %+v", got)
	}
}

func TestAttachmentUploadRejectsEmptyBody(t *testing.T) {
	srv, _ := newUploadTestServer(t, newFakeSessionService())
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/attachment-uploads", "")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_ATTACHMENT_DATA")
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
