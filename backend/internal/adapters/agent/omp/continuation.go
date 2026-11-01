package omp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ompConfigDirEnv overrides OMP's session-state root. OMP is a Pi fork and
// reuses Pi's variable name; it defaults to ~/.omp/agent.
const ompConfigDirEnv = "PI_CODING_AGENT_DIR"

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
	_ ports.AgentImportedSessionRelocator    = (*Plugin)(nil)
)

// NativeSessionConfigDir returns OMP's session-state root: PI_CODING_AGENT_DIR
// when set, otherwise ~/.omp/agent.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, ompConfigDirEnv, filepath.Join(".omp", "agent"))
	if err != nil {
		return "", fmt.Errorf("omp: resolve config dir: %w", err)
	}
	return dir, nil
}

// ProbeNativeSession reports whether OMP's local transcript for the native id
// still exists under the recorded config dir. A missing config dir is Unknown
// rather than Unavailable, mirroring the other continuation adapters.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, ok, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// LocateTranscript finds OMP's rollout JSONL by native id. OMP writes one
// conversation per file at <config>/sessions/<cwd-folder>/<ISO-time>_<id>.jsonl;
// a suffix match finds the original or its AO copy without depending on the
// folder-name encoding.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	id, err := validateOMPNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return "", false, err
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	suffix := "_" + id + ".jsonl"
	var found string
	walkErr := filepath.WalkDir(filepath.Join(configDir, "sessions"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), suffix) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(walkErr, os.ErrNotExist) {
		return "", false, nil
	}
	if walkErr != nil {
		return "", false, fmt.Errorf("omp: scan transcripts: %w", walkErr)
	}
	return found, found != "", nil
}

func validateOMPNativeSessionID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, `/\`+"\x00") {
		return "", errors.New("omp: invalid native session id")
	}
	return value, nil
}

// ompSessionDir encodes the folder OMP derives from a working directory, so AO
// can place an imported transcript where OMP will look when launched in the AO
// worktree. Pinned empirically against omp 18.14.0 (the binary ships no source):
// the folder is built from the RESOLVED path. A path under $HOME has the home
// prefix stripped and each "/" turned into "-" (e.g. ~/.ao/data/wt/x =>
// "-.ao-data-wt-x"); any other path is wrapped, "--" + path without its leading
// "/" and "/"->"-" + "--" (e.g. /private/tmp/a/b => "--private-tmp-a-b--").
// Dots and other characters are preserved. Callers pass the real user home; both
// paths are symlink-resolved (AO worktrees live under $HOME, so the home-relative
// form is the one that matters at resume).
func ompSessionDir(home, cwd string) string {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Clean(r)
		}
		return filepath.Clean(p)
	}
	resolved, home := resolve(cwd), resolve(home)
	sep := string(filepath.Separator)
	if home != "" && home != "." && (resolved == home || strings.HasPrefix(resolved, home+sep)) {
		return strings.ReplaceAll(strings.TrimPrefix(resolved, home), sep, "-")
	}
	return "--" + strings.ReplaceAll(strings.TrimPrefix(resolved, sep), sep, "-") + "--"
}

// RelocateImportedSession places the imported transcript where OMP's ACP
// session/load looks for LaunchCWD. The folder uses the resolved path, while
// the session record must keep the raw launch path. The shared helper keeps
// the original untouched and never replaces a copy holding AO's later turns.
func (p *Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id, err := validateOMPNativeSessionID(r.NativeSessionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.ConfigDir) == "" || !filepath.IsAbs(r.LaunchCWD) {
		return errors.New("omp: relocation requires a config directory and absolute launch directory")
	}
	src := r.TranscriptPath
	if src == "" {
		var ok bool
		src, ok, err = p.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: id, ConfigDir: r.ConfigDir})
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("omp: imported transcript is missing")
		}
	}
	if !strings.HasSuffix(filepath.Base(src), "_"+id+".jsonl") {
		return errors.New("omp: transcript filename does not match the native session id")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dst := filepath.Join(r.ConfigDir, "sessions", ompSessionDir(home, r.LaunchCWD), filepath.Base(src))
	sessions := 0
	rewrite := func(line []byte) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if kind, _ := importcopy.StringField(line, "type"); kind != "session" {
			return line, nil
		}
		sessions++
		if headerID, _ := importcopy.StringField(line, "id"); headerID != id {
			return nil, errors.New("omp: session record does not match the native session id")
		}
		return importcopy.SetStringField(line, "cwd", r.LaunchCWD)
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if sessions != 1 {
			return errors.New("omp: imported transcript must contain exactly one session record")
		}
		return nil
	}
	_, err = importcopy.CopyOnce(src, dst, rewrite, check)
	return err
}
