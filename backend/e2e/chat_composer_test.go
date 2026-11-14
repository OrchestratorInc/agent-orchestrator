//go:build !windows

package e2e

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestChatSkillsComeFromTheProvider(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "skills")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	var catalog struct {
		Skills []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
			Source      string `json:"source"`
		} `json:"skills"`
	}
	d.mustCall("GET", "/sessions/"+session+"/conversation/skills", http.StatusOK, nil, &catalog)

	if len(catalog.Skills) == 0 {
		t.Skip("provider reported no skills on this machine; nothing to assert about rows")
	}

	seen := map[string]bool{}
	for _, skill := range catalog.Skills {
		if skill.Name == "" {
			t.Errorf("skill %+v has no name, so nothing could be inserted for it", skill)
		}
		if skill.DisplayName == "" {
			t.Errorf("skill %q has no label, so its menu row would render blank", skill.Name)
		}
		if seen[skill.Name] {
			t.Errorf("skill %q reported twice", skill.Name)
		}
		seen[skill.Name] = true
		if strings.ContainsAny(skill.Name, " \t\n") {
			t.Errorf("skill name %q contains whitespace", skill.Name)
		}
	}
}

func TestChatSkillsAreRefusedForATUISession(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "skillstui")

	tui := spawn(t, d, map[string]any{
		"projectId": project, "kind": "worker", "harness": "codex", "mode": "tui",
		"prompt": "do nothing",
	}).Session.ID

	status, body := d.callExpectingError("GET", "/sessions/"+tui+"/conversation/skills", nil)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%+v)", status, body)
	}
	if body.Code != "SESSION_MODE_MISMATCH" {
		t.Errorf("code = %q, want SESSION_MODE_MISMATCH", body.Code)
	}
}

func TestChatAttachmentIsStagedIntoTheWorktreeAndReadableByTheAgent(t *testing.T) {
	requireE2E(t)
	d := startDaemon(t, t.TempDir())
	project := seedProject(t, d, "attachments")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg=="

	var staged struct {
		SessionID string   `json:"sessionId"`
		Paths     []string `json:"paths"`
	}
	d.mustCall("POST", "/sessions/"+session+"/attachments", http.StatusCreated,
		map[string]any{"attachments": []map[string]any{{"mimeType": "image/png", "data": onePixelPNG}}},
		&staged)

	if len(staged.Paths) != 1 {
		t.Fatalf("staged %d paths, want 1: %+v", len(staged.Paths), staged.Paths)
	}
	path := staged.Paths[0]
	if !strings.HasPrefix(path, ".ao/attachments/") || !strings.HasSuffix(path, ".png") {
		t.Errorf("staged path = %q, want a .png under .ao/attachments/", path)
	}

	var second struct {
		Paths []string `json:"paths"`
	}
	d.mustCall("POST", "/sessions/"+session+"/attachments", http.StatusCreated,
		map[string]any{"attachments": []map[string]any{{"mimeType": "image/png", "data": onePixelPNG}}},
		&second)
	if len(second.Paths) != 1 || second.Paths[0] == path {
		t.Errorf("second staging reused the path %q, overwriting the first image", path)
	}

	send(t, d, session,
		"Run `test -f "+path+" && echo FOUND || echo MISSING` and reply with its exact output.",
		"attach-read")
	snap := d.awaitConversation(session, 3*time.Minute, "the agent to look for the image",
		func(s snapshot) bool { return terminal(s.Turns[len(s.Turns)-1].State) })
	if !contains(snap.assistantText(), "FOUND") {
		t.Fatalf("the agent could not see the staged image at %s:\n%s", path, describe(snap))
	}

	var files struct {
		Files []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"files"`
	}
	d.mustCall("GET", "/sessions/"+session+"/workspace/files", http.StatusOK, nil, &files)
	for _, file := range files.Files {
		if strings.HasPrefix(file.Path, ".ao/attachments/") {
			t.Errorf("staged attachment %q shows up as a workspace change (%s)", file.Path, file.Status)
		}
	}
}

func TestChatAttachmentSurvivesDaemonRestartRecovery(t *testing.T) {
	requireE2E(t)
	dataDir := t.TempDir()
	d := startDaemon(t, dataDir)
	project := seedProject(t, d, "attachmentrestart")
	session := chatSession(t, d, project, "Reply with exactly: READY")

	const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg=="
	want, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatal(err)
	}
	var staged struct {
		Paths []string `json:"paths"`
	}
	d.mustCall("POST", "/sessions/"+session+"/attachments", http.StatusCreated,
		map[string]any{"attachments": []map[string]any{{"mimeType": "image/png", "data": onePixelPNG}}},
		&staged)
	if len(staged.Paths) != 1 {
		t.Fatalf("staged paths = %v, want one", staged.Paths)
	}
	attachmentPath := staged.Paths[0]
	if got := readAttachmentPreview(t, d, session, attachmentPath, "before-restart"); !bytes.Equal(got, want) {
		t.Fatalf("preview before restart = %x, want %x", got, want)
	}

	d.stop()
	restarted := startDaemon(t, dataDir)
	restarted.awaitLiveController(session, 90*time.Second)
	if got := readAttachmentPreview(t, restarted, session, attachmentPath, "after-restart"); !bytes.Equal(got, want) {
		t.Fatalf("preview after restart = %x, want %x", got, want)
	}

	send(t, restarted, session,
		"Run `wc -c < "+attachmentPath+"` and reply with exactly the number it prints.",
		"attachment-after-restart")
	snap := restarted.awaitConversation(session, 3*time.Minute, "the restored agent to read the attachment",
		func(s snapshot) bool { return terminal(s.Turns[len(s.Turns)-1].State) })
	if !contains(snap.assistantText(), strconv.Itoa(len(want))) {
		t.Fatalf("resumed agent could not read %s:\n%s", attachmentPath, describe(snap))
	}
}

func readAttachmentPreview(t *testing.T, d *daemon, session, attachmentPath, cacheBust string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		d.baseURL+"/sessions/"+session+"/preview/files/"+attachmentPath+"?cache-bust="+cacheBust, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("preview %s: %v", attachmentPath, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("preview %s = %d, %s\n%s", attachmentPath, res.StatusCode, body, d.tailLog())
	}
	return body
}
