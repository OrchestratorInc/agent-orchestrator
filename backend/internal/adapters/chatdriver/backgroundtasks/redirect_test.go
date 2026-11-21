package backgroundtasks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStdoutFileFollowsWhereACommandSendsItsOutput(t *testing.T) {
	// Host-absolute fixtures: on Windows a rooted path needs a drive, and the
	// command line writes it with forward slashes (see stdoutFile).
	volume := ""
	if runtime.GOOS == "windows" {
		volume = "C:"
	}
	dir, home := filepath.FromSlash(volume+"/work"), filepath.FromSlash(volume+"/home/me")
	for _, tc := range []struct {
		command string
		want    string
	}{
		{`npm run dev > {V}/tmp/dev.log 2>&1`, "/tmp/dev.log"},
		{`PORT=3000 npm run dev >{V}/tmp/dev.log 2>&1`, "/tmp/dev.log"},
		{`npm run dev >> logs/dev.log 2>&1`, "/work/logs/dev.log"},
		{`nohup ./server 1>server.log 2>&1 &`, "/work/server.log"},
		{`./server &> out.log`, "/work/out.log"},
		{`./server &>> out.log`, "/work/out.log"},
		{`./server >| out.log`, "/work/out.log"},
		{`./server >& out.log`, "/work/out.log"},
		{`node server.js > "logs/my app.log" 2>&1`, "/work/logs/my app.log"},
		{`node server.js > ~/dev.log`, "/home/me/dev.log"},
		{`cd frontend && npm run dev > dev.log 2>&1`, "/work/frontend/dev.log"},
		{`cd {V}/srv/app; ./run > out.log`, "/srv/app/out.log"},
		{`cd frontend && cd .. && ./run > out.log`, "/work/out.log"},
		{`(cd frontend && npm run build) > build.log`, "/work/build.log"},
		// Across commands, the one run with & decides, else the first that redirects.
		{`npm run dev > dev.log 2>&1 & echo $! > dev.pid`, "/work/dev.log"},
		{`npm run dev > logs/dev.log 2>&1; echo ok > status.txt`, "/work/logs/dev.log"},
		{`echo starting > status.txt; npm run dev > dev.log 2>&1 &`, "/work/dev.log"},
		{`(cd app && npm run dev > ../dev.log 2>&1) & echo $! > dev.pid`, "/work/dev.log"},
		{`(cd app && npm run dev) > dev.log 2>&1 & echo $! > dev.pid`, "/work/dev.log"},
		{`npm install && npm run dev > dev.log 2>&1`, "/work/dev.log"},
		{`npm run dev 2>&1 | grep -v noise > dev.log`, "/work/dev.log"},
		{`./a > a.log; ./b > b.log`, "/work/a.log"},
		{`./a > a.log > b.log`, "/work/b.log"},
		{`2>&1 ./server > out.log`, "/work/out.log"},
		{"./server \\\n  > out.log", "/work/out.log"},
		{`./server > out.log # keep it running`, "/work/out.log"},
		{`./server > "$(date).log"`, ""},
		{`./server > $LOG`, ""},
		{`./server > "$LOG"`, ""},
		{`./server > logs/*.log`, ""},
		{`./server > ~other/out.log`, ""},
		{`./server > out.log; ./other > $LOG`, "/work/out.log"},
		{`./server > $LOG; ./other > out.log`, ""},
		{`npm run dev > /dev/null 2>&1 & echo $! > dev.pid`, ""},
		{`npm test 2> err.log`, ""},
		{`npm run dev 2>&1 | tee dev.log`, ""},
		{`npm run dev > /dev/null 2>&1`, ""},
		{`npm run dev >&2`, ""},
		{`echo "npm run dev > dev.log"`, ""},
		{`echo 'a > b.log'`, ""},
		{`echo a\>b.log`, ""},
		{`cat <<EOF > out.log`, ""},
		{`cd - && ./run > out.log`, ""},
		{`cd "$APP" && ./run > out.log`, ""},
		{`cd $APP && ./run > {V}/abs/out.log`, "/abs/out.log"},
		{`./server > 'unterminated.log`, ""},
		{`npm run dev`, ""},
	} {
		command := strings.ReplaceAll(tc.command, "{V}", volume)
		want := ""
		if tc.want != "" {
			want = filepath.FromSlash(volume + tc.want)
		}
		// Read as a POSIX shell on every host; cmd.exe lines are TestStdoutFileReadsACmdLine.
		got, ok := stdoutFileAs(command, dir, home, false)
		if got != want || ok != (want != "") {
			t.Errorf("stdoutFileAs(%q) = %q, %v; want %q", command, got, ok, want)
		}
	}
	if got, ok := stdoutFileAs(`./run > out.log`, "", home, false); ok {
		t.Errorf("relative path without a directory = %q", got)
	}
}

func TestRedirectedOutputReadsTheLogWrittenSinceTheTaskStarted(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "dev.log")
	if err := os.WriteFile(logPath, []byte("\x1b[32mready\x1b[0m on :3000\nGET / 200\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute)
	got, ok := RedirectedOutput("npm run dev > dev.log 2>&1", dir, &started)
	if !ok || got.Text != "ready on :3000\nGET / 200" || got.Truncated {
		t.Fatalf("output = %+v, %v", got, ok)
	}

	// A log last written before the task started belongs to an earlier run.
	old := started.Add(-time.Hour)
	if err := os.Chtimes(logPath, old, old); err != nil {
		t.Fatal(err)
	}
	if got, ok := RedirectedOutput("npm run dev > dev.log 2>&1", dir, &started); ok {
		t.Fatalf("stale log read: %+v", got)
	}
	if got, ok := RedirectedOutput("npm run dev > dev.log 2>&1", dir, nil); ok {
		t.Fatalf("log read without a start time: %+v", got)
	}
}

func TestRedirectedOutputRefusesWhatIsNotARegularLog(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-time.Minute)
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "link.log")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"./run > link.log", "./run > logs", "./run > missing.log", "./run"} {
		if got, ok := RedirectedOutput(command, dir, &started); ok {
			t.Errorf("%q read %+v", command, got)
		}
	}
}

func TestRedirectedOutputReadsOnlyInsideTheWorkspace(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	started := time.Now().Add(-time.Minute)
	// A file outside the workspace, written just now by something else.
	unrelated := filepath.Join(outside, "notes.txt")
	if err := os.WriteFile(unrelated, []byte("not the task's output"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := "./run > " + filepath.ToSlash(unrelated)
	if got, ok := RedirectedOutput(command, dir, &started); ok {
		t.Errorf("%q read a recent file outside the workspace: %+v", command, got)
	}
	// A symlinked parent that leaves the workspace.
	if err := os.Symlink(outside, filepath.Join(dir, "logs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, ok := RedirectedOutput("./run > logs/notes.txt", dir, &started); ok {
		t.Errorf("read through a symlinked parent: %+v", got)
	}
	if got, ok := RedirectedOutput("cd logs && ./run > notes.txt", dir, &started); ok {
		t.Errorf("read after cd into a symlinked parent: %+v", got)
	}
}

func TestRedirectedOutputKeepsTheTailOfALargeLog(t *testing.T) {
	dir := t.TempDir()
	var log strings.Builder
	for log.Len() < 2*MaxOutputBytes {
		log.WriteString("a fairly long log line that repeats\n")
	}
	log.WriteString("the newest line\n")
	if err := os.WriteFile(filepath.Join(dir, "big.log"), []byte(log.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute)
	got, ok := RedirectedOutput("./run > big.log", dir, &started)
	if !ok || !got.Truncated || !strings.HasSuffix(got.Text, "the newest line") ||
		!strings.HasPrefix(got.Text, "a fairly long log line") || len(got.Text) > MaxOutputBytes {
		t.Fatalf("truncated=%v len=%d ok=%v", got.Truncated, len(got.Text), ok)
	}
}

func TestRedirectedOutputSaysWhenTheLogIsOutsideTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-time.Minute)
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), "dev.log"))
	if got, ok := RedirectedOutput("npm run dev > "+outside+" 2>&1", dir, &started); ok || !got.LogOutsideWorkspace {
		t.Fatalf("outside log = %+v, %v", got, ok)
	}
	// A log inside the workspace that isn't there yet is simply not shown.
	if got, ok := RedirectedOutput("npm run dev > dev.log 2>&1", dir, &started); ok || got.LogOutsideWorkspace {
		t.Fatalf("missing inside log = %+v, %v", got, ok)
	}
}

func TestStdoutFileOnWindowsKeepsRootedAndBackslashPathsOutOfTheWorkspace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path rules")
	}
	dir := `C:\work`
	// Git Bash's /tmp is not under the workspace.
	if got, ok := stdoutFile(`npm run dev > /tmp/dev.log 2>&1`, dir, ""); !ok || got != `\tmp\dev.log` {
		t.Errorf("rooted path = %q, %v", got, ok)
	}
	started := time.Now().Add(-time.Minute)
	if got, ok := RedirectedOutput(`npm run dev > /tmp/dev.log 2>&1`, t.TempDir(), &started); ok || !got.LogOutsideWorkspace {
		t.Errorf("rooted log = %+v, %v", got, ok)
	}
	// A cmd.exe line: a backslash is a separator, not the escape that would make logsdev.log.
	if got, ok := stdoutFile(`npm run dev > logs\dev.log 2>&1`, dir, ""); !ok || got != `C:\work\logs\dev.log` {
		t.Errorf("backslash path = %q, %v", got, ok)
	}
	if got, ok := stdoutFile(`cd app & npm run dev > C:\logs\dev.log`, dir, ""); !ok || got != `C:\logs\dev.log` {
		t.Errorf("drive path = %q, %v", got, ok)
	}
	// A POSIX line still reads a backslash as an escape, so it is not followed.
	if got, ok := stdoutFile(`npm run dev > logs\dev.log 2>&1 & echo $! > dev.pid`, dir, ""); ok {
		t.Errorf("escaped path in a POSIX line = %q", got)
	}
}

func TestStdoutFileReadsACmdLine(t *testing.T) {
	volume := ""
	if runtime.GOOS == "windows" {
		volume = "C:"
	}
	dir := filepath.FromSlash(volume + "/work")
	for _, tc := range []struct {
		command string
		want    string
	}{
		// `&` means "then", so the last command that redirects decides.
		{`echo start > status.txt & npm run dev > dev.log 2>&1`, "/work/dev.log"},
		{`npm run dev > dev.log 2>&1 & echo done`, "/work/dev.log"},
		{`cd app & npm run dev > dev.log`, "/work/app/dev.log"},
		{`cd app && npm run dev > logs/dev.log 2>&1`, "/work/app/logs/dev.log"},
		{`npm run dev > %TEMP%/dev.log`, ""},
		{`npm run dev > NUL 2>&1`, ""},
		{`npm run dev > nul`, ""},
		{`echo it's > out.log`, "/work/out.log"},
	} {
		want := ""
		if tc.want != "" {
			want = filepath.FromSlash(volume + tc.want)
		}
		got, ok := stdoutFileAs(tc.command, dir, "", true)
		if got != want || ok != (want != "") {
			t.Errorf("stdoutFileAs(%q, cmd) = %q, %v; want %q", tc.command, got, ok, want)
		}
	}
	for command, cmd := range map[string]bool{
		`npm run dev > dev.log & echo $! > dev.pid`: false,
		"echo `date` > out.log":                     false,
		`nohup ./server > out.log &`:                false,
		`npm run dev > logs\dev.log 2>&1`:           true,
	} {
		if cmdLine(command) != cmd {
			t.Errorf("cmdLine(%q) = %v, want %v", command, !cmd, cmd)
		}
	}
}
