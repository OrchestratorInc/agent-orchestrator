package systemexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type Adapter struct {
	installerRoot string
	binDir        string
	httpClient    *http.Client
	downloadStall time.Duration
}

var (
	_ ports.ExecutableFinder       = Adapter{}
	_ ports.CommandRunner          = Adapter{}
	_ ports.InstallCommandRunner   = Adapter{}
	_ ports.InstallScriptRunner    = Adapter{}
	_ ports.InstallCapabilityProbe = Adapter{}
	_ ports.PathWritableProbe      = Adapter{}
	_ ports.ManagedBinaryInstaller = Adapter{}
)

func New(dataDir string) Adapter {
	return newAdapter(dataDir, http.DefaultClient)
}

func (Adapter) PathWritable(ctx context.Context, path string) (bool, error) {
	return pathWritable(ctx, path)
}

func newAdapter(dataDir string, client *http.Client) Adapter {
	return Adapter{
		installerRoot: filepath.Join(dataDir, "installers", "tmp"),
		binDir:        filepath.Join(dataDir, "bin"),
		httpClient:    client,
	}
}

func (Adapter) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (Adapter) RunInstall(ctx context.Context, command ports.InstallCommand, stdout, stderr io.Writer) error {
	cmd, err := commandContext(ctx, command.Argv[0], command.Argv[1:]...)
	if err != nil {
		return err
	}
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Dir = command.Dir
	env := os.Environ()
	if command.Isolated {
		env = installerSafeEnv(env)
	}
	env = append(env, command.Env...)
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		return err
	}
	refreshExecutablePath()
	return nil
}

func installerSafeEnv(environ []string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if upper := strings.ToUpper(name); strings.HasPrefix(upper, "LC_") || installerSafeVars[upper] {
			kept = append(kept, entry)
		}
	}
	return kept
}

var installerSafeVars = map[string]bool{
	"TMPDIR": true, "TMP": true, "TEMP": true, "LANG": true, "LANGUAGE": true, "TZ": true,
	"USER": true, "LOGNAME": true, "SHELL": true, "TERM": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "CURL_CA_BUNDLE": true, "REQUESTS_CA_BUNDLE": true, "NODE_EXTRA_CA_CERTS": true,
}

func (Adapter) Probe(ctx context.Context) (ports.InstallCapabilities, error) {
	var snapshot ports.InstallCapabilities

	nodeVersion, nodeErr := capabilityOutput(ctx, "node", "--version")
	npmVersion, npmVersionErr := capabilityOutput(ctx, "npm", "--version")
	npmPrefix, npmPrefixErr := capabilityOutput(ctx, "npm", "prefix", "-g")
	if err := ctx.Err(); err != nil {
		return ports.InstallCapabilities{}, err
	}
	snapshot.NPM = ports.NPMInstallCapabilities{
		NodeVersion: nodeVersion, NPMVersion: npmVersion, GlobalPrefix: npmPrefix,
	}
	snapshot.NPM.RuntimeErr = errors.Join(nodeErr, npmVersionErr)
	snapshot.NPM.Err = errors.Join(snapshot.NPM.RuntimeErr, npmPrefixErr)
	if snapshot.NPM.Err == nil {
		writable, err := pathWritable(ctx, npmPrefix)
		if err != nil {
			return ports.InstallCapabilities{}, err
		}
		snapshot.NPM.PrefixWritable = writable
	}

	brewPrefix, brewPrefixErr := capabilityOutput(ctx, "brew", "--prefix")
	formulae, formulaErr := capabilityLines(ctx, "brew", "list", "--formula", "-1")
	casks, caskErr := capabilityLines(ctx, "brew", "list", "--cask", "-1")
	if err := ctx.Err(); err != nil {
		return ports.InstallCapabilities{}, err
	}
	snapshot.Homebrew = ports.HomebrewInstallCapabilities{
		Prefix: brewPrefix, Formulae: formulae, Casks: casks,
		Err: errors.Join(brewPrefixErr, formulaErr, caskErr),
	}
	if snapshot.Homebrew.Err == nil {
		writable, err := pathWritable(ctx, brewPrefix)
		if err != nil {
			return ports.InstallCapabilities{}, err
		}
		snapshot.Homebrew.PrefixWritable = writable
	}
	return snapshot, nil
}

func capabilityOutput(parent context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	cmd, err := commandContext(ctx, name, args...)
	if err != nil {
		return "", err
	}
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func capabilityLines(ctx context.Context, name string, args ...string) (map[string]bool, error) {
	out, err := capabilityOutput(ctx, name, args...)
	if err != nil {
		return nil, err
	}
	items := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		if item := strings.TrimSpace(line); item != "" {
			items[item] = true
		}
	}
	return items, nil
}

func pathWritable(ctx context.Context, path string) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if _, err := os.Stat(path); err == nil {
			file, createErr := os.CreateTemp(path, ".ao-write-check-*")
			if createErr != nil {
				return false, nil //nolint:nilerr // inability to create the probe file means not writable.
			}
			name := file.Name()
			closeErr := file.Close()
			removeErr := os.Remove(name)
			return closeErr == nil && removeErr == nil, nil
		} else if !os.IsNotExist(err) {
			return false, nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false, nil
		}
		path = parent
	}
}

func (Adapter) Run(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
	cmd, err := commandContext(ctx, argv[0], argv[1:]...)
	if err != nil {
		return err
	}
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
