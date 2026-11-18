package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
)

// ompConfigDirEnv overrides OMP's state root. OMP is a Pi fork and reuses Pi's
// variable name; it defaults to ~/.omp/agent.
const ompConfigDirEnv = "PI_CODING_AGENT_DIR"

// OMP is a Pi-fork, terminal-first coding harness. It stores one conversation
// per file at <root>/sessions/-<cwd-dashed>/<ISO-time>_<uuid>.jsonl, where the
// trailing UUID is the native session id (also recorded in the session header).
// Its ACP session/load finds conversations by cwd; the OMP adapter copies the
// transcript for AO's launch directory before first resume, keeping the native
// id and leaving the original file untouched.

// ompRoot resolves OMP's session-state root the way an OMP launch does:
// PI_CODING_AGENT_DIR when set, otherwise ~/.omp/agent.
func ompRoot(home string) string {
	if dir := strings.TrimSpace(os.Getenv(ompConfigDirEnv)); dir != "" {
		return dir
	}
	return filepath.Join(home, ".omp", "agent")
}
