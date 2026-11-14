package kimi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	kimiTrustDirName       = "workspace-trust"
	kimiWorkdirKeyPrefix   = "wd_"
	kimiWorkdirHashLength  = 12
	kimiWorkdirSlugMaxLen  = 40
	kimiWorkdirSlugDefault = "workspace"
)

var kimiWorkdirSlugInvalidRE = regexp.MustCompile(`[^a-z0-9._-]+`)

func (p *Plugin) PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" || strings.TrimSpace(cfg.DataDir) == "" {
		return nil
	}
	return ensureKimiWorkspaceTrusted(kimiCodeHomeDir(cfg.DataDir), cfg.WorkspacePath)
}

func EnsureWorkspaceTrusted(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	home, ok := kimiCodeHome()
	if !ok {
		return nil
	}
	return ensureKimiWorkspaceTrusted(home, workspacePath)
}

type kimiWorkspaceTrust struct {
	Root      string `json:"root"`
	TrustedAt int64  `json:"trustedAt"`
}

func ensureKimiWorkspaceTrusted(kimiHome, workspacePath string) error {
	absWorkspace, err := filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("kimi: resolve workspace: %w", err)
	}
	trustPath := filepath.Join(kimiHome, kimiTrustDirName, kimiWorkdirKey(absWorkspace))
	if _, err := os.Stat(trustPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("kimi: stat %s: %w", trustPath, err)
	}

	trust := kimiWorkspaceTrust{Root: absWorkspace, TrustedAt: time.Now().UnixMilli()}
	data, err := json.Marshal(trust)
	if err != nil {
		return fmt.Errorf("kimi: encode trust record: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(trustPath), 0o750); err != nil {
		return fmt.Errorf("kimi: create trust dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(trustPath, data, 0o600); err != nil {
		return fmt.Errorf("kimi: write %s: %w", trustPath, err)
	}
	return nil
}

func kimiWorkdirKey(workspacePath string) string {
	normalized := strings.TrimRight(strings.ReplaceAll(workspacePath, `\`, "/"), "/")
	base := normalized
	if idx := strings.LastIndex(normalized, "/"); idx >= 0 {
		base = normalized[idx+1:]
	}
	sum := sha256.Sum256([]byte(normalized))
	return kimiWorkdirKeyPrefix + kimiWorkdirSlug(base) + "_" + hex.EncodeToString(sum[:])[:kimiWorkdirHashLength]
}

func kimiWorkdirSlug(name string) string {
	slug := kimiWorkdirSlugInvalidRE.ReplaceAllString(strings.ToLower(name), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > kimiWorkdirSlugMaxLen {
		slug = strings.Trim(slug[:kimiWorkdirSlugMaxLen], "-")
	}
	if slug == "" || slug == "." || slug == ".." {
		return kimiWorkdirSlugDefault
	}
	return slug
}
