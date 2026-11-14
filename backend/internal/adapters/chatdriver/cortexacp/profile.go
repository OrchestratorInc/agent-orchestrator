package cortexacp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxProfileEntries = 4096

// validateProfile checks metadata only, without reading credentials or changing
// files. Candidate cold launches admit only small, deliberately isolated profiles
// consisting of directories and regular files. This is an admission policy, not
// a native format restriction or an adversarial filesystem sandbox: hardlinks
// and concurrent mutations by the profile owner require trusted preparation.
// In particular, validating HOME alone misses linked logs/settings/state below
// .snowflake, which native startup may write even before authentication.
// Never run this scan when adopting a live host with unsettled native state.
func validateProfile(ctx context.Context, home string) error {
	fail := func() error {
		return fmt.Errorf("%w: Cortex cold launch requires bounded isolated profile directories and regular files without links", ports.ErrChatDriverIncompatible)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return fail()
	}
	defer root.Close()
	directories := []string{"."}
	count := 0
	for len(directories) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := directories[len(directories)-1]
		directories = directories[:len(directories)-1]
		dir, err := root.Open(path)
		if err != nil {
			return fail()
		}
		entries, readErr := dir.ReadDir(maxProfileEntries - count + 1)
		closeErr := dir.Close()
		if readErr != nil && readErr != io.EOF || closeErr != nil {
			return fail()
		}
		count += len(entries)
		if count > maxProfileEntries {
			return fail()
		}
		for _, entry := range entries {
			if entry.IsDir() {
				directories = append(directories, filepath.Join(path, entry.Name()))
			} else if !entry.Type().IsRegular() {
				return fail()
			}
		}
	}
	return ctx.Err()
}
