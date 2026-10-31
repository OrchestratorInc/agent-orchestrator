package coderagents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AO's client-executed tools. Coder's chat loop calls them through
// `unsafe_dynamic_tools`, and AO runs them here, in the session worktree on
// this machine. The ao_ prefix keeps them apart from Coder's built-in tools,
// whose names a dynamic tool may not reuse.
const (
	toolReadFile = "ao_read_file"
	toolListDir  = "ao_list_directory"
	toolWrite    = "ao_write_file"
	toolEdit     = "ao_edit_file"
	toolExecute  = "ao_execute"
)

const (
	maxReadBytes          = 256 << 10
	maxWriteBytes         = 4 << 20
	maxListEntries        = 1000
	defaultCommandTimeout = 2 * time.Minute
	maxCommandTimeout     = 10 * time.Minute
)

// toolClass decides which permission modes need an approval.
type toolClass int

const (
	toolClassRead toolClass = iota
	toolClassEdit
	toolClassCommand
)

type toolSpec struct {
	name        string
	description string
	schema      string
	class       toolClass
}

var toolSpecs = []toolSpec{
	{toolReadFile, "Read a UTF-8 text file in the AO session worktree. Paths are relative to the worktree.",
		`{"type":"object","properties":{"path":{"type":"string","description":"File path relative to the worktree"}},"required":["path"],"additionalProperties":false}`,
		toolClassRead},
	{toolListDir, "List one directory in the AO session worktree. Paths are relative to the worktree; use \".\" for its root.",
		`{"type":"object","properties":{"path":{"type":"string","description":"Directory path relative to the worktree"}},"required":["path"],"additionalProperties":false}`,
		toolClassRead},
	{toolWrite, "Create or overwrite a file in the AO session worktree with the given content.",
		`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`,
		toolClassEdit},
	{toolEdit, "Replace an exact text occurrence in a file in the AO session worktree. old_text must match exactly once unless replace_all is true.",
		`{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_text","new_text"],"additionalProperties":false}`,
		toolClassEdit},
	{toolExecute, "Run a shell command on the user's machine with the AO session worktree as the working directory (or workdir, relative to it).",
		`{"type":"object","properties":{"command":{"type":"string"},"workdir":{"type":"string","description":"Directory relative to the worktree"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":600}},"required":["command"],"additionalProperties":false}`,
		toolClassCommand},
}

func specFor(name string) (toolSpec, bool) {
	for _, spec := range toolSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return toolSpec{}, false
}

// declaredTools is what a new chat declares. A read-only conversation gets
// only the read tools, so Coder never offers the model a way to change files.
func declaredTools(readOnly, disabled bool) []coderapi.DynamicTool {
	if disabled {
		return nil
	}
	out := make([]coderapi.DynamicTool, 0, len(toolSpecs))
	for _, spec := range toolSpecs {
		if readOnly && spec.class != toolClassRead {
			continue
		}
		out = append(out, coderapi.DynamicTool{Name: spec.name, Description: spec.description, InputSchema: json.RawMessage(spec.schema)})
	}
	return out
}

func activityKindFor(spec toolSpec) domain.ActivityKind {
	switch spec.class {
	case toolClassCommand:
		return domain.ActivityKindCommand
	case toolClassEdit:
		return domain.ActivityKindFileChange
	default:
		return domain.ActivityKindMCPTool
	}
}

// toolArgs is the union of every tool's arguments.
type toolArgs struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	OldText        string `json:"old_text"`
	NewText        string `json:"new_text"`
	ReplaceAll     bool   `json:"replace_all"`
	Command        string `json:"command"`
	Workdir        string `json:"workdir"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// toolOutcome is a finished tool call. Output is the JSON returned to Coder.
type toolOutcome struct {
	Output  json.RawMessage `json:"output"`
	IsError bool            `json:"isError"`
	// Display fields for the AO timeline.
	ExitCode *int   `json:"exitCode,omitempty"`
	Text     string `json:"text,omitempty"`
}

func errorOutcome(message string) toolOutcome {
	encoded, _ := json.Marshal(map[string]string{"error": message})
	return toolOutcome{Output: encoded, IsError: true, Text: message}
}

func textOutcome(fields map[string]any, display string) toolOutcome {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return errorOutcome("encode tool result: " + err.Error())
	}
	return toolOutcome{Output: encoded, Text: display}
}

// toolRunner executes tools inside the worktree.
type toolRunner struct {
	root       string
	extraRoots []string
	env        []string
	readOnly   bool
}

func (r *toolRunner) run(ctx context.Context, name string, rawArgs string) toolOutcome {
	spec, ok := specFor(name)
	if !ok {
		return errorOutcome(fmt.Sprintf("unknown AO tool %q", name))
	}
	if r.readOnly && spec.class != toolClassRead {
		return errorOutcome("this AO conversation is read-only")
	}
	var args toolArgs
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return errorOutcome("invalid tool arguments: " + err.Error())
	}
	switch name {
	case toolReadFile:
		return r.readFile(args)
	case toolListDir:
		return r.listDir(args)
	case toolWrite:
		return r.writeFile(args)
	case toolEdit:
		return r.editFile(args)
	case toolExecute:
		return r.execute(ctx, args)
	}
	return errorOutcome(fmt.Sprintf("unknown AO tool %q", name))
}

// resolve maps a tool path to an absolute path inside an allowed root. The
// nearest existing ancestor is resolved through symlinks, so a link inside
// the worktree cannot lead a write or read outside it.
func (r *toolRunner) resolve(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.root, path)
	}
	path = filepath.Clean(path)
	real, err := realPath(path)
	if err != nil {
		return "", err
	}
	for _, root := range append([]string{r.root}, r.extraRoots...) {
		realRoot, err := realPath(filepath.Clean(root))
		if err != nil {
			continue
		}
		if within(realRoot, real) {
			return path, nil
		}
	}
	return "", fmt.Errorf("path %q is outside the AO session worktree", path)
}

// realPath resolves symlinks in the longest existing prefix of path.
func realPath(path string) (string, error) {
	suffix := ""
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, suffix), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path, nil
		}
		suffix = filepath.Join(filepath.Base(current), suffix)
		current = parent
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func (r *toolRunner) relative(path string) string {
	if rel, err := filepath.Rel(r.root, path); err == nil && within(r.root, path) {
		return filepath.ToSlash(rel)
	}
	return path
}

func (r *toolRunner) readFile(args toolArgs) toolOutcome {
	path, err := r.resolve(args.Path)
	if err != nil {
		return errorOutcome(err.Error())
	}
	file, err := os.Open(path)
	if err != nil {
		return errorOutcome(err.Error())
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxReadBytes+1))
	if err != nil {
		return errorOutcome(err.Error())
	}
	truncated := len(data) > maxReadBytes
	if truncated {
		data = data[:maxReadBytes]
	}
	content := string(data)
	return textOutcome(map[string]any{"path": r.relative(path), "content": content, "truncated": truncated},
		commanddetail.Head(content, 2000))
}

func (r *toolRunner) listDir(args toolArgs) toolOutcome {
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}
	path, err := r.resolve(args.Path)
	if err != nil {
		return errorOutcome(err.Error())
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return errorOutcome(err.Error())
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	truncated := len(names) > maxListEntries
	if truncated {
		names = names[:maxListEntries]
	}
	return textOutcome(map[string]any{"path": r.relative(path), "entries": names, "truncated": truncated},
		strings.Join(names, "\n"))
}

func (r *toolRunner) writeFile(args toolArgs) toolOutcome {
	if len(args.Content) > maxWriteBytes {
		return errorOutcome("content exceeds the 4 MiB AO write limit")
	}
	path, err := r.resolve(args.Path)
	if err != nil {
		return errorOutcome(err.Error())
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, fs.ErrNotExist)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errorOutcome(err.Error())
	}
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil { //nolint:gosec // worktree files keep normal permissions
		return errorOutcome(err.Error())
	}
	status := "modified"
	if created {
		status = "added"
	}
	return textOutcome(map[string]any{"path": r.relative(path), "status": status, "bytes": len(args.Content)},
		status+" "+r.relative(path))
}

func (r *toolRunner) editFile(args toolArgs) toolOutcome {
	if args.OldText == "" {
		return errorOutcome("old_text must not be empty")
	}
	path, err := r.resolve(args.Path)
	if err != nil {
		return errorOutcome(err.Error())
	}
	info, err := os.Stat(path)
	if err != nil {
		return errorOutcome(err.Error())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errorOutcome(err.Error())
	}
	text := string(data)
	count := strings.Count(text, args.OldText)
	switch {
	case count == 0:
		return errorOutcome("old_text was not found in " + r.relative(path))
	case count > 1 && !args.ReplaceAll:
		return errorOutcome(fmt.Sprintf("old_text matches %d times in %s; make it unique or set replace_all", count, r.relative(path)))
	}
	replaced := strings.Replace(text, args.OldText, args.NewText, 1)
	if args.ReplaceAll {
		replaced = strings.ReplaceAll(text, args.OldText, args.NewText)
	}
	if err := os.WriteFile(path, []byte(replaced), info.Mode().Perm()); err != nil {
		return errorOutcome(err.Error())
	}
	return textOutcome(map[string]any{"path": r.relative(path), "status": "modified", "replacements": count},
		"modified "+r.relative(path))
}

func (r *toolRunner) execute(ctx context.Context, args toolArgs) toolOutcome {
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return errorOutcome("command is required")
	}
	dir := r.root
	if strings.TrimSpace(args.Workdir) != "" {
		resolved, err := r.resolve(args.Workdir)
		if err != nil {
			return errorOutcome(err.Error())
		}
		dir = resolved
	}
	timeout := defaultCommandTimeout
	if args.TimeoutSeconds > 0 {
		timeout = min(time.Duration(args.TimeoutSeconds)*time.Second, maxCommandTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := shellCommand(ctx, command)
	cmd.Dir = dir
	cmd.Env = r.env
	cmd.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr):
			exitCode = exitErr.ExitCode()
		case ctx.Err() != nil:
			exitCode = -1
		default:
			return errorOutcome("start command: " + err.Error())
		}
	}
	text, cut := commanddetail.TailChars(output.String(), commanddetail.MaxOutputChars)
	fields := map[string]any{"exit_code": exitCode, "output": text, "truncated": cut}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fields["timed_out"] = true
	}
	outcome := textOutcome(fields, text)
	outcome.ExitCode = &exitCode
	return outcome
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", command) //nolint:gosec // the model-requested command is the tool's purpose; approval gates it
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command) //nolint:gosec // see above
}

// toolEnv is the command environment: the session env without any Coder
// credential, so a command the model runs can never read AO's Coder session.
func toolEnv(merged []string) []string {
	out := make([]string, 0, len(merged))
	for _, entry := range merged {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case coderapi.EnvSessionToken, "CODER_AGENT_TOKEN", "CODER_AGENT_TOKEN_FILE":
			continue
		}
		out = append(out, entry)
	}
	return out
}
