package litmus

import (
	_ "embed"
	"os"
	"path/filepath"
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

// writeWindowLabel puts the label script and icon into legDir, where legEnv points NODE_OPTIONS and LITMUS_ICON.
func writeWindowLabel(legDir string) error {
	if err := os.WriteFile(filepath.Join(legDir, windowLabelScriptName), windowLabelScript, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(legDir, windowLabelIconName), windowLabelIcon, 0o600)
}
