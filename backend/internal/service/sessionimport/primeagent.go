package sessionimport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func primeAgentRoot(home string) string {
	expand := func(path string) string {
		if path == "~" {
			return home
		}
		if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
			return filepath.Join(home, path[2:])
		}
		return path
	}
	root, set := os.LookupEnv("PRIME_AGENT_SESSION_DIR")
	if !set {
		root = os.Getenv("PRIME_AGENT_CODING_AGENT_SESSION_DIR")
	}
	if root != "" {
		return expand(root)
	}
	if root := os.Getenv("PRIME_AGENT_CODING_AGENT_DIR"); root != "" {
		return filepath.Join(expand(root), "sessions")
	}
	return filepath.Join(home, ".prime", "agent", "sessions")
}

// Prime's native JSONL header/message ancestry shares the Pi format. The
// provider, not this display reader, performs the actual first-open fork.
func readPrimeAgentTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	if len(paths) != 1 {
		return transcript{}, errors.New("prime native history requires one file")
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return transcript{}, err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(root, paths[0])
	if err != nil || !filepath.IsLocal(rel) {
		return transcript{}, errors.New("prime native history is outside its state root")
	}
	info, err := provider.Stat(rel)
	if err != nil {
		return transcript{}, err
	}
	if !info.Mode().IsRegular() {
		return transcript{}, errors.New("prime native history is not a regular file")
	}
	file, err := provider.Open(rel)
	if err != nil {
		return transcript{}, err
	}
	defer func() { _ = file.Close() }()
	line, err := bufio.NewReader(io.LimitReader(file, 1<<20)).ReadBytes('\n')
	if err != nil {
		return transcript{}, errors.New("prime native history header is incomplete")
	}
	var header struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
	}
	if json.Unmarshal(line, &header) != nil || header.Type != "session" || header.Version != 3 {
		return transcript{}, errors.New("prime native history requires version 3")
	}
	t, err := readPiTranscript(ctx, root, paths...)
	t.harness = domain.HarnessPrimeAgent
	return t, err
}

func discoverPrimeAgent(ctx context.Context, root string) ([]sourceFiles, error) {
	var files []sourceFiles
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil || !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil //nolint:nilerr // Unreadable native histories are ineligible.
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // A concurrently removed source is ineligible.
		}
		files = append(files, sourceFiles{harness: domain.HarnessPrimeAgent, root: root, key: piNativeID(path), paths: []string{path}, modTime: info.ModTime(), size: info.Size()})
		return nil
	})
	return files, err
}
