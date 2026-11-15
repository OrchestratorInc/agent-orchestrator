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

var binarySpec = binaryutil.BinarySpec{
	Label:         "cua-driver",
	Names:         []string{"cua-driver"},
	WinNames:      []string{"cua-driver.exe"},
	UnixPaths:     []string{"/Applications/CuaDriver.app/Contents/MacOS/cua-driver"},
	UnixHomePaths: [][]string{{".local", "bin", "cua-driver"}},
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinLocalAppData, Parts: []string{"Programs", "Cua", "cua-driver", "bin", "cua-driver.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".cua-driver", "packages", "current", "cua-driver.exe"}},
		{Base: binaryutil.WinLocalAppData, Parts: []string{"Programs", "trycua", "cua-driver-rs", "bin", "cua-driver.exe"}},
	},
}

const startTimeout = 15 * time.Second

type Driver struct{}

func New() Driver { return Driver{} }

func (Driver) Locate(ctx context.Context) (string, error) {
	path, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if errors.Is(err, ports.ErrAgentBinaryNotFound) {
		return "", ports.ErrComputerUseDriverNotFound
	}
	return path, err
}

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

func (d Driver) StartDaemon(ctx context.Context, binary string) error {
	switch runtime.GOOS {
	case "darwin":
		return d.runShort(ctx, "/usr/bin/open", "-n", "-g", "-a", "CuaDriver", "--args", "serve")
	case "windows":
		if err := d.runShort(ctx, binary, "autostart", "kick"); err == nil {
			return nil
		}
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
