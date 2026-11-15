package qwen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/importcopy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const qwenHomeEnv = "QWEN_HOME"

var qwenNativeSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
	_ ports.AgentImportedSessionRelocator    = (*Plugin)(nil)
)

func sanitizeQwenCwdForPlatform(cwd string, windows bool) string {
	if windows {
		cwd = strings.ToLower(strings.ReplaceAll(cwd, "İ", "i\u0307"))
	}
	var encoded strings.Builder
	for _, c := range utf16.Encode([]rune(cwd)) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			encoded.WriteByte(byte(c))
		} else {
			encoded.WriteByte('-')
		}
	}
	return encoded.String()
}

func qwenChatPath(configDir, cwd, sessionID string) string {
	return filepath.Join(configDir, "projects", sanitizeQwenCwdForPlatform(cwd, runtime.GOOS == "windows"), "chats", sessionID+".jsonl")
}

func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, qwenHomeEnv, ".qwen")
	if err != nil {
		return "", fmt.Errorf("qwen: resolve config dir: %w", err)
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
	sessionID := strings.TrimSpace(ref.NativeSessionID)
	if !qwenNativeSessionID.MatchString(sessionID) {
		return "", false, fmt.Errorf("qwen: invalid native session id")
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	projectsDir := filepath.Join(configDir, "projects")
	projects, err := os.ReadDir(projectsDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("qwen: read projects: %w", err)
	}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !project.IsDir() {
			continue
		}
		path := filepath.Join(projectsDir, project.Name(), "chats", sessionID+".jsonl")
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Mode().IsRegular():
			return path, true, nil
		case err == nil, errors.Is(err, os.ErrNotExist), errors.Is(err, os.ErrPermission):
			continue
		default:
			return "", false, fmt.Errorf("qwen: stat transcript: %w", err)
		}
	}
	return "", false, nil
}

func (p *Plugin) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !qwenNativeSessionID.MatchString(r.NativeSessionID) {
		return errors.New("qwen: invalid native session id")
	}
	if strings.TrimSpace(r.ConfigDir) == "" {
		return errors.New("qwen: missing config dir")
	}
	if strings.TrimSpace(r.LaunchCWD) == "" || !filepath.IsAbs(r.LaunchCWD) {
		return errors.New("qwen: resume working directory must be absolute")
	}
	source := r.TranscriptPath
	if source == "" {
		path, _, err := p.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: r.ConfigDir})
		if err != nil {
			return err
		}
		source = path
	}
	n := 0
	err := importcopy.CopyOnce(source, qwenChatPath(r.ConfigDir, r.LaunchCWD, r.NativeSessionID), func(line []byte) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if id, ok := importcopy.StringField(line, "sessionId"); ok && id != r.NativeSessionID {
			return nil, errors.New("qwen: native identity mismatch")
		}
		out, err := importcopy.SetStringField(line, "cwd", r.LaunchCWD)
		if err == nil {
			n++
		}
		return out, err
	}, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n == 0 {
			return errors.New("qwen: transcript has no records to relocate")
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("qwen: relocate imported transcript: %w", err)
	}
	return nil
}
