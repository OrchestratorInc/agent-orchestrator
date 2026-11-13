package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const storageReportJSON = `{
	"scanning": %SCANNING%,
	"cleaning": %CLEANING%,
	"scanDurationMs": 1200,
	"totalBytes": 3221225472,
	"reclaimableBytes": 2147483648,
	"autoReclaimableBytes": 1073741824,
	"disk": {"path": "/data", "totalBytes": 107374182400, "freeBytes": 53687091200, "level": "normal"},
	"categories": [
		{"category": "worktrees", "bytes": 2147483648, "reclaimableBytes": 2147483648},
		{"category": "database", "bytes": 1073741824, "reclaimableBytes": 0},
		{"category": "logs", "bytes": 0, "reclaimableBytes": 0}
	],
	"worktrees": [{"path": "/data/worktrees/demo/demo-1", "sessionId": "demo-1", "state": "quiet", "bytes": 1, "offloaded": true, "offloadedBytes": 1048576}],
	"plan": [],
	"settings": {"autoClean": true, "offloadEnabled": true, "offloadIdleHours": 48, "offloadNoPrGraceHours": 2, "offloadMergedIdleMinutes": 30, "endedWorktreeDays": 7, "backupDays": 14, "logDays": 14, "cleanDevProfiles": false, "devProfileDays": 7, "templateDays": 30},
	"autoCleanAllowed": true,
	"copyOnWrite": false,
	%LAST%
	"database": {"fileBytes": 1073741824, "walBytes": 0, "freeBytes": 0, "tables": [], "tablesKnown": false}
}`

func storageReport(scanning, cleaning bool, last string) string {
	body := strings.ReplaceAll(storageReportJSON, "%SCANNING%", boolJSON(scanning))
	body = strings.ReplaceAll(body, "%CLEANING%", boolJSON(cleaning))
	return strings.ReplaceAll(body, "%LAST%", last)
}

func boolJSON(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func storageTestDeps() Deps {
	return Deps{ProcessAlive: func(int) bool { return true }, Sleep: func(time.Duration) {}}
}

func TestStorageReportWaitsForScanAndPrintsCategories(t *testing.T) {
	cfg := setConfigEnv(t)
	var gets int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/storage":
			gets++
			_, _ = io.WriteString(w, storageReport(gets == 1, false, ""))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, storageTestDeps(), "storage")
	if err != nil {
		t.Fatalf("storage: %v stderr=%s", err, stderr)
	}
	if gets != 2 || !strings.Contains(stderr, "Measuring disk usage") {
		t.Fatalf("gets=%d stderr=%q, want a wait for the scan", gets, stderr)
	}
	for _, want := range []string{"Session worktrees", "2.0 GiB", "Database", "Total", "3.0 GiB", "Offloaded sessions: 1", "Auto-clean: on", "ao storage clean --dry-run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Logs") {
		t.Fatalf("empty categories should be left out:\n%s", out)
	}
}

func TestStorageCleanDryRunPostsKindsAndChangesNothing(t *testing.T) {
	cfg := setConfigEnv(t)
	var mu sync.Mutex
	var bodies []storageCleanRequestTO
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/storage/clean":
			var body storageCleanRequestTO
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			mu.Lock()
			bodies = append(bodies, body)
			mu.Unlock()
			_, _ = io.WriteString(w, `{"startedAt":"2026-11-13T00:00:00Z","running":false,"dryRun":true,"freedBytes":1073741824,
				"actions":[{"at":"2026-11-13T00:00:00Z","kind":"offload","target":"/w/demo-1","sessionId":"demo-1","reason":"quiet 3d","bytes":1073741824,"dryRun":true,"result":"planned"}]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, storageTestDeps(), "storage", "clean", "--dry-run", "--only", "offload", "--session", "demo-1")
	if err != nil {
		t.Fatalf("clean: %v stderr=%s", err, stderr)
	}
	if len(bodies) != 1 || !bodies[0].DryRun || len(bodies[0].Kinds) != 1 || bodies[0].Kinds[0] != "offload" || len(bodies[0].SessionIDs) != 1 || bodies[0].SessionIDs[0] != "demo-1" {
		t.Fatalf("bodies=%+v, want one dry run for offload of demo-1", bodies)
	}
	if !strings.Contains(out, "Offload dependencies of quiet sessions: 1 item") || !strings.Contains(out, "dry-run: nothing was changed") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestStorageCleanConfirmedRunsAndReportsResult(t *testing.T) {
	cfg := setConfigEnv(t)
	var posts, gets int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/storage/clean":
			posts++
			var body storageCleanRequestTO
			_ = json.NewDecoder(r.Body).Decode(&body)
			if posts == 2 && body.DryRun {
				t.Errorf("second post should run the cleanup")
			}
			_, _ = io.WriteString(w, `{"startedAt":"2026-11-13T00:00:00Z","running":false,"dryRun":true,"freedBytes":10,
				"actions":[{"at":"2026-11-13T00:00:00Z","kind":"db_backups","target":"/d/ao.db.backup-1","reason":"old","bytes":10,"dryRun":true,"result":"planned"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/storage":
			gets++
			last := `"lastClean": {"startedAt":"2026-11-13T00:00:00Z","running":false,"dryRun":false,"freedBytes":10,
				"actions":[{"at":"2026-11-13T00:00:00Z","kind":"db_backups","target":"/d/ao.db.backup-1","reason":"old","bytes":10,"dryRun":false,"result":"done"},
				{"at":"2026-11-13T00:00:00Z","kind":"ended_worktrees","target":"/w/demo-2","sessionId":"demo-2","reason":"ended","bytes":0,"dryRun":false,"result":"skipped","error":"it has uncommitted changes"}]},`
			_, _ = io.WriteString(w, storageReport(false, gets == 1, last))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, storageTestDeps(), "storage", "clean", "--yes")
	if err != nil {
		t.Fatalf("clean: %v stderr=%s", err, stderr)
	}
	if posts != 2 || gets != 2 {
		t.Fatalf("posts=%d gets=%d, want preview+run and a poll until done", posts, gets)
	}
	if !strings.Contains(out, "Skipped: demo-2 (it has uncommitted changes)") || !strings.Contains(out, "1 done, 1 skipped, 0 failed") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestStorageCleanWithoutConfirmationAborts(t *testing.T) {
	cfg := setConfigEnv(t)
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/storage/clean":
			posts++
			_, _ = io.WriteString(w, `{"startedAt":"2026-11-13T00:00:00Z","running":false,"dryRun":true,"freedBytes":10,
				"actions":[{"at":"2026-11-13T00:00:00Z","kind":"logs","target":"/s/logs/a.jsonl","reason":"old","bytes":10,"dryRun":true,"result":"planned"}]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	deps := storageTestDeps()
	deps.In = strings.NewReader("no\n")
	out, _, err := executeCLI(t, deps, "storage", "clean")
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if posts != 1 || !strings.Contains(out, "aborted") {
		t.Fatalf("posts=%d output=%q, want only the preview", posts, out)
	}
}

func TestStorageCleanWaitsForFirstScan(t *testing.T) {
	cfg := setConfigEnv(t)
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/storage/clean":
			posts++
			if posts == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":"conflict","code":"STORAGE_SCAN_REQUIRED","message":"not measured yet"}`)
				return
			}
			_, _ = io.WriteString(w, `{"startedAt":"2026-11-13T00:00:00Z","running":false,"dryRun":true,"freedBytes":0,"actions":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/storage":
			_, _ = io.WriteString(w, storageReport(false, false, ""))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, storageTestDeps(), "storage", "clean", "--dry-run")
	if err != nil {
		t.Fatalf("clean: %v stderr=%s", err, stderr)
	}
	if posts != 2 || !strings.Contains(out, "Nothing to clean up.") {
		t.Fatalf("posts=%d output=%q", posts, out)
	}
}

func TestStorageCleanRejectsUnknownKind(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, storageTestDeps(), "storage", "clean", "--only", "everything")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "unknown cleanup kind") {
		t.Fatalf("err=%v exit=%d, want a usage error", err, ExitCode(err))
	}
}

func TestStorageSettingsPatchesOnlyChangedFlags(t *testing.T) {
	cfg := setConfigEnv(t)
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/storage/settings":
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			_, _ = io.WriteString(w, `{"autoClean":true,"offloadEnabled":false,"offloadIdleHours":48,"offloadNoPrGraceHours":2,"offloadMergedIdleMinutes":30,"endedWorktreeDays":0,"backupDays":14,"logDays":14,"cleanDevProfiles":false,"devProfileDays":7,"templateDays":30}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, storageTestDeps(), "storage", "settings", "--offload", "off", "--ended-worktree-days", "0")
	if err != nil {
		t.Fatalf("settings: %v stderr=%s", err, stderr)
	}
	if len(body) != 2 || body["offloadEnabled"] != false || body["endedWorktreeDays"] != float64(0) {
		t.Fatalf("patch body=%v, want only the two changed settings", body)
	}
	if !strings.Contains(out, "offload:                off") || !strings.Contains(out, "ended-worktree-days:    0") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestStorageSettingsRejectsBadSwitch(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, storageTestDeps(), "storage", "settings", "--auto-clean", "maybe")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want a usage error", err, ExitCode(err))
	}
}

func TestStorageLogPrintsEntriesAndKeepsErrorEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/storage/actions":
			if r.URL.Query().Get("limit") != "5" {
				t.Errorf("limit=%q", r.URL.Query().Get("limit"))
			}
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":"internal","code":"STORAGE_BROKEN","message":"boom","requestId":"req-9"}`)
				return
			}
			_, _ = io.WriteString(w, `{"actions":[{"id":1,"at":"2026-11-13T00:00:00Z","kind":"offload","target":"/w/demo-1","sessionId":"demo-1","reason":"quiet","bytes":2048,"dryRun":false,"result":"done"}]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, storageTestDeps(), "storage", "log", "--limit", "5")
	if err != nil {
		t.Fatalf("log: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(out, "offload") || !strings.Contains(out, "demo-1") || !strings.Contains(out, "2.0 KiB") {
		t.Fatalf("output:\n%s", out)
	}
	fail = true
	_, _, err = executeCLI(t, storageTestDeps(), "storage", "log", "--limit", "5")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "STORAGE_BROKEN") || !strings.Contains(err.Error(), "req-9") {
		t.Fatalf("err=%v, want the daemon error envelope", err)
	}
}
