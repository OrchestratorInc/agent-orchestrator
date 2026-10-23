package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecordClaudeConversation(t *testing.T) {
	previous := claudeProcessRunning
	claudeProcessRunning = func(string, string) bool { return true }
	defer func() { claudeProcessRunning = previous }()
	dataDir := t.TempDir()
	start := time.Unix(1_800_000_000, 0)
	if err := RecordClaudeConversation(dataDir, "100", "a", start); err != nil {
		t.Fatal(err)
	}
	// Later hooks in the same conversation keep when it began.
	if err := RecordClaudeConversation(dataDir, "100", "a", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	want := ClaudeConversation{First: "a", Current: "a", Since: start.UnixNano(), Started: start.UnixNano()}
	if got := ReadClaudeConversations(dataDir)["100"]; got != want {
		t.Fatalf("record = %+v, want %+v", got, want)
	}
	// /clear: a new conversation in the same process.
	cleared := start.Add(2 * time.Minute)
	if err := RecordClaudeConversation(dataDir, "100", "b", cleared); err != nil {
		t.Fatal(err)
	}
	want = ClaudeConversation{First: "a", Current: "b", Since: cleared.UnixNano(), Started: start.UnixNano()}
	if got := ReadClaudeConversations(dataDir)["100"]; got != want {
		t.Fatalf("record = %+v, want %+v", got, want)
	}
	if err := RecordClaudeConversation(dataDir, "200", "c", cleared); err != nil {
		t.Fatal(err)
	}
	if got := len(ReadClaudeConversations(dataDir)); got != 2 {
		t.Fatalf("records = %d, want one per process", got)
	}
	for _, bad := range []struct{ pid, session string }{
		{"", "a"}, {"abc", "a"}, {"0", "a"}, {"../1", "a"}, {"100", ""}, {"100", "../x"},
	} {
		if err := RecordClaudeConversation(dataDir, bad.pid, bad.session, start); err == nil {
			t.Errorf("RecordClaudeConversation(%q, %q) accepted", bad.pid, bad.session)
		}
	}
}

// Records of exited Claude processes go when a new process records its own;
// past the limit the oldest go, never the terminal's.
func TestPruneClaudeConversations(t *testing.T) {
	running := map[string]bool{}
	previous := claudeProcessRunning
	claudeProcessRunning = func(pid, _ string) bool { return running[pid] }
	defer func() { claudeProcessRunning = previous }()
	dataDir := t.TempDir()
	start := time.Unix(1_800_000_000, 0)
	for i := 1; i <= 4; i++ {
		pid := strconv.Itoa(i)
		running[pid] = true
		if err := RecordClaudeConversation(dataDir, pid, "c"+pid, start.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	running["1"], running["3"] = false, false
	// A conversation change in a known process prunes nothing.
	if err := RecordClaudeConversation(dataDir, "4", "c4b", start.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := keys(ReadClaudeConversations(dataDir)); got != "1,2,3,4" {
		t.Fatalf("records = %s, want no prune on a conversation change", got)
	}
	// A new process prunes the exited ones.
	if err := RecordClaudeConversation(dataDir, "5", "c5", start.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := keys(ReadClaudeConversations(dataDir)); got != "2,4,5" {
		t.Fatalf("records = %s, want the exited 1 and 3 pruned", got)
	}

	// Over the limit, the oldest go but the terminal's (2, the oldest) stays.
	for i := 6; i <= 9; i++ {
		pid := strconv.Itoa(i)
		running[pid] = true
		if err := RecordClaudeConversation(dataDir, pid, "c"+pid, start.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	PruneClaudeConversations(dataDir, "2", 4)
	if got := keys(ReadClaudeConversations(dataDir)); got != "2,7,8,9" {
		t.Fatalf("records = %s, want the terminal's and the 3 newest", got)
	}
	running["2"] = false
	PruneClaudeConversations(dataDir, "2", 4)
	if _, ok := ReadClaudeConversations(dataDir)["2"]; !ok {
		t.Fatal("the terminal's record was pruned")
	}
}

// A pid reused by a later process starts a new record.
func TestRecordClaudeConversationTellsAReusedPid(t *testing.T) {
	if processStart(strconv.Itoa(os.Getpid())) == "" {
		t.Skip("no /proc process start times here")
	}
	dataDir := t.TempDir()
	pid := strconv.Itoa(os.Getpid())
	if err := os.MkdirAll(filepath.Join(dataDir, claudeConversationDir), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := `{"first":"old","current":"old","since":1,"started":1,"procStart":"1"}`
	if err := os.WriteFile(filepath.Join(dataDir, claudeConversationDir, pid+".json"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordClaudeConversation(dataDir, pid, "new", time.Now()); err != nil {
		t.Fatal(err)
	}
	if record := ReadClaudeConversations(dataDir)[pid]; record.First != "new" {
		t.Fatalf("record = %+v, want a new process's record", record)
	}
}

func TestProcessRunning(t *testing.T) {
	self := strconv.Itoa(os.Getpid())
	if !processRunning(self, processStart(self)) {
		t.Fatal("this process reads as exited")
	}
	command := exec.Command("true")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	if processRunning(strconv.Itoa(command.Process.Pid), "") {
		t.Fatal("an exited process reads as running")
	}
}

func keys(records map[string]ClaudeConversation) string {
	pids := make([]string, 0, len(records))
	for pid := range records {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	return strings.Join(pids, ",")
}

func TestStatProcessStart(t *testing.T) {
	stat := "4242 (claude (2.1.302)) S 1 4242 4242 0 -1 4194560 1 0 0 0 0 0 0 0 20 0 11 0 987654 1000 100\n"
	if got := statProcessStart([]byte(stat)); got != "987654" {
		t.Fatalf("start = %q, want 987654", got)
	}
	if got := statProcessStart([]byte("4242 (claude) S 1")); got != "" {
		t.Fatalf("start of a short stat = %q", got)
	}
}
