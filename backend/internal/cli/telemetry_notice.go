package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// telemetryNoticeFile marks that this install's CLI has shown the first-run
// telemetry notice. It lives beside the other telemetry markers in the data dir.
const telemetryNoticeFile = "telemetry_cli_notice_shown"

// telemetryOptOutFile is the Share usage analytics opt-out marker the desktop
// writes and the daemon reads (adapters/telemetry.OptOutFile).
const telemetryOptOutFile = "telemetry_opt_out"

const telemetryDocsURL = "https://github.com/Untrivial-ai/agent-orchestrator/blob/main/docs/telemetry.md"

const telemetryNoticeText = `Orchestrator collects anonymous usage and error telemetry when its daemon runs from the
desktop app: command names, app version, OS, error codes and a random install ID. It never
sends your code, prompts, agent output or file paths, and sends your GitHub username or
Cloud account ID only if you turn that on. See a record of what was sent with
` + "`ao telemetry log`" + `; opt out with ` + "`ao telemetry off`" + `, in Settings > General > Privacy,
or with DO_NOT_TRACK=1. Details: ` + telemetryDocsURL + "\n"

// maybeShowTelemetryNotice prints the notice once per install, to stderr, for
// a person at a terminal. Agent sessions, piped output, DO_NOT_TRACK and an
// install that already opted out never see it. The marker is written before
// printing so a failure to record it never repeats the notice on every run.
func maybeShowTelemetryNotice(errOut io.Writer, getenv func(string) string, dataDir string, interactive bool) {
	if !interactive || dataDir == "" || config.DoNotTrack(getenv) {
		return
	}
	if strings.TrimSpace(getenv("AO_SESSION_ID")) != "" {
		return
	}
	if _, err := os.Stat(filepath.Join(dataDir, telemetryOptOutFile)); err == nil {
		return
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dataDir, telemetryNoticeFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		// Already shown, or unwritable: stay quiet either way.
		return
	}
	_ = file.Close()
	_, _ = fmt.Fprint(errOut, telemetryNoticeText)
}

// stderrIsTerminal reports whether w is a terminal the user is looking at.
func stderrIsTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

// telemetryNoticeDataDir resolves the data dir without failing the command.
func telemetryNoticeDataDir() string {
	_, dataDir, _, err := config.ResolvePaths()
	if err != nil {
		return ""
	}
	return dataDir
}
