package omp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const ompConfigDirEnv = "PI_CODING_AGENT_DIR"

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
	_ ports.AgentImportedSessionRelocator    = (*Plugin)(nil)
)

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

func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	return nativeconfig.ProbeTranscript(ctx, ref, p.LocateTranscript)
}

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
	path, found, err := nativeconfig.FindFile(ctx, filepath.Join(configDir, "sessions"), func(name string) bool {
		return strings.HasSuffix(name, suffix)
	})
	if err != nil {
		return "", false, fmt.Errorf("omp: scan transcripts: %w", err)
	}
	return path, found, nil
}

func validateOMPNativeSessionID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, `/\`+"\x00") {
		return "", errors.New("omp: invalid native session id")
	}
	return value, nil
}

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
	return importcopy.CopyOnce(src, dst, rewrite, check)
}
