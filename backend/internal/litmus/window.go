package litmus

import (
	_ "embed"
	"os"
	"path/filepath"
	"strconv"
)

// The window label the unpackaged Electron under test loads through NODE_OPTIONS=--require: it names the window
// after the build it shows and sets a Litmus Dock icon. Written into the leg dir at launch, so it applies to any
// checkout without touching the code under test.
var (
	//go:embed assets/litmus-window.cjs
	windowLabelScript []byte
	//go:embed assets/litmus-icon.png
	windowLabelIcon []byte
)

const (
	windowLabelScriptName = "litmus-window.cjs"
	windowLabelIconName   = "litmus-icon.png"
)

func (r Run) windowTitle(leg string) string {
	var number string
	if r.Number() > 0 {
		number = " #" + strconv.Itoa(r.Number())
	}
	if r.Kind == "issue" {
		return "Litmus: " + r.LegName(leg) + " build for " + r.kindNoun() + number
	}
	if leg == "base" {
		title := "Litmus: " + r.LegName(leg)
		if number != "" {
			title += " for" + number
		}
		return title
	}
	return "Litmus: " + r.LegName(leg) + " build" + number
}

// writeWindowLabel puts the label script and icon into legDir, where legEnv points NODE_OPTIONS and LITMUS_ICON.
func writeWindowLabel(legDir string) error {
	if err := os.WriteFile(filepath.Join(legDir, windowLabelScriptName), windowLabelScript, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(legDir, windowLabelIconName), windowLabelIcon, 0o600)
}
