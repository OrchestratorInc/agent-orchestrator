package systemexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	remoteScriptDownloadTimeout  = 30 * time.Second
	remoteScriptMaxBytes         = 4 << 20
	remoteScriptMaxRedirects     = 5
	remoteScriptDownloadAttempts = 3
)

// RunInstallScript downloads a fixed HTTPS installer into private AO-owned
// storage, executes the complete file, and removes it afterward.
func (a Adapter) RunInstallScript(ctx context.Context, command ports.InstallScriptCommand, stdout, stderr io.Writer) (result ports.InstallScriptResult, err error) {
	if a.installerRoot == "" {
		return result, errors.New("installer scratch directory is not configured")
	}
	if len(command.Interpreter) == 0 {
		return result, errors.New("installer interpreter is empty")
	}
	parsed, err := url.Parse(command.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return result, fmt.Errorf("installer URL must be absolute HTTPS: %q", command.URL)
	}

	body, err := a.downloadInstallScript(ctx, command.URL, stdout, stderr)
	if err != nil {
		return result, err
	}

	sum := sha256.Sum256(body)
	result.SHA256 = hex.EncodeToString(sum[:])

	if err := os.MkdirAll(a.installerRoot, 0o700); err != nil {
		return result, fmt.Errorf("create installer scratch directory: %w", err)
	}
	if err := os.Chmod(a.installerRoot, 0o700); err != nil { //nolint:gosec // Private directories need owner traversal.
		return result, fmt.Errorf("secure installer scratch directory: %w", err)
	}
	jobDir, err := os.MkdirTemp(a.installerRoot, "job-*")
	if err != nil {
		return result, fmt.Errorf("create installer job directory: %w", err)
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(jobDir))
	}()
	if err := os.Chmod(jobDir, 0o700); err != nil { //nolint:gosec // Private directories need owner traversal.
		return result, fmt.Errorf("secure installer job directory: %w", err)
	}
	extension := ".sh"
	program := strings.ToLower(filepath.Base(command.Interpreter[0]))
	if strings.Contains(program, "powershell") || strings.Contains(program, "pwsh") {
		extension = ".ps1"
	}
	scriptPath := filepath.Join(jobDir, "installer"+extension)
	if err := os.WriteFile(scriptPath, body, 0o600); err != nil {
		return result, fmt.Errorf("write installer script: %w", err)
	}
	if err := os.Chmod(scriptPath, 0o600); err != nil {
		return result, fmt.Errorf("secure installer script: %w", err)
	}

	argv := append([]string(nil), command.Interpreter...)
	if extension == ".ps1" {
		// Some desktop environments disable PowerShell module autoloading. Both
		// vendor installers and their checksum checks need Get-FileHash, so load
		// its bundled module explicitly before invoking the untouched script.
		launcherPath := filepath.Join(jobDir, "launch.ps1")
		launcher := "Import-Module (Join-Path $PSHOME 'Modules/Microsoft.PowerShell.Utility/Microsoft.PowerShell.Utility.psd1') -ErrorAction Stop\n& (Join-Path $PSScriptRoot 'installer.ps1')" + powerShellArgs(command.Args) + "\nif (-not $?) { if ($LASTEXITCODE) { exit $LASTEXITCODE }; exit 1 }\n"
		if err := os.WriteFile(launcherPath, []byte(launcher), 0o600); err != nil {
			return result, fmt.Errorf("write PowerShell installer launcher: %w", err)
		}
		argv = append(argv, launcherPath)
	} else {
		argv = append(argv, scriptPath)
		argv = append(argv, command.Args...)
	}
	// The job directory is the working directory too, so files an installer
	// writes to relative paths stay private and are removed with it.
	_, _ = fmt.Fprintln(stdout, "[installer] Installing harness.")
	if err = a.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: command.Env, Dir: jobDir, Isolated: command.Isolated}, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "installer execution: %v\n", err)
		return result, fmt.Errorf("installer execution failed; it was not automatically retried. See diagnostics for details: %w", err)
	}
	return result, nil
}

// getHTTPS fetches a fixed daemon-owned HTTPS URL, refusing a plain-HTTP URL
// or redirect and more than remoteScriptMaxRedirects hops. what names the
// download in errors. The caller closes the body of the returned response.
func (a Adapter) getHTTPS(ctx context.Context, rawURL, what string) (*http.Response, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("%s URL must be absolute HTTPS: %q", what, rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", what, err)
	}
	client := *a.httpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > remoteScriptMaxRedirects {
			return fmt.Errorf("%s download exceeded redirect limit", what)
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("%s redirect must remain HTTPS", what)
		}
		return nil
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", what, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, errors.Join(fmt.Errorf("download %s: HTTP %s", what, response.Status), response.Body.Close())
	}
	return response, nil
}

var powerShellParameterName = regexp.MustCompile(`^-[A-Za-z][A-Za-z0-9]*$`)

// powerShellArgs renders fixed installer arguments into the launcher source:
// parameter names stay bare so they bind as switches, and every other value is
// a single-quoted literal so nothing in it is evaluated.
func powerShellArgs(args []string) string {
	var b strings.Builder
	for _, arg := range args {
		b.WriteByte(' ')
		if powerShellParameterName.MatchString(arg) {
			b.WriteString(arg)
			continue
		}
		b.WriteString("'" + strings.ReplaceAll(arg, "'", "''") + "'")
	}
	return b.String()
}

// Only this download loop retries. Script staging and execution remain outside it.
func (a Adapter) downloadInstallScript(ctx context.Context, source string, stdout, stderr io.Writer) ([]byte, error) {
	client := *a.httpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > remoteScriptMaxRedirects {
			return errors.New("installer download exceeded redirect limit")
		}
		if req.URL.Scheme != "https" {
			return errors.New("installer redirect must remain HTTPS")
		}
		return nil
	}
	for attempt := 1; attempt <= remoteScriptDownloadAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, _ = fmt.Fprintf(stdout, "[installer] Downloading installer (attempt %d/%d).\n", attempt, remoteScriptDownloadAttempts)
		started := time.Now()
		body, retry, err := downloadInstallScriptAttempt(ctx, &client, source)
		if err == nil {
			return body, nil
		}
		_, _ = fmt.Fprintf(stderr, "download attempt %d/%d elapsed=%s source=%s: %v\n", attempt, remoteScriptDownloadAttempts, time.Since(started).Round(time.Millisecond), source, err)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !retry || attempt == remoteScriptDownloadAttempts {
			return nil, &installerDownloadError{attempts: attempt, cause: err}
		}
		delay := time.Second << (attempt - 1)
		_, _ = fmt.Fprintf(stdout, "[installer] Retrying download in %s (attempt %d/%d).\n", delay, attempt+1, remoteScriptDownloadAttempts)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable: installer download attempts exhausted")
}

// installerDownloadError keeps technical causes available to errors.Is/As while
// the UI receives a concise failure summary. Attempt details go to stderr.
type installerDownloadError struct {
	attempts int
	cause    error
}

func (e *installerDownloadError) Error() string {
	return fmt.Sprintf("Installer download failed after %d attempt(s). The installer was not run. See diagnostics for details before trying again.", e.attempts)
}

func (e *installerDownloadError) Unwrap() error { return e.cause }

func downloadInstallScriptAttempt(ctx context.Context, client *http.Client, source string) ([]byte, bool, error) {
	downloadCtx, cancel := context.WithTimeout(ctx, remoteScriptDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, source, http.NoBody)
	if err != nil {
		return nil, false, fmt.Errorf("create installer request: %w", err)
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, retryableDownloadError(err), fmt.Errorf("download installer: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		retry := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode == http.StatusInternalServerError || response.StatusCode == http.StatusBadGateway ||
			response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusGatewayTimeout
		return nil, retry, errors.Join(fmt.Errorf("download installer: HTTP %s", response.Status), response.Body.Close())
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, remoteScriptMaxBytes+1))
	closeErr := response.Body.Close()
	if err != nil {
		return nil, retryableDownloadError(err), errors.Join(fmt.Errorf("read installer: %w", err), closeErr)
	}
	if closeErr != nil {
		return nil, retryableDownloadError(closeErr), fmt.Errorf("close installer response: %w", closeErr)
	}
	if len(body) > remoteScriptMaxBytes {
		return nil, false, fmt.Errorf("installer exceeds %d-byte download limit", remoteScriptMaxBytes)
	}
	return body, false, nil
}

func retryableDownloadError(err error) bool {
	var networkError net.Error
	return (errors.As(err, &networkError) && networkError.Timeout()) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE)
}
