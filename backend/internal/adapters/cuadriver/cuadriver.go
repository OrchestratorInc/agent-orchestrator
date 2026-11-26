// Package cuadriver locates and runs Cua Driver (https://cua.ai), the
// computer-use driver behind AO's computer-use tools. It owns only process concerns: where
// the official installers put the binary on each platform, how to execute it,
// and how its background service is started. What to run lives in
// service/computeruse.
package cuadriver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// binarySpec lists where Cua Driver's official installers put the CLI. PATH
// comes first, but a desktop-launched daemon rarely has ~/.local/bin on it,
// so every installer destination is probed directly as well.
var binarySpec = binaryutil.BinarySpec{
	Label:    "cua-driver",
	Names:    []string{"cua-driver"},
	WinNames: []string{"cua-driver.exe"},
	// macOS installs CuaDriver.app and links the binary inside it.
	UnixPaths: []string{"/Applications/CuaDriver.app/Contents/MacOS/cua-driver"},
	// macOS and Linux link it into ~/.local/bin.
	UnixHomePaths: [][]string{{".local", "bin", "cua-driver"}},
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinLocalAppData, Parts: []string{"Programs", "Cua", "cua-driver", "bin", "cua-driver.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".cua-driver", "packages", "current", "cua-driver.exe"}},
		// Releases before the Cua rename installed here.
		{Base: binaryutil.WinLocalAppData, Parts: []string{"Programs", "trycua", "cua-driver-rs", "bin", "cua-driver.exe"}},
	},
}

// startTimeout bounds the short-lived launcher commands StartDaemon runs.
const startTimeout = 15 * time.Second

// Driver is service/computeruse's Driver, backed by the installed CLI.
type Driver struct{}

// New returns a Driver.
func New() Driver { return Driver{} }

// Locate returns the installed cua-driver executable.
func (Driver) Locate(ctx context.Context) (string, error) {
	path, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if errors.Is(err, ports.ErrAgentBinaryNotFound) {
		return "", ports.ErrComputerUseDriverNotFound
	}
	return path, err
}

// Run executes binary with args, capturing both streams.
func (Driver) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := aoprocess.CommandContext(ctx, binary, args...)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// StartDaemon starts Cua Driver's background service the way its docs
// prescribe for each platform.
//
//   - macOS: through LaunchServices (`open -n -g -a CuaDriver --args serve`),
//     so Accessibility and Screen Recording are attributed to CuaDriver.app
//     rather than to AO.
//   - Windows: `autostart kick` starts the logon task the installer registers;
//     when that task is missing, `serve` is started detached instead.
//   - Linux: `serve` started detached in the desktop session.
func (d Driver) StartDaemon(ctx context.Context, binary string) error {
	switch runtime.GOOS {
	case "darwin":
		return d.runShort(ctx, "/usr/bin/open", "-n", "-g", "-a", "CuaDriver", "--args", "serve")
	case "windows":
		if err := d.runShort(ctx, binary, "autostart", "kick"); err == nil {
			return nil
		}
		// The installer's logon task may run serve elevated; this fallback runs
		// at the AO daemon's integrity level, so Windows (UIPI) blocks it from
		// sending input to elevated windows such as admin consoles. Untested on
		// a clean machine, and the status row does not tell the two apart.
		return startDetached(binary, "serve")
	default:
		return startDetached(binary, "serve")
	}
}

func (d Driver) runShort(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	_, stderr, err := d.Run(ctx, name, args...)
	if err != nil {
		if message := bytes.TrimSpace(stderr); len(message) > 0 {
			return fmt.Errorf("%s: %w: %s", name, err, message)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
