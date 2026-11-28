package agy

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// cliVersionTimeout bounds `agy --version`, which answers in well under a
// second.
const cliVersionTimeout = 10 * time.Second

type cliVersionKey struct {
	path    string
	size    int64
	modTime time.Time
}

// cliVersions caches `agy --version` per binary, so a launch does not run it
// again until the CLI is replaced or updated.
var cliVersions sync.Map // cliVersionKey -> string

// CLIVersion reports the version the agy CLI at path prints.
func CLIVersion(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	key := cliVersionKey{path: path, size: info.Size(), modTime: info.ModTime()}
	if cached, ok := cliVersions.Load(key); ok {
		if version, ok := cached.(string); ok {
			return version, nil
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, cliVersionTimeout)
	defer cancel()
	out, err := aoprocess.CommandContext(probeCtx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("agy --version: %w", err)
	}
	version := strings.TrimSpace(string(out))
	cliVersions.Store(key, version)
	return version, nil
}

var agentsSkillsVersion = [3]int{1, 5, 16}

var versionNumbers = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// readsAgentsSkills reports whether this agy loads ~/.agents/skills itself,
// which 1.5.16 added.
func readsAgentsSkills(version string) bool {
	match := versionNumbers.FindStringSubmatch(version)
	if match == nil {
		return false
	}
	for i, want := range agentsSkillsVersion {
		got, err := strconv.Atoi(match[i+1])
		if err != nil {
			return false
		}
		if got != want {
			return got > want
		}
	}
	return true
}
