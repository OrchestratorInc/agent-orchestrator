package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const triageMaxBytes = 64 << 10

func (c *commandContext) triageSnapshot(ctx context.Context, state, data, run string) (string, string, triageGitHub) {
	ctx, cancel := context.WithTimeout(ctx, triageBudget)
	defer cancel()
	githubCh := make(chan triageGitHub, 1)
	go func() { githubCh <- c.triagePreflight(ctx) }()
	var b, evidence, logs strings.Builder
	fmt.Fprintf(&evidence, "- App: %s", Version)
	fmt.Fprintf(&b, "# Orchestrator diagnostics\n\nApp: %s\n", Version)
	if Commit != "" {
		fmt.Fprintf(&evidence, ", commit %s", Commit)
		fmt.Fprintf(&b, "Commit: %s\n", Commit)
	}
	if Date != "" {
		fmt.Fprintf(&b, "Build date: %s\n", Date)
	}
	fmt.Fprintf(&evidence, ", OS: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "OS: %s\nArch: %s\nGo: %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	exe, err := c.deps.Executable()
	if err != nil {
		exe = "unavailable: " + err.Error()
	}
	fmt.Fprintf(&b, "Executable: %s\nState dir: %s\nData dir: %s\nRun file: %s\n", exe, state, data, run)
	st, err := c.inspectDaemon(ctx)
	if err != nil {
		fmt.Fprintf(&b, "Daemon: unavailable: %s\n", triageErrorReason(err))
		st.State = "unavailable"
	} else {
		fmt.Fprintf(&b, "Daemon: %s\nPID: %d\nPort: %d\n", st.State, st.PID, st.Port)
		if st.ExecutablePath != "" {
			fmt.Fprintf(&b, "Daemon executable: %s\n", st.ExecutablePath)
		}
	}
	fmt.Fprintf(&evidence, "- Daemon: %s", st.State)
	for _, field := range []struct{ name, value string }{{"Health", st.Health}, {"Ready", st.Ready}} {
		if field.value == "" {
			continue
		}
		value := field.value
		if strings.IndexFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) >= 0 {
			value = fmt.Sprintf("%q", value)
		}
		fmt.Fprintf(&b, "%s: %s\n", field.name, value)
		fmt.Fprintf(&evidence, ", %s: %s", strings.ToLower(field.name), value)
	}
	evidence.WriteByte('\n')
	if st.Error != "" {
		fmt.Fprintf(&b, "Daemon error: %s\n", st.Error)
	}
	if err != nil {
		fmt.Fprintf(&evidence, "- Daemon check unavailable: %s\n", triageErrorReason(err))
	}
	home, _ := os.UserHomeDir()
	logLines := 0
	for _, path := range []string{filepath.Join(data, "daemon.log"), filepath.Join(data, "desktop.log"), filepath.Join(data, "hooks.log")} {
		fmt.Fprintf(&b, "\n## Log: %s\n", path)
		tail, err := triageLogTail(ctx, path)
		if err != nil {
			fmt.Fprintf(&b, "unavailable: %s\n", triageErrorReason(err))
		} else {
			lines := 0
			if tail != "" {
				lines = strings.Count(tail, "\n") + 1
			}
			logLines += lines
			messages := triageLogMessages(tail)
			if len(messages) == 0 {
				fmt.Fprintf(&b, "- Logs: no warnings or errors in the last %d lines\n", lines)
			}
			for _, message := range messages {
				fmt.Fprintf(&b, "- Log excerpt: %s\n", message)
			}
			logs.WriteString(redactTriage(tail, home) + "\n")
		}
	}
	messages := triageLogMessages(logs.String())
	if len(messages) == 0 {
		fmt.Fprintf(&evidence, "- Logs: no warnings or errors in the last %d lines\n", logLines)
	}
	for _, message := range messages {
		fmt.Fprintf(&evidence, "- Log excerpt: %.240s\n", message)
	}
	ghPath, _ := c.deps.LookPath("gh")
	queries := triageKeywords(logs.String())
	duplicatesCh := make(chan string, 1)
	go func() { duplicatesCh <- triageDuplicates(ctx, ghPath, queries) }()
	versions := make([]struct{ details, evidence strings.Builder }, 2)
	var wg sync.WaitGroup
	for i, name := range []string{"claude", "codex"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, evidence := &versions[i].details, &versions[i].evidence
			path, err := c.deps.LookPath(name)
			if err != nil {
				fmt.Fprintf(b, "\n%s: unavailable: %s\n", name, triageErrorReason(err))
				fmt.Fprintf(evidence, "- %s: unavailable\n", name)
				return
			}
			fmt.Fprintf(b, "\n%s: %s\n", name, path)
			probe, cancel := context.WithTimeout(ctx, 6*time.Second)
			output := &triageOutput{remaining: 1024}
			cmd := aoprocess.CommandContext(probe, path, "--version")
			configureCLIBatch(cmd)
			cmd.Stdout, cmd.Stderr = output, output
			cmd.WaitDelay = 100 * time.Millisecond
			err = cmd.Run()
			if probe.Err() != nil {
				err = probe.Err()
			}
			cancel()
			if err != nil {
				fmt.Fprintf(b, "unavailable: %s\n", triageErrorReason(err))
				fmt.Fprintf(evidence, "- %s version: unavailable: %s\n", name, triageErrorReason(err))
			} else {
				fmt.Fprintln(b, output.String())
				fmt.Fprintf(evidence, "- %s version: %s\n", name, strings.Join(strings.Fields(output.String()), " "))
			}
		}()
	}
	wg.Wait()
	for i := range versions {
		b.WriteString(versions[i].details.String())
		evidence.WriteString(versions[i].evidence.String())
	}
	b.WriteString("\n## Recent sessions\n")
	if st.State != stateReady {
		b.WriteString("unavailable: daemon not ready\n")
	} else if err := c.triageSessions(ctx, st.Port, &b); err != nil {
		fmt.Fprintf(&b, "unavailable: %s\n", triageErrorReason(err))
	}
	github := <-githubCh
	// Unconfirmed candidates stay in context.md for the agent; posted evidence never lists them.
	duplicates := <-duplicatesCh
	draft := fmt.Sprintf(`status: draft

Title: <one line, the symptom as the user sees it>

<What happens, one or two sentences. What was expected, one sentence.>

## Steps
<numbered, max 5, or delete this section>

Seen on %s, %s/%s, daemon %s. Related: none.

## Comment
<Duplicates only: the new observation to post to the existing issue. For a new issue, leave empty.>

## Agent-collected evidence
%s`, Version, runtime.GOOS, runtime.GOARCH, st.State, evidence.String())
	quoted := make([]string, len(queries))
	for i, query := range queries {
		quoted[i] = fmt.Sprintf("%q", query)
	}
	search := fmt.Sprintf("\n## Possible duplicates (keyword search: %s; candidates, not confirmed)\n", strings.Join(quoted, "; "))
	if len(queries) == 0 {
		search = "\n## Possible duplicates\nDuplicate pre-search "
	}
	text := redactTriage(b.String(), home) + "\n" + github.summary() + search + redactTriage(duplicates, home)
	if len(text) > triageMaxBytes {
		text = text[:triageMaxBytes-len("\n[truncated]\n")] + "\n[truncated]\n"
	}
	return text, redactTriage(draft, home), github
}

func triageErrorReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return err.Error()
}

// Discard excess bytes while continuing to drain the child's pipes.
type triageOutput struct {
	strings.Builder
	remaining int
}

func (b *triageOutput) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(n, b.remaining)
	_, _ = b.Builder.Write(p[:keep])
	b.remaining -= keep
	return n, nil
}

func triageLogTail(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular log file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	const limit = 16 << 10
	start := max(int64(0), info.Size()-limit)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if start > 0 {
		lines = lines[1:]
	} // Drop the partial first line.
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	return strings.Join(lines, "\n"), nil
}

func (c *commandContext) triageSessions(ctx context.Context, port int, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s:%d/api/v1/sessions", config.LoopbackHost, port), http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.deps.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sessions: HTTP %d", resp.StatusCode)
	}
	var result sessionListResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return err
	}
	slices.SortFunc(result.Sessions, func(a, b sessionDTO) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	for _, s := range result.Sessions[:min(20, len(result.Sessions))] {
		state := s.Status
		if state == "" {
			state = s.Activity.State
		}
		if _, err := fmt.Fprintf(out, "id=%s harness=%s state=%s created=%s updated=%s\n", s.ID, s.Harness, state, s.CreatedAt.Format(time.RFC3339), s.UpdatedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return nil
}
