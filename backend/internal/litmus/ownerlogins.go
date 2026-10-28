package litmus

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const ownerLoginsFile = "owner-logins.json"

type ownerLogins struct {
	SHA256 string
	MTime  time.Time
	Claude bool
}

func readOwnerLogins(ctx context.Context) ownerLogins {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".codex", "auth.json")
	var login ownerLogins
	if data, err := os.ReadFile(path); err == nil {
		login.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	} else {
		login.SHA256 = err.Error()
	}
	if st, err := os.Stat(path); err == nil {
		login.MTime = st.ModTime()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Run discards stdout and stderr. Only the Keychain item's presence is kept.
	login.Claude = process.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials").Run() == nil
	return login
}

func (s *Service) ownerLoginStatus(ctx context.Context, run Run) string {
	var before ownerLogins
	if err := s.store.read(run.ID, ownerLoginsFile, &before); err != nil {
		if os.IsNotExist(err) {
			return "" // older runs did not record a baseline
		}
		return "owner logins: unverified"
	}
	after := readOwnerLogins(ctx)
	var changed []string
	if before.SHA256 != after.SHA256 || !before.MTime.Equal(after.MTime) {
		changed = append(changed, "~/.codex/auth.json")
	}
	if before.Claude != after.Claude {
		changed = append(changed, "Claude Code-credentials")
	}
	if len(changed) > 0 {
		return "CHANGED: " + strings.Join(changed, ", ")
	}
	return "owner logins: unchanged"
}
