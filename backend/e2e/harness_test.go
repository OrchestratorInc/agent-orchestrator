//go:build !windows

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const gateEnv = "AO_CHAT_E2E"

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 to run the chat end-to-end suite (real daemon, real model calls)", gateEnv)
	}
	bin := codexBinary()
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("agent binary %q is not on PATH: %v", bin, err)
	}
}

func codexBinary() string {
	if bin := os.Getenv("AO_CODEX_BIN"); bin != "" {
		return bin
	}
	return "codex"
}

var (
	buildOnce sync.Once
	aoBinary  string
	buildErr  error
)

func buildDaemon(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ao-e2e-bin-")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "ao")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/ao")
		cmd.Dir = ".."
		if combined, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/ao: %w\n%s", err, combined)
			return
		}
		aoBinary = out
	})
	if buildErr != nil {
		t.Fatalf("build daemon: %v", buildErr)
	}
	return aoBinary
}

type daemon struct {
	t       *testing.T
	dataDir string
	port    int
	baseURL string
	logPath string

	cmd  *exec.Cmd
	pgid int
}

func startDaemon(t *testing.T, dataDir string) *daemon {
	t.Helper()
	bin := buildDaemon(t)
	port := freePort(t)

	logPath := filepath.Join(t.TempDir(), fmt.Sprintf("daemon-%d.log", port))
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create daemon log: %v", err)
	}

	cmd := exec.Command(bin, "daemon")
	cmd.Env = append(os.Environ(),
		"AO_DATA_DIR="+dataDir,
		"AO_RUN_FILE="+filepath.Join(dataDir, "running.json"),
		"AO_PORT="+strconv.Itoa(port),
		"AO_TELEMETRY_REMOTE=off",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	d := &daemon{
		t:       t,
		dataDir: dataDir,
		port:    port,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d/api/v1", port),
		logPath: logPath,
		cmd:     cmd,
		pgid:    cmd.Process.Pid,
	}
	t.Cleanup(d.kill)
	d.waitReady()
	t.Cleanup(d.killAllSessions)
	return d
}

func (d *daemon) killAllSessions() {
	var list struct {
		Sessions []struct {
			ID           string `json:"id"`
			IsTerminated bool   `json:"isTerminated"`
		} `json:"sessions"`
	}
	if status, err := d.call("GET", "/sessions", nil, &list); err != nil || status != http.StatusOK {
		return
	}
	for _, session := range list.Sessions {
		if session.IsTerminated {
			continue
		}
		_, _ = d.call("POST", "/sessions/"+session.ID+"/kill", nil, nil)
	}
}

func (d *daemon) waitReady() {
	d.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		var settings struct {
			DefaultSessionMode string   `json:"defaultSessionMode"`
			ChatHarnesses      []string `json:"chatHarnesses"`
		}
		if status, err := d.call("GET", "/settings", nil, &settings); err == nil && status == http.StatusOK {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	d.t.Fatalf("daemon never became ready on :%d\n%s", d.port, d.tailLog())
}

func (d *daemon) stop() {
	if d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-d.pgid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = d.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = syscall.Kill(-d.pgid, syscall.SIGKILL)
		<-done
	}
	d.cmd = nil
	d.waitPortFree()
}

func (d *daemon) kill() {
	if d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-d.pgid, syscall.SIGKILL)
	_, _ = d.cmd.Process.Wait()
	d.cmd = nil
	d.waitPortFree()
}

func (d *daemon) waitPortFree() {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", d.port), 200*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(200 * time.Millisecond)
	}
}

func (d *daemon) tailLog() string {
	data, err := os.ReadFile(d.logPath)
	if err != nil {
		return fmt.Sprintf("(no daemon log: %v)", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, "level=INFO msg=\"http request\"") && strings.Contains(line, "status=200") {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) > 60 {
		kept = kept[len(kept)-60:]
	}
	return "--- daemon log (non-200 requests, warnings, errors) ---\n" + strings.Join(kept, "\n")
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func (d *daemon) call(method, path string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, d.baseURL+path, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, err
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return res.StatusCode, fmt.Errorf("decode %s %s (%d): %w: %s",
				method, path, res.StatusCode, err, payload)
		}
	}
	return res.StatusCode, nil
}

func (d *daemon) mustCall(method, path string, want int, body, out any) {
	d.t.Helper()
	var raw json.RawMessage
	status, err := d.call(method, path, body, &raw)
	if err != nil {
		d.t.Fatalf("%s %s: %v", method, path, err)
	}
	if status != want {
		d.t.Fatalf("%s %s: status %d, want %d: %s\n%s", method, path, status, want, raw, d.tailLog())
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			d.t.Fatalf("%s %s: decode: %v: %s", method, path, err, raw)
		}
	}
}

type apiError struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (d *daemon) callExpectingError(method, path string, body any) (int, apiError) {
	d.t.Helper()
	var out apiError
	status, err := d.call(method, path, body, &out)
	if err != nil {
		d.t.Fatalf("%s %s: %v", method, path, err)
	}
	return status, out
}

func seedProject(t *testing.T, d *daemon, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), fmt.Sprintf("%s%d", name, uniqueSuffix()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=ao-e2e", "GIT_AUTHOR_EMAIL=e2e@example.com",
			"GIT_COMMITTER_NAME=ao-e2e", "GIT_COMMITTER_EMAIL=e2e@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# e2e fixture\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-m", "init")
	run("git", "remote", "add", "origin", dir)
	run("git", "fetch", "origin", "main")
	run("git", "remote", "set-head", "origin", "main")

	var out struct {
		Project struct{ ID string } `json:"project"`
	}
	d.mustCall("POST", "/projects", http.StatusCreated, map[string]any{"path": dir}, &out)
	if out.Project.ID == "" {
		d.mustCall("POST", "/projects", http.StatusOK, map[string]any{"path": dir}, &out)
	}
	if out.Project.ID == "" {
		t.Fatalf("project registration returned no id for %s", dir)
	}
	return out.Project.ID
}

func setPermissions(t *testing.T, d *daemon, projectID, mode string) {
	t.Helper()
	d.mustCall("PUT", "/projects/"+projectID+"/config", http.StatusOK, map[string]any{
		"config": map[string]any{
			"agentConfig": map[string]any{"permissions": mode},
		},
	}, nil)
}

type spawned struct {
	Session struct {
		ID      string `json:"id"`
		Mode    string `json:"mode"`
		Status  string `json:"status"`
		Harness string `json:"harness"`
	} `json:"session"`
}

func spawn(t *testing.T, d *daemon, req map[string]any) spawned {
	t.Helper()
	var out spawned
	d.mustCall("POST", "/sessions", http.StatusCreated, req, &out)
	if out.Session.ID == "" {
		t.Fatalf("spawn returned no session id: %+v", out)
	}
	return out
}

type turn struct {
	RolledBack     bool      `json:"rolledBack"`
	ID             string    `json:"id"`
	State          string    `json:"state"`
	ProviderTurnID string    `json:"providerTurnId"`
	ErrorMessage   string    `json:"errorMessage"`
	RequestedAt    string    `json:"requestedAt"`
	Diff           *turnDiff `json:"diff"`
	Plan           *turnPlan `json:"plan"`
}

type turnPlan struct {
	Explanation string     `json:"explanation"`
	Steps       []planStep `json:"steps"`
}

type planStep struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

type turnDiff struct {
	Files     []diffFile `json:"files"`
	Truncated bool       `json:"truncated"`
}

type diffFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
}

func (d turnDiff) fileByPath(path string) (diffFile, bool) {
	for _, file := range d.Files {
		if file.Path == path {
			return file, true
		}
	}
	return diffFile{}, false
}

type message struct {
	ID       string `json:"id"`
	TurnID   string `json:"turnId"`
	Sequence int64  `json:"sequence"`
	Revision int64  `json:"revision"`
	Role     string `json:"role"`
	Origin   string `json:"origin"`
	Text     string `json:"text"`
	Stream   bool   `json:"streaming"`
}

type activity struct {
	ID         string          `json:"id"`
	TurnID     string          `json:"turnId"`
	Sequence   int64           `json:"sequence"`
	Kind       string          `json:"activityKind"`
	Status     string          `json:"status"`
	Summary    string          `json:"summary"`
	RequestID  string          `json:"requestId"`
	DetailJSON json.RawMessage `json:"detail"`
}

type decisionOpt struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func (a activity) decisions() []decisionOpt {
	var detail struct {
		Decisions []decisionOpt `json:"decisions"`
	}
	if len(a.DetailJSON) == 0 {
		return nil
	}
	if err := json.Unmarshal(a.DetailJSON, &detail); err != nil {
		return nil
	}
	return detail.Decisions
}

type turnSettings struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	ApprovalMode    string `json:"approvalMode"`
}

type snapshot struct {
	ConversationID string             `json:"conversationId"`
	Settings       turnSettings       `json:"settings"`
	Mode           string             `json:"mode"`
	Controller     string             `json:"controller"`
	Title          string             `json:"title"`
	LatestSequence int64              `json:"latestSequence"`
	Turns          []turn             `json:"turns"`
	Messages       []message          `json:"messages"`
	Activities     []activity         `json:"activities"`
	Usage          *usageState        `json:"usage"`
	RateLimits     *rateLimitsState   `json:"rateLimits"`
	CompactedAt    *string            `json:"compactedAt"`
	ModelReroute   *modelRerouteState `json:"modelReroute"`
	Account        *accountState      `json:"account"`
	ThreadState    *threadState       `json:"threadState"`
	MCPServers     []mcpServerState   `json:"mcpServers"`
}

type modelRerouteState struct {
	FromModel      string `json:"fromModel"`
	ToModel        string `json:"toModel"`
	Reason         string `json:"reason"`
	ProviderTurnID string `json:"providerTurnId"`
	At             string `json:"at"`
}

type accountState struct {
	AuthMode         string  `json:"authMode"`
	PlanLabel        string  `json:"planLabel"`
	ReauthRequiredAt *string `json:"reauthRequiredAt"`
	ReauthReason     string  `json:"reauthReason"`
}

type threadState struct {
	Status     string   `json:"status"`
	WaitingOn  []string `json:"waitingOn"`
	ArchivedAt *string  `json:"archivedAt"`
	ClosedAt   *string  `json:"closedAt"`
}

type mcpServerState struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	FailureReason string `json:"failureReason"`
}

type usageState struct {
	ContextUsed   int64 `json:"contextUsed"`
	ContextWindow int64 `json:"contextWindow"`
	InputTokens   int64 `json:"inputTokens"`
	OutputTokens  int64 `json:"outputTokens"`
	CachedTokens  int64 `json:"cachedTokens"`
	TotalTokens   int64 `json:"totalTokens"`
}

type rateLimitsState struct {
	PrimaryUsedPercent       float64    `json:"primaryUsedPercent"`
	SecondaryUsedPercent     float64    `json:"secondaryUsedPercent"`
	PrimaryResetsInSeconds   int64      `json:"primaryResetsInSeconds"`
	SecondaryResetsInSeconds int64      `json:"secondaryResetsInSeconds"`
	PlanLabel                string     `json:"planLabel"`
	CompactedAt              string     `json:"compactedAt"`
	Mode                     string     `json:"mode"`
	Controller               string     `json:"controller"`
	LatestSequence           int64      `json:"latestSequence"`
	Turns                    []turn     `json:"turns"`
	Messages                 []message  `json:"messages"`
	Activities               []activity `json:"activities"`
}

func (s snapshot) turnByID(id string) (turn, bool) {
	for _, t := range s.Turns {
		if t.ID == id {
			return t, true
		}
	}
	return turn{}, false
}

func (s snapshot) userTurnStates() map[string]string {
	states := map[string]string{}
	for _, m := range s.Messages {
		if m.Role != "user" {
			continue
		}
		if t, ok := s.turnByID(m.TurnID); ok {
			states[m.Text] = t.State
		}
	}
	return states
}

func (s snapshot) assistantText() string {
	var b strings.Builder
	for _, m := range s.Messages {
		if m.Role == "assistant" {
			b.WriteString(m.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (s snapshot) pendingApproval() (activity, bool) {
	for _, a := range s.Activities {
		if a.Kind == "approval" && a.Status == "pending" {
			return a, true
		}
	}
	return activity{}, false
}

func (d *daemon) conversation(sessionID string) snapshot {
	d.t.Helper()
	var out snapshot
	d.mustCall("GET", "/sessions/"+sessionID+"/conversation", http.StatusOK, nil, &out)
	return out
}

func (d *daemon) awaitConversation(sessionID string, timeout time.Duration, what string, pred func(snapshot) bool) snapshot {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	var last snapshot
	for time.Now().Before(deadline) {
		last = d.conversation(sessionID)
		if pred(last) {
			return last
		}
		time.Sleep(300 * time.Millisecond)
	}
	d.t.Fatalf("timed out after %s waiting for %s\n%s\n%s",
		timeout, what, describe(last), d.tailLog())
	return last
}

func (d *daemon) awaitLiveController(sessionID string, timeout time.Duration) snapshot {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	var last snapshot
	for time.Now().Before(deadline) {
		var read struct {
			Session struct {
				Status       string `json:"status"`
				IsTerminated bool   `json:"isTerminated"`
			} `json:"session"`
		}
		if status, err := d.call("GET", "/sessions/"+sessionID, nil, &read); err == nil && status == http.StatusOK {
			if !read.Session.IsTerminated {
				var snap snapshot
				if code, err := d.call("GET", "/sessions/"+sessionID+"/conversation", nil, &snap); err == nil && code == http.StatusOK {
					last = snap
					if snap.Controller == "ready" || snap.Controller == "busy" {
						return snap
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	d.t.Fatalf("session %s never came back with a live controller within %s\n%s\n%s",
		sessionID, timeout, describe(last), d.tailLog())
	return last
}

func describe(s snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- conversation (controller=%s seq=%d) ---\n", s.Controller, s.LatestSequence)
	for _, t := range s.Turns {
		fmt.Fprintf(&b, "  turn %s state=%s provider=%s err=%q\n",
			short(t.ID), t.State, short(t.ProviderTurnID), t.ErrorMessage)
	}
	for _, m := range s.Messages {
		fmt.Fprintf(&b, "  msg  seq=%d %s/%s %q\n", m.Sequence, m.Role, m.Origin, trim(m.Text, 70))
	}
	for _, a := range s.Activities {
		fmt.Fprintf(&b, "  act  seq=%d %s/%s %q req=%s\n",
			a.Sequence, a.Kind, a.Status, trim(a.Summary, 60), a.RequestID)
	}
	return b.String()
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func trim(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > n {
		return text[:n] + "…"
	}
	return text
}

func terminal(state string) bool {
	switch state {
	case "completed", "interrupted", "failed":
		return true
	default:
		return false
	}
}

func uniqueSuffix() int64 {
	return suffixCounter.Add(1) + time.Now().Unix()%100000
}

var suffixCounter atomic.Int64

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release reserved port: %v", err)
	}
	return port
}

func harnessWithoutChatDriver(t *testing.T, d *daemon) string {
	t.Helper()
	var settings struct {
		ChatHarnesses []string `json:"chatHarnesses"`
	}
	d.mustCall("GET", "/settings", http.StatusOK, nil, &settings)

	supported := map[string]bool{}
	for _, harness := range settings.ChatHarnesses {
		supported[harness] = true
	}
	for _, candidate := range []string{"aider", "goose", "continue", "cline", "amp"} {
		if !supported[candidate] {
			return candidate
		}
	}
	t.Fatalf("every candidate harness now has a chat driver: %v", settings.ChatHarnesses)
	return ""
}
