package dockersandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/container/dockerreap"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

const (
	probeTimeout  = 30 * time.Second
	createTimeout = 10 * time.Minute // `docker run` may pull the image first
)

// dockerFallbacks covers GUI launches whose PATH lacks the Docker CLI.
var dockerFallbacks = []string{
	"/usr/local/bin/docker",
	"/opt/homebrew/bin/docker",
	"/Applications/Docker.app/Contents/Resources/bin/docker",
}

type commandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && stderr.Len() > 0 {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

// Config locates AO state. Both paths must be under the AO data dir.
type Config struct {
	DataDir     string
	ManagedRoot string // workspace adapter's managed worktree root
	// DaemonPort returns the daemon's bound loopback port, probed to prove
	// the sandbox can't reach it. It is read on every Ensure because the
	// listener binds after the sandbox is built and may fall back to an
	// ephemeral port. nil, or a result of 0, skips the probe.
	DaemonPort func() int
}

func (c Config) daemonPort() int {
	if c.DaemonPort == nil {
		return 0
	}
	return c.DaemonPort()
}

// Sandbox implements ports.Sandbox with the docker CLI.
type Sandbox struct {
	cfg    Config
	run    commandRunner
	docker string // resolved lazily unless injected by tests

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// Engine kind from `docker info`, cached under mu once known.
	engineKnown, engineRequiresAlias bool
}

var _ ports.Sandbox = (*Sandbox)(nil)

// New returns a docker-CLI sandbox.
func New(cfg Config) *Sandbox { return newWithRunner(cfg, execRunner{}, "") }

func newWithRunner(cfg Config, r commandRunner, docker string) *Sandbox {
	return &Sandbox{cfg: cfg, run: r, docker: docker, locks: map[string]*sync.Mutex{}}
}

func (s *Sandbox) lock(name string) func() {
	s.mu.Lock()
	l, ok := s.locks[name]
	if !ok {
		l = &sync.Mutex{}
		s.locks[name] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *Sandbox) dockerPath() (string, error) {
	if s.docker != "" {
		return s.docker, nil
	}
	if p, err := exec.LookPath("docker"); err == nil {
		return p, nil
	}
	for _, p := range dockerFallbacks {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: the docker CLI was not found; install Docker Desktop or Docker Engine", ports.ErrSandboxUnavailable)
}

// projectStateDir is a project's sandbox state root under the data dir.
func projectStateDir(dataDir string, project domain.ProjectID) string {
	return filepath.Join(dataDir, "sandbox", "projects", string(project))
}

// SessionStateDir is a session's sandbox state dir. Ensure creates it before
// the container exists, so its presence proves the session's agent may have
// run in a container. The daemon then keeps AO's host git hardened for that
// worktree even after the sandbox config is turned off.
//
// Session state dirs must only ever be cleaned up after the session's
// worktree is gone; removing one earlier would drop that hardening.
func SessionStateDir(dataDir string, project domain.ProjectID, session domain.SessionID) string {
	return filepath.Join(projectStateDir(dataDir, project), "sessions", string(session))
}

func (s *Sandbox) targetFor(container string, project domain.ProjectID, session domain.SessionID) ports.SandboxTarget {
	projectDir := projectStateDir(s.cfg.DataDir, project)
	sessionDir := SessionStateDir(s.cfg.DataDir, project, session)
	return ports.SandboxTarget{
		Container:  container,
		Docker:     s.docker,
		BinDir:     filepath.Join(s.cfg.DataDir, "sandbox", "bin"),
		ProjectDir: projectDir,
		SessionDir: sessionDir,
		RelayDir:   filepath.Join(sessionDir, "relay"),
	}
}

func (s *Sandbox) mounts(req ports.SandboxRequest, g gitLayout) ([]mount, error) {
	m := []mount{{Path: req.WorkspacePath}}
	projectDir := projectStateDir(s.cfg.DataDir, req.ProjectID)
	// Per-agent containers see only their own session dir and prompts, not
	// siblings'. A shared container serves every session of the project.
	state := []mount{
		{Path: filepath.Join(projectDir, "home")},
		{Path: filepath.Join(projectDir, "claude")},
		{Path: SessionStateDir(s.cfg.DataDir, req.ProjectID, req.SessionID)},
	}
	prompts := filepath.Join(s.cfg.DataDir, "prompts", string(req.SessionID))
	// The system prompt tells the agent to write deliverables to its host
	// artifact dir, so that path must be writable inside the container.
	artifacts := sessionartifacts.Dir(s.cfg.DataDir, req.SessionID)
	if req.Config.Mode == domain.SandboxModeShared {
		work, err := sharedWorkRoot(filepath.Join(s.cfg.ManagedRoot, string(req.ProjectID)), req.WorkspacePath)
		if err != nil {
			return nil, err
		}
		m = []mount{{Path: work}}
		// The orchestrator's worktree lives under the same project root; an
		// empty read-only tmpfs hides it from sandboxed workers.
		if orch := filepath.Join(work, "orchestrator"); !within(orch, req.WorkspacePath) {
			m = append(m, mount{Path: orch, Tmpfs: true})
		}
		state = []mount{{Path: projectDir}}
		prompts = filepath.Join(s.cfg.DataDir, "prompts")
		artifacts = filepath.Join(s.cfg.DataDir, "artifacts")
	}
	m = append(m, gitMounts(req, g)...)
	m = append(m, mount{Path: prompts, ReadOnly: true}, mount{Path: artifacts})
	m = append(m, state...)
	return append(m, mount{Path: filepath.Join(s.cfg.DataDir, "sandbox", "bin"), ReadOnly: true}), nil
}

// Ensure returns a running container for req, creating or starting it when
// needed. It is idempotent so restore after a Docker restart works.
func (s *Sandbox) Ensure(ctx context.Context, req ports.SandboxRequest) (ports.SandboxTarget, error) {
	if !req.Config.Enabled() {
		return ports.SandboxTarget{}, fmt.Errorf("%w: sandbox mode is off", ports.ErrSandboxMisconfigured)
	}
	docker, err := s.dockerPath()
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	git, err := s.resolveGit(ctx, req)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	mounts, err := s.mounts(req, git)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	hash := configHash(req.Config, mounts)
	name := containerName(req, hash)
	if req.Config.Mode == domain.SandboxModeShared {
		defer s.lock("project:" + string(req.ProjectID))()
	}
	defer s.lock(name)()

	target := s.targetFor(name, req.ProjectID, req.SessionID)
	target.Docker = docker
	target.Mode = req.Config.Mode
	target.GitUserName, target.GitUserEmail = git.userName, git.userEmail
	if err := ensureStateDirs(target.ProjectDir, req.SessionID); err != nil {
		return ports.SandboxTarget{}, fmt.Errorf("sandbox state dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(s.cfg.DataDir, "prompts", string(req.SessionID)), 0o700); err != nil {
		return ports.SandboxTarget{}, fmt.Errorf("sandbox prompts dir: %w", err)
	}
	if err := os.MkdirAll(sessionartifacts.Dir(s.cfg.DataDir, req.SessionID), 0o700); err != nil {
		return ports.SandboxTarget{}, fmt.Errorf("sandbox artifact dir: %w", err)
	}
	if err := InstallBin(target.BinDir); err != nil {
		return ports.SandboxTarget{}, err
	}
	if err := requireMountSources(mounts); err != nil {
		return ports.SandboxTarget{}, err
	}

	status, existingHash, err := s.inspect(ctx, docker, name)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	created := false
	switch {
	case status == "created" || (status != "" && status != "running" && existingHash != hash):
		// A stopped container with stale config is recreated; the worktree
		// lives on the host. A running one may hold a live agent: reuse it.
		// One left in "created" by a failed `docker run` never ran, and
		// starting it would only repeat that failure.
		if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", name); err != nil {
			return ports.SandboxTarget{}, err
		}
		fallthrough
	case status == "":
		if err := s.ensureNetwork(ctx, docker); err != nil {
			return ports.SandboxTarget{}, err
		}
		labels := map[string]string{labelSandbox: string(req.Config.Mode), labelProject: string(req.ProjectID), labelHash: hash}
		if req.Config.Mode == domain.SandboxModePerAgent {
			labels[dockerreap.SessionLabel] = string(req.SessionID)
		}
		if _, err := s.output(ctx, createTimeout, docker, runArgs(name, req.Config, labels, mounts, hostUser())...); err != nil {
			if errors.Is(err, ports.ErrSandboxUnavailable) {
				return ports.SandboxTarget{}, err
			}
			err = fmt.Errorf("%w: start sandbox from image %s (the image must exist or be pullable and provide /bin/sh): %w",
				ports.ErrSandboxMisconfigured, req.Config.Image, err)
			// `docker run -d` can create the container and then fail to start
			// it; nothing else ever owns that name, so remove it.
			if _, rmErr := s.output(ctx, probeTimeout, docker, "rm", "-f", name); rmErr != nil && !errors.Is(rmErr, ports.ErrSandboxUnavailable) {
				err = fmt.Errorf("%w (removing the failed container also failed: %w)", err, rmErr)
			}
			return ports.SandboxTarget{}, err
		}
		created = true
	case status != "running":
		// `docker network prune` removes the network while this stopped
		// container still names it, and start would then fail.
		if err := s.ensureNetwork(ctx, docker); err != nil {
			return ports.SandboxTarget{}, err
		}
		if _, err := s.output(ctx, probeTimeout, docker, "start", name); err != nil {
			if errors.Is(err, ports.ErrSandboxUnavailable) {
				return ports.SandboxTarget{}, err
			}
			return ports.SandboxTarget{}, fmt.Errorf("%w: start stopped sandbox container %s (remove it with `docker rm -f %s` to recreate it): %w",
				ports.ErrSandboxMisconfigured, name, name, err)
		}
	}
	// Remove only a per-agent container this call created; a reused or
	// started one may belong to a live session, and shared containers belong
	// to Release.
	discardNew := func(err error) error {
		if created && req.Config.Mode == domain.SandboxModePerAgent {
			if _, rmErr := s.output(ctx, probeTimeout, docker, "rm", "-f", name); rmErr != nil {
				err = fmt.Errorf("%w (removing the new container also failed: %w)", err, rmErr)
			}
		}
		return err
	}
	if err := s.isolateHostNetwork(ctx, docker, name); err != nil {
		if errors.Is(err, ports.ErrSandboxUnavailable) {
			return ports.SandboxTarget{}, err
		}
		return ports.SandboxTarget{}, discardNew(err)
	}
	for _, bin := range req.RequireBinaries {
		if _, err := s.output(ctx, probeTimeout, docker, "exec", name, "/bin/sh", "-c", `command -v "$1"`, "sh", bin); err != nil {
			if errors.Is(err, ports.ErrSandboxUnavailable) {
				return ports.SandboxTarget{}, err
			}
			return ports.SandboxTarget{}, fmt.Errorf("%w: image %s has no %q on PATH (and needs /bin/sh): %w",
				ports.ErrSandboxMisconfigured, req.Config.Image, bin, discardNew(err))
		}
	}
	return target, nil
}

// inspect returns ("", "", nil) when the container does not exist.
func (s *Sandbox) inspect(ctx context.Context, docker, name string) (string, string, error) {
	out, err := s.output(ctx, probeTimeout, docker, "inspect", "--type", "container",
		"--format", `{{.State.Status}}	{{index .Config.Labels "`+labelHash+`"}}`, name)
	if err != nil {
		if msg := strings.ToLower(err.Error()); strings.Contains(msg, "no such object") || strings.Contains(msg, "no such container") {
			return "", "", nil
		}
		return "", "", err
	}
	status, hash, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	return status, hash, nil
}

func (s *Sandbox) output(ctx context.Context, timeout time.Duration, docker string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := s.run.Output(ctx, docker, args...)
	if err != nil && (dockerreap.IsDockerUnavailable(err) || dockerSocketDenied(err)) {
		return out, fmt.Errorf("%w: Docker is not running or not reachable: %w", ports.ErrSandboxUnavailable, err)
	}
	return out, err
}

// Exec writes the session env file (0600) and returns the docker exec argv.
func (s *Sandbox) Exec(target ports.SandboxTarget, workspacePath string, env map[string]string, argv []string) ([]string, error) {
	merged := make(map[string]string, len(env)+7)
	for k, v := range env {
		merged[k] = v
	}
	if err := addGitConfigEnv(merged, target); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrSandboxMisconfigured, err)
	}
	data, err := renderEnvFile(merged)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrSandboxMisconfigured, err)
	}
	// Set after rendering so hostOnlyEnv cannot drop them.
	data = append(data, []byte("HOME="+shQuote(filepath.Join(target.ProjectDir, "home"))+"\nAO_SANDBOX='1'\nAO_SANDBOX_RELAY="+shQuote(target.RelayDir)+"\n")...)
	if err := writeSessionEnv(target, data); err != nil {
		return nil, err
	}
	docker := target.Docker
	if docker == "" {
		if docker, err = s.dockerPath(); err != nil {
			return nil, err
		}
	}
	return execArgv(docker, target, workspacePath, argv), nil
}

// Discard removes a per-agent container after a failed launch. Shared
// containers belong to Release; an unreachable Docker is not an error.
func (s *Sandbox) Discard(ctx context.Context, target ports.SandboxTarget) error {
	if target.Mode != domain.SandboxModePerAgent || target.Container == "" {
		return nil
	}
	defer s.lock(target.Container)()
	docker := target.Docker
	if docker == "" {
		var err error
		if docker, err = s.dockerPath(); err != nil {
			return nil
		}
	}
	if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", target.Container); err != nil && !errors.Is(err, ports.ErrSandboxUnavailable) {
		return err
	}
	return nil
}

// HasSessionState reports whether the session's state dir is a real
// directory. Lstat never follows a link planted in its place.
func (s *Sandbox) HasSessionState(project domain.ProjectID, session domain.SessionID) bool {
	if s.cfg.DataDir == "" || project == "" || session == "" {
		return false
	}
	fi, err := os.Lstat(SessionStateDir(s.cfg.DataDir, project, session))
	return err == nil && fi.IsDir()
}

// DiscardSession removes a terminated session's per-agent container, found by
// its labels so no container target is needed. Shared containers never match;
// an unreachable Docker is not an error.
func (s *Sandbox) DiscardSession(ctx context.Context, project domain.ProjectID, session domain.SessionID) error {
	defer s.lock(containerName(ports.SandboxRequest{SessionID: session, Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent}}, ""))()
	docker, err := s.dockerPath()
	if err != nil {
		return nil
	}
	out, err := s.output(ctx, probeTimeout, docker, "ps", "-a",
		"--filter", "label="+labelSandbox+"="+string(domain.SandboxModePerAgent),
		"--filter", "label="+dockerreap.SessionLabel+"="+string(session),
		"--filter", "label="+labelProject+"="+string(project), "--format", "{{.ID}}")
	if err != nil {
		if errors.Is(err, ports.ErrSandboxUnavailable) {
			return nil
		}
		return err
	}
	var errs []error
	for _, id := range strings.Fields(string(out)) {
		if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", id); err != nil && !errors.Is(err, ports.ErrSandboxUnavailable) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// dockerSocketDenied reports a Docker socket permission error, which means
// the daemon is unusable for this user. It matches only the CLI's connect
// error ("... to connect to the Docker daemon socket" or "... the docker
// API"), not a permission error from inside a container or image, which
// `docker run` also reports with a "docker:" prefix.
func dockerSocketDenied(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "permission denied while trying to connect to the docker")
}

// Release removes the project's shared containers once nothing uses them.
// inUse runs under the project lock, so it cannot race a shared Ensure; an
// error or true keeps the containers. An unreachable Docker is not an error.
func (s *Sandbox) Release(ctx context.Context, project domain.ProjectID, inUse func(context.Context) (bool, error)) error {
	defer s.lock("project:" + string(project))()
	used, err := inUse(ctx)
	if err != nil || used {
		return nil
	}
	docker, err := s.dockerPath()
	if err != nil {
		return nil
	}
	out, err := s.output(ctx, probeTimeout, docker, "ps", "-a", "--filter", "label="+labelProject+"="+string(project),
		"--filter", "label="+labelSandbox+"=shared", "--format", "{{.ID}}")
	if err != nil {
		if errors.Is(err, ports.ErrSandboxUnavailable) {
			return nil
		}
		return err
	}
	var errs []error
	for _, id := range strings.Fields(string(out)) {
		if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// hostUser runs the container as the host user so files written to the
// worktree keep the user's ownership on Linux.
func hostUser() string {
	return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
}
