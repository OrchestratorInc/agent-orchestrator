// Package dockersandbox runs AO agent processes inside local Docker
// containers (#6663). The daemon, runtime and worktrees stay on the host; this
// package only creates containers and rewrites a launch into `docker exec`.
// Every host path is mounted at the same absolute path inside the container.
package dockersandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Labels on every AO sandbox container. Per-agent containers also carry
// dockerreap.SessionLabel so session termination removes them.
const (
	labelSandbox = "ao.sandbox"
	labelProject = "ao.project"
	labelHash    = "ao.sandbox.hash"
	labelGitDir  = "ao.sandbox.gitdir"
)

// keepAliveScript keeps the container alive without depending on the image's
// entrypoint and exits promptly on `docker stop`.
const keepAliveScript = `trap "exit 0" TERM INT; while :; do sleep 3600 & wait $!; done`

type mount struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"ro,omitempty"`
	Tmpfs    bool   `json:"tmpfs,omitempty"` // empty read-only tmpfs that hides Path
}

var (
	envKeyPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	nameUnsafeChar = regexp.MustCompile(`[^a-z0-9_.-]+`)
)

// hostOnlyEnv never crosses into the container: these describe the host
// process environment, and the image supplies its own.
var hostOnlyEnv = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "SHELL": true, "PWD": true,
	"OLDPWD": true, "USER": true, "LOGNAME": true, "TMUX": true, "TMUX_PANE": true,
}

var hostOnlyEnvPrefixes = []string{"DYLD_", "__CF", "TERM_PROGRAM", "XPC_"}

func sanitizeName(s string) string {
	return strings.Trim(nameUnsafeChar.ReplaceAllString(strings.ToLower(s), "-"), "-.")
}

// nameFor keeps clean IDs as they are. Any ID that sanitization would change
// (or that sanitizes to nothing) gets a short hash of the original, so IDs
// that differ only by case or unsafe characters never share a container name.
func nameFor(id string) string {
	s := sanitizeName(id)
	if s != "" && s == id {
		return s
	}
	sum := sha256.Sum256([]byte(id))
	suffix := hex.EncodeToString(sum[:])[:8]
	if s == "" {
		return suffix
	}
	return s + "-" + suffix
}

func containerName(req ports.SandboxRequest, hash string) string {
	if req.Config.Mode == domain.SandboxModeShared {
		return "ao-sbx-" + nameFor(string(req.ProjectID)) + "-" + hash[:8]
	}
	return "ao-sbx-" + nameFor(string(req.SessionID))
}

func configHash(cfg domain.SandboxConfig, mounts []mount) string {
	data, _ := json.Marshal(struct {
		Cfg    domain.SandboxConfig `json:"cfg"`
		Mounts []mount              `json:"mounts"`
	}{cfg, mounts})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

func runArgs(name string, cfg domain.SandboxConfig, labels map[string]string, mounts []mount, user string) []string {
	args := []string{"run", "-d", "--init", "--name", name}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}
	if user != "" {
		args = append(args, "--user", user)
	}
	for _, m := range mounts {
		if m.Tmpfs {
			args = append(args, "--mount", "type=tmpfs,destination="+m.Path+",readonly")
			continue
		}
		spec := m.Path + ":" + m.Path
		if m.ReadOnly {
			spec += ":ro"
		}
		args = append(args, "-v", spec)
	}
	if cfg.CPUs != "" {
		args = append(args, "--cpus", cfg.CPUs)
	}
	if cfg.Memory != "" {
		args = append(args, "--memory", cfg.Memory)
	}
	if cfg.PidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprint(cfg.PidsLimit))
	}
	return append(args, "--entrypoint", "/bin/sh", cfg.Image, "-c", keepAliveScript)
}

// renderEnvFile renders env as POSIX sh assignments for `set -a; . file`.
// Values are single-quoted so the shell never expands them.
func renderEnvFile(env map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if hostOnlyEnv[k] || hasHostOnlyPrefix(k) {
			continue
		}
		if !envKeyPattern.MatchString(k) {
			return nil, fmt.Errorf("sandbox env: invalid variable name %q", k)
		}
		v := env[k]
		if strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("sandbox env: %s contains a NUL byte", k)
		}
		b.WriteString(k + "=" + shQuote(v) + "\n")
	}
	return []byte(b.String()), nil
}

// shQuote single-quotes v for POSIX sh so it is never expanded.
func shQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func hasHostOnlyPrefix(k string) bool {
	for _, p := range hostOnlyEnvPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

func execArgv(docker string, t ports.SandboxTarget, workspacePath string, argv []string) []string {
	head := []string{docker, "exec", "-i", "-t", "-w", workspacePath, t.Container,
		"/bin/sh", t.BinDir + "/ao-enter", t.SessionDir + "/env"}
	out := make([]string, 0, len(head)+len(argv))
	out = append(out, head...)
	return append(out, argv...)
}
