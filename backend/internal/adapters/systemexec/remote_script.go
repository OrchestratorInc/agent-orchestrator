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
	argv = append(argv, scriptPath)
	_, _ = fmt.Fprintln(stdout, "[installer] Running installer.")
	if err = a.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: command.Env}, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "installer execution: %v\n", err)
		return result, fmt.Errorf("installer execution failed; it was not automatically retried. See diagnostics for details: %w", err)
	}
	return result, nil
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
