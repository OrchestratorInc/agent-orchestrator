package sessionimport

import (
	"os"
	"path/filepath"
	"strings"
)

const ompConfigDirEnv = "PI_CODING_AGENT_DIR"

func ompRoot(home string) string {
	if dir := strings.TrimSpace(os.Getenv(ompConfigDirEnv)); dir != "" {
		return dir
	}
	return filepath.Join(home, ".omp", "agent")
}
