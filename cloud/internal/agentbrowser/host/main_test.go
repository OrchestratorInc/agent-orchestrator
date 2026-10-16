package host

import (
	"encoding/json"
	"maps"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The command follows any global flags, and the values of those that take one.
func TestAgentCommand(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"snapshot", "-i"}, "snapshot"},
		{[]string{"--json", "snapshot", "-i"}, "snapshot"},
		{[]string{"--json", "dialog", "accept"}, "dialog"},
		{[]string{"--session", "a", "--json", "dialog", "dismiss"}, "dialog"},
		{[]string{"-p", "kernel", "--max-output", "500", "open", "example.com"}, "open"},
		{[]string{"--headed", "false", "--cdp=9222", "click", "@e1"}, "click"},
		{[]string{"--json"}, ""},
	} {
		if got := agentCommand(c.args); got != c.want {
			t.Errorf("agentCommand(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

// agentHost is a host whose agent-browser is a script: it prints its arguments
// and saves a screenshot where it is told to, as agent-browser does.
func agentHost(t *testing.T) *host {
	dir := t.TempDir()
	ab := filepath.Join(dir, "agent-browser")
	script := `#!/bin/sh
shift 5 # --cdp 9222 --session poc --no-auto-dialog
echo "args: $*"
case " $* " in *" screenshot "*) for a; do :; done; echo png > "$a"; echo "saved to $a" ;; esac
`
	if err := os.WriteFile(ab, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	old := agentDir
	agentDir = filepath.Join(dir, "work")
	t.Cleanup(func() { agentDir = old })
	return &host{abPath: ab, cdpPort: 9222, tabs: map[string]*tab{}}
}

type agentResp struct {
	Output, Error string
	Files         map[string][]byte
}

func runCommand(t *testing.T, h *host, args []string, files map[string][]byte) agentResp {
	return runRequest(t, h, map[string]any{"args": args, "files": files})
}

func runRequest(t *testing.T, h *host, request map[string]any) agentResp {
	args := request["args"]
	body, _ := json.Marshal(request)
	rec := httptest.NewRecorder()
	h.handleAgent(rec, httptest.NewRequest("POST", "/agent", strings.NewReader(string(body))))
	var r agentResp
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("%q: %v (%s)", args, err, rec.Body)
	}
	return r
}

func TestHandleAgent(t *testing.T) {
	h := agentHost(t) // one script: a Mac scans each new one for seconds on its first run
	// `--json snapshot` dates the agent's refs like `snapshot` does, and a
	// dialog gates only the tab the agent works in, never `dialog` itself.
	h.tabs["person"], h.tabs["agent"] = &tab{ID: "person"}, &tab{ID: "agent"}
	h.order = []string{"person", "agent"} // the agent's: the newest
	h.setDialog("person", "confirm: Leave?")
	if r := runCommand(t, h, []string{"--json", "snapshot", "-i"}, nil); r.Error != "" {
		t.Fatalf("a dialog in the person's tab refused the agent: %+v", r)
	}
	if h.ctl.snapshotAt().IsZero() {
		t.Error("`--json snapshot` did not date the refs")
	}
	h.setDialog("agent", "alert: Saved")
	if r := runCommand(t, h, []string{"--json", "dialog", "accept"}, nil); r.Error != "" {
		t.Errorf("`--json dialog accept` refused: %+v", r)
	}
	h.setDialog("agent", "")

	// Relative paths are to the agent's directory: an upload's files go up and
	// reach Chromium in full, a screenshot comes back by the name it was given,
	// folder and all.
	r := runCommand(t, h, []string{"upload", "#file", "docs/a.txt"}, map[string][]byte{"docs/a.txt": []byte("hi")})
	if b, err := os.ReadFile(filepath.Join(agentDir, "docs/a.txt")); err != nil || string(b) != "hi" {
		t.Errorf("uploaded file: %q, %v", b, err)
	}
	if !strings.Contains(r.Output, "args: upload #file "+filepath.Join(agentDir, "docs/a.txt")) {
		t.Errorf("upload path not made absolute: %q", r.Output)
	}
	if len(r.Files) != 0 {
		t.Errorf("the uploaded file came back: %q", slices.Collect(maps.Keys(r.Files)))
	}
	r = runCommand(t, h, []string{"screenshot", "./shots/a.png"}, nil)
	if keys := slices.Collect(maps.Keys(r.Files)); len(keys) != 1 || string(r.Files["./shots/a.png"]) != "png\n" {
		t.Errorf("files back: %q (output %q)", keys, r.Output)
	}

	// A file the agent sent that is already here, the same, is this machine's
	// own: it is left alone. Saved over by the command, it comes back.
	shot := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(shot, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(shot, old, old); err != nil {
		t.Fatal(err)
	}
	r = runCommand(t, h, []string{"fill", "@e1", shot}, map[string][]byte{shot: []byte("old")})
	if st, err := os.Stat(shot); err != nil || !st.ModTime().Equal(old) {
		t.Errorf("the agent's own file was rewritten: %v, %v", st.ModTime(), err)
	}
	r = runCommand(t, h, []string{"screenshot", shot}, map[string][]byte{shot: []byte("old")})
	if string(r.Files[shot]) != "png\n" {
		t.Errorf("a screenshot saved over a sent file did not come back: %q (output %q)", r.Files[shot], r.Output)
	}

	// An agent on this machine (dir) sends no files and gets none: an upload
	// reads its file where it is, and a screenshot at a relative path lands in
	// its directory.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = runRequest(t, h, map[string]any{"args": []string{"--session", "x", "upload", "#file", "notes.txt"}, "dir": dir})
	if !strings.Contains(r.Output, "upload #file "+filepath.Join(dir, "notes.txt")) || len(r.Files) != 0 {
		t.Errorf("local upload: output %q, files %q; want the agent's file in full and none back", r.Output, slices.Collect(maps.Keys(r.Files)))
	}
	r = runRequest(t, h, map[string]any{"args": []string{"screenshot", "shots/b.png"}, "dir": dir})
	if b, err := os.ReadFile(filepath.Join(dir, "shots", "b.png")); err != nil || string(b) != "png\n" || len(r.Files) != 0 {
		t.Errorf("local screenshot: %q, %v, files %d; want it in the agent's directory", b, err, len(r.Files))
	}
	if r = runRequest(t, h, map[string]any{"args": []string{"snapshot"}, "dir": "relative"}); r.Error == "" && r.Output != "" {
		t.Errorf("a relative dir was taken: %+v", r)
	}

	// The output names files saved where agent-browser chose; "./shot.png" is not "/shot.png".
	var found []string
	for _, m := range absPath.FindAllStringSubmatch(`saved to ./shot.png, "/tmp/a.png" and /b c/d`, -1) {
		found = append(found, m[1])
	}
	if want := []string{"/tmp/a.png", "/b"}; !slices.Equal(found, want) {
		t.Errorf("absolute paths in the output: %q, want %q", found, want)
	}
}

// A daemon left by an earlier run is stopped by the PID it recorded, and only
// if that PID is still an agent-browser: the agent's own daemons, and a
// process that reused the PID, live on.
func TestStopStaleAgentStopsOnlyItsOwnDaemon(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	dir := t.TempDir()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	data, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(dir, "agent-browser")
	if err := os.WriteFile(named, data, 0o755); err != nil {
		t.Fatal(err)
	}
	start := func(bin string) (*exec.Cmd, chan struct{}) {
		cmd := exec.Command(bin, "60")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		return cmd, exited
	}
	daemon, daemonExited := start(named)
	other, otherExited := start(sleep)
	old := agentSocketDir
	agentSocketDir = dir
	t.Cleanup(func() { agentSocketDir = old })
	record := func(pid int) {
		if err := os.WriteFile(filepath.Join(dir, agentSession+".pid"), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	record(other.Process.Pid)
	stopStaleAgent()
	record(daemon.Process.Pid)
	stopStaleAgent()
	select {
	case <-daemonExited:
	case <-time.After(5 * time.Second):
		t.Fatal("the recorded agent-browser daemon was not stopped")
	}
	select {
	case <-otherExited:
		t.Fatal("a process that is not agent-browser was stopped")
	case <-time.After(200 * time.Millisecond):
	}
}
