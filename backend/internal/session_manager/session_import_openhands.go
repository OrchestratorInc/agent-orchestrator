package sessionmanager

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/process"
)

var openHandsPythonName = regexp.MustCompile(`^pythonw?(?:\d+(?:\.\d+)*)?(?:\.exe)?$`)

// This supplementary probe detects a state descriptor held open. It cannot
// detect an idle original CLI; the S2 preparer isolates the selected history
// into an AO-owned native store before launch to avoid sharing that writer.
func openHandsImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	return writableTranscriptOwnerMatching(ctx, rec.Metadata.NativeTranscriptPath, func(ctx context.Context, pid int, command string) bool {
		if command == "openhands" || command == "openhands.exe" {
			return true
		}
		if pid <= 0 || !openHandsPythonName.MatchString(command) {
			return false
		}
		out, err := process.CommandContext(ctx, "ps", "-ww", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
		return err == nil && openHandsProcessCommand(string(out))
	})
}

// Python alone is not OpenHands evidence. Match its module or console script
// in the entrypoint position, never a string in another program's arguments.
func openHandsProcessCommand(command string) bool {
	args := strings.Fields(command)
	if len(args) < 2 || !openHandsPythonName.MatchString(strings.ToLower(filepath.Base(args[0]))) {
		return false
	}
	for i := 1; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "-m":
			if i+1 >= len(args) {
				return false
			}
			module := args[i+1]
			return module == "openhands_cli" || module == "openhands_cli.entrypoint" || module == "openhands_cli.__main__"
		case "-u", "-B", "-E", "-I", "-s", "-S", "-O", "-OO":
			continue
		default:
			name := filepath.Base(arg)
			return name == "openhands" || name == "openhands.exe" ||
				(filepath.Base(filepath.Dir(arg)) == "openhands_cli" && (name == "__main__.py" || name == "entrypoint.py"))
		}
	}
	return false
}
