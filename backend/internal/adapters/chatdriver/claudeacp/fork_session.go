package claudeacp

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

//go:embed fork_session.mjs
var forkSessionScript []byte

var _ ports.ChatWorkspaceForker = (*checkpointDriver)(nil)

type nativeConfigDirResolver interface {
	NativeSessionConfigDir(context.Context, map[string]string) (string, error)
}

func (d *checkpointDriver) ForkIntoWorkspace(ctx context.Context, sourceID, sourceWorkspace, upToMessageID string, cfg ports.ChatStartConfig) (string, error) {
	resolver, ok := d.plugin.(nativeConfigDirResolver)
	if !ok {
		return "", ports.ErrChatCapabilityUnavailable
	}
	configDir, err := resolver.NativeSessionConfigDir(ctx, cfg.Env)
	if err != nil {
		return "", err
	}
	src, err := filepath.EvalSymlinks(sourceWorkspace)
	if err != nil {
		return "", fmt.Errorf("claude fork: source workspace: %w", err)
	}
	dst, err := filepath.EvalSymlinks(cfg.WorkspacePath)
	if err != nil {
		return "", fmt.Errorf("claude fork: fork workspace: %w", err)
	}
	cut := ""
	if upToMessageID != "" {
		sourceFile := filepath.Join(configDir, "projects", claudeProjectKey(src), sourceID+".jsonl")
		if cut, err = resolveForkPoint(ctx, sourceFile, upToMessageID); err != nil {
			return "", fmt.Errorf("claude fork: %w", err)
		}
	}
	env := append(os.Environ(), envList(cfg.Env)...)
	env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	childID, err := runForkScript(ctx, env, sourceID, src, cut)
	if err != nil {
		return "", fmt.Errorf("claude fork: %w", err)
	}
	if childID == "" || childID == sourceID || strings.ContainsAny(childID, `/\`) {
		return "", fmt.Errorf("claude fork: invalid session id %q", childID)
	}
	from := filepath.Join(configDir, "projects", claudeProjectKey(src), childID+".jsonl")
	to := filepath.Join(configDir, "projects", claudeProjectKey(dst), childID+".jsonl")
	if err := moveSessionFile(ctx, from, to, []string{src, filepath.Clean(sourceWorkspace)}, dst); err != nil {
		_ = os.Remove(from)
		return "", fmt.Errorf("claude fork: move session: %w", err)
	}
	return childID, nil
}

func (d *checkpointDriver) DeleteWorkspaceFork(ctx context.Context, providerConversationID, _ string, env map[string]string) error {
	resolver, ok := d.plugin.(nativeConfigDirResolver)
	if !ok || providerConversationID == "" || strings.ContainsAny(providerConversationID, `/\*?[`) {
		return nil
	}
	configDir, err := resolver.NativeSessionConfigDir(ctx, env)
	if err != nil {
		return err
	}
	copies, err := filepath.Glob(filepath.Join(configDir, "projects", "*", providerConversationID+".jsonl"))
	if err != nil {
		return err
	}
	var errs []error
	for _, path := range copies {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var nonAlphanumeric = regexp.MustCompile(`[^A-Za-z0-9]`)

func resolveForkPoint(ctx context.Context, sessionFile, apiMessageID string) (string, error) {
	file, err := os.Open(sessionFile)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 256<<20)
	last := ""
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var line struct {
			UUID    string `json:"uuid"`
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) == nil && line.Message.ID == apiMessageID && line.UUID != "" {
			last = line.UUID
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if last == "" {
		return "", fmt.Errorf("message %s is not in the session", apiMessageID)
	}
	return last, nil
}

func claudeProjectKey(realPath string) string {
	return nonAlphanumeric.ReplaceAllString(realPath, "-")
}

func moveSessionFile(ctx context.Context, from, to string, sources []string, dst string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), ".ao-fork-*.jsonl")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	out := bufio.NewWriter(tmp)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			_ = tmp.Close()
			return err
		}
		line := rewriteWorkspacePaths(scanner.Bytes(), sources, dst)
		if _, err := out.Write(append(line, '\n')); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := out.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), to); err != nil {
		return err
	}
	_ = in.Close()
	return os.Remove(from)
}

func rewriteWorkspacePaths(line []byte, sources []string, dst string) []byte {
	to := jsonStringBody(dst)
	for _, src := range sources {
		if src == "" || src == dst {
			continue
		}
		from := jsonStringBody(src)
		var out []byte
		rest := line
		for {
			i := bytes.Index(rest, from)
			if i < 0 {
				break
			}
			end := i + len(from)
			if end < len(rest) && continuesPathName(rest[end]) {
				out = append(out, rest[:end]...)
				rest = rest[end:]
				continue
			}
			out = append(append(out, rest[:i]...), to...)
			rest = rest[end:]
		}
		if out != nil {
			out = append(out, rest...)
			line = out
		}
	}
	return line
}

func continuesPathName(c byte) bool {
	return c >= 0x80 || c == '.' || c == '-' || c == '_' ||
		('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func jsonStringBody(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimSuffix(bytes.TrimPrefix(bytes.TrimSpace(b.Bytes()), []byte(`"`)), []byte(`"`))
}

var runForkScript = func(ctx context.Context, env []string, sourceID, dir, upTo string) (string, error) {
	runtimeDir := strings.TrimSpace(os.Getenv("AO_ACP_RUNTIME_DIR"))
	if runtimeDir == "" {
		runtimeDir = runtimeDirectoryBesideExecutable()
	}
	if runtimeDir == "" {
		return "", errors.New("AO ACP runtime is not installed")
	}
	node := filepath.Join(runtimeDir, "node", "bin", "node")
	sdk := filepath.Join(runtimeDir, "node_modules", "@anthropic-ai", "claude-agent-sdk", "sdk.mjs")
	if err := requireFile(node, "packaged Node runtime"); err != nil {
		return "", err
	}
	if err := requireFile(sdk, "Claude Agent SDK"); err != nil {
		return "", err
	}
	node, err := jitCapableNode(ctx, node)
	if err != nil {
		return "", err
	}
	script, err := os.CreateTemp("", "ao-claude-fork-*.mjs")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(script.Name()) }()
	if _, err := script.Write(forkSessionScript); err != nil {
		_ = script.Close()
		return "", err
	}
	_ = script.Close()
	cmd := aoprocess.CommandContext(ctx, node, script.Name(), sdk, sourceID, dir, upTo)
	cmd.Env = env
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("fork script: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		return "", fmt.Errorf("fork script output: %w", err)
	}
	return result.SessionID, nil
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
