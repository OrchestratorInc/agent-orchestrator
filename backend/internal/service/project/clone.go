package project

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var scpStyleGitURL = regexp.MustCompile(`^[^/@:\s]+@[^/:\s]+:(.+)$`)

var allowedCloneSchemes = map[string]struct{}{
	"file":  {},
	"git":   {},
	"http":  {},
	"https": {},
	"ssh":   {},
}

const clonePreparationMarker = ".ao-clone-prepared"

// normalizeCloneURL only rewrites public GitHub repository roots. Other Git
// transports and self-hosted URL shapes retain their original meaning.
func normalizeCloneURL(raw string) string {
	value := strings.TrimSpace(raw)
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Host != "github.com" || (u.Scheme != "https" && u.Scheme != "http") {
		return value
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || u.RawQuery != "" {
		return value
	}
	u.Path = "/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git") + ".git"
	u.RawPath = ""
	u.Fragment = ""
	return u.String()
}

// CheckCloneDestination inspects local clone readiness without contacting the remote.
func (m *Service) CheckCloneDestination(ctx context.Context, in CloneInput) (CloneDestination, error) {
	remoteURL := normalizeCloneURL(in.RemoteURL)
	name, err := cloneRepositoryName(remoteURL)
	if err != nil {
		return CloneDestination{}, err
	}
	parent, err := normalizePath(in.DestinationParent)
	if err != nil {
		return CloneDestination{}, err
	}
	target := filepath.Join(parent, name)
	if err := validateRepositorySetupPathSafety(target); err != nil {
		return CloneDestination{}, err
	}
	result := CloneDestination{RemoteURL: remoteURL, Path: target}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, apierr.Invalid("CLONE_DESTINATION_UNAVAILABLE", "AO could not inspect this destination. Choose another folder.", nil)
	}
	result.Exists = true
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, nil
	}
	// Only offer reuse for a repository root with the requested origin. This
	// does not change branches, fetch, reset, or remove any local files.
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		return result, nil //nolint:nilerr // Unusable Git metadata leaves a normal destination conflict.
	}
	origin, err := aoprocess.CommandContext(ctx, "git", "-C", target, "config", "--local", "--get", "remote.origin.url").Output()
	if err != nil || normalizeCloneURL(string(origin)) != remoteURL {
		return result, nil //nolint:nilerr // Reuse is optional and requires a readable, matching origin.
	}
	result.CanUseExisting = true
	return result, nil
}

// Clone checks out one remote repository into a user-selected parent folder,
// then registers it through the same Add boundary as an existing local repo.
// The checkout is staged in a unique sibling directory so a failed or cancelled
// clone can never leave a half-populated destination behind.
func (m *Service) Clone(ctx context.Context, in CloneInput) (Project, error) {
	prepared, err := m.prepareClone(ctx, in)
	if err != nil {
		return Project{}, err
	}
	if !repoHasCommit(ctx, prepared.Path) {
		m.cleanupPreparedCloneAfterFailure(ctx, prepared)
		return Project{}, apierr.Invalid("CLONE_EMPTY_REPOSITORY", "AO needs a repository with at least one commit.", nil)
	}

	project, err := m.Add(ctx, AddInput{
		Path:               prepared.Path,
		ProjectID:          in.ProjectID,
		Name:               in.Name,
		Config:             in.Config,
		ClonePreparationID: prepared.PreparationID,
	})
	if err != nil {
		m.cleanupPreparedCloneAfterFailure(ctx, prepared)
		return Project{}, err
	}
	return project, nil
}

// PrepareClone checks out a repository without registering it as a project.
func (m *Service) PrepareClone(ctx context.Context, in CloneInput) (ClonePreparationResult, error) {
	return m.prepareClone(ctx, in)
}

// CleanupPreparedClone removes an unregistered checkout created by PrepareClone,
// or releases AO's cleanup ownership when the user chooses to keep it.
func (m *Service) CleanupPreparedClone(ctx context.Context, in ClonePreparationCleanupInput) error {
	path, err := normalizePath(in.Path)
	if err != nil {
		return err
	}
	if err := validateRepositorySetupPathSafety(path); err != nil {
		return err
	}
	m.addMu.Lock()
	defer m.addMu.Unlock()

	markerID, exists, err := readClonePreparationID(path)
	if !exists && err == nil {
		return nil
	} else if err != nil {
		return apierr.Invalid("CLONE_CLEANUP_FAILED", "The prepared clone could not be inspected.", map[string]any{"path": path})
	}
	if !sameClonePreparationID(markerID, in.PreparationID) {
		return apierr.Conflict("CLONE_PREPARATION_MISMATCH", "This checkout belongs to a different clone preparation.", map[string]any{"path": path})
	}
	if _, registered, err := m.store.FindProjectByPath(ctx, path); err != nil {
		return apierr.Invalid("CLONE_CLEANUP_FAILED", "The prepared clone registration state could not be inspected.", map[string]any{"path": path})
	} else if registered {
		removeClonePreparationMarker(path)
		return nil
	}
	if in.KeepCheckout {
		if err := os.Remove(filepath.Join(path, ".git", clonePreparationMarker)); err != nil {
			return apierr.Invalid("CLONE_CLEANUP_FAILED", "AO could not release this checkout. Retry before leaving this flow.", nil)
		}
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return apierr.Invalid("CLONE_CLEANUP_FAILED", "The abandoned prepared clone could not be removed.", map[string]any{"path": path})
	}
	return nil
}

func (m *Service) cleanupPreparedCloneAfterFailure(ctx context.Context, prepared ClonePreparationResult) {
	if err := m.CleanupPreparedClone(context.WithoutCancel(ctx), ClonePreparationCleanupInput{
		Path: prepared.Path, PreparationID: prepared.PreparationID,
	}); err != nil {
		m.logger.Warn("project: failed to clean up prepared clone", "path", prepared.Path, "error", err)
	}
}

func (m *Service) prepareClone(ctx context.Context, in CloneInput) (ClonePreparationResult, error) {
	remoteURL := normalizeCloneURL(in.RemoteURL)
	repositoryName, err := cloneRepositoryName(remoteURL)
	if err != nil {
		return ClonePreparationResult{}, err
	}
	parent, err := normalizePath(in.DestinationParent)
	if err != nil {
		return ClonePreparationResult{}, err
	}
	if in.Config != nil {
		if err := in.Config.Validate(); err != nil {
			return ClonePreparationResult{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
		}
	}
	if in.ProjectID != nil {
		if err := validateProjectID(domain.ProjectID(strings.TrimSpace(*in.ProjectID))); err != nil {
			return ClonePreparationResult{}, err
		}
	}

	target := filepath.Join(parent, repositoryName)
	if err := validateRepositorySetupPathSafety(target); err != nil {
		return ClonePreparationResult{}, err
	}
	if _, err := os.Lstat(target); err == nil {
		// A stale marker cannot grant a new request cleanup ownership. The
		// original caller keeps its token; later flows must explicitly reuse.
		return ClonePreparationResult{}, apierr.Conflict("CLONE_DESTINATION_EXISTS", "A folder with this repository name already exists in the selected destination.", map[string]any{"path": target})
	} else if !errors.Is(err, os.ErrNotExist) {
		return ClonePreparationResult{}, apierr.Invalid("CLONE_DESTINATION_UNAVAILABLE", "The clone destination could not be inspected.", map[string]any{"path": target})
	}

	if err := os.MkdirAll(parent, 0o750); err != nil {
		return ClonePreparationResult{}, apierr.Invalid("CLONE_DESTINATION_UNAVAILABLE", "AO could not create the destination folder.", nil)
	}
	temporaryPath, err := os.MkdirTemp(parent, ".ao-clone-")
	if err != nil {
		return ClonePreparationResult{}, apierr.Invalid("CLONE_DESTINATION_UNAVAILABLE", "AO could not prepare the selected clone destination.", nil)
	}
	cleanupPath := temporaryPath
	defer func() {
		if cleanupPath != "" {
			_ = os.RemoveAll(cleanupPath)
		}
	}()

	for attempt := 0; ; attempt++ {
		cmd := aoprocess.CommandContext(ctx, "git", "clone", "--origin", "origin", "--", remoteURL, temporaryPath)
		aoprocess.ConfigureTreeCancellation(cmd)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "LC_ALL=C")
		var stderr limitedCloneOutput
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			break
		}
		var executableError *exec.Error
		if errors.As(err, &executableError) {
			return ClonePreparationResult{}, apierr.Invalid("GIT_NOT_FOUND", "Git is required to clone a repository.", nil)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ClonePreparationResult{}, apierr.Invalid("GIT_CLONE_TIMED_OUT", "Cloning this repository took too long and was stopped.", nil)
		}
		if ctx.Err() != nil {
			return ClonePreparationResult{}, apierr.Invalid("GIT_CLONE_CANCELLED", "Repository cloning was cancelled.", nil)
		}
		if transientCloneFailure(stderr.String()) {
			if attempt == 2 {
				return ClonePreparationResult{}, apierr.Unavailable("GIT_CLONE_NETWORK_FAILED", "The connection failed after three attempts. Check your connection, then retry. Your URL and destination have been kept.")
			}
			// Only discard this attempt's private staging folder, never target.
			if err := os.RemoveAll(temporaryPath); err != nil {
				return ClonePreparationResult{}, apierr.Invalid("CLONE_DESTINATION_UNAVAILABLE", "AO could not reset the temporary clone. Choose another destination.", nil)
			}
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ClonePreparationResult{}, apierr.Invalid("GIT_CLONE_CANCELLED", "Repository cloning was cancelled.", nil)
			case <-timer.C:
			}
			continue
		}
		return ClonePreparationResult{}, apierr.Invalid("GIT_CLONE_FAILED", "Could not clone this repository. Check the URL, your Git credentials, and your network connection.", nil)
	}
	if ctx.Err() != nil {
		return ClonePreparationResult{}, apierr.Invalid("GIT_CLONE_CANCELLED", "Repository cloning was cancelled.", nil)
	}
	preparationID, err := newClonePreparationID()
	if err != nil {
		return ClonePreparationResult{}, apierr.Internal("CLONE_PREPARATION_FAILED", "AO could not identify the prepared clone")
	}
	if err := os.WriteFile(filepath.Join(temporaryPath, ".git", clonePreparationMarker), []byte(preparationID+"\n"), 0o600); err != nil {
		return ClonePreparationResult{}, apierr.Invalid("CLONE_PREPARATION_FAILED", "AO could not mark the prepared clone for cleanup.", nil)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return ClonePreparationResult{}, apierr.Conflict("CLONE_DESTINATION_EXISTS", "The clone destination became unavailable before the repository could be created.", map[string]any{"path": target})
	}
	cleanupPath = target
	cleanupPath = ""
	return ClonePreparationResult{Path: target, RemoteURL: remoteURL, PreparationID: preparationID}, nil
}

// Git may print a URL containing secrets. Inspect bounded output for retry
// classification only; never return or log it.
type limitedCloneOutput struct{ data []byte }

func (b *limitedCloneOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 32*1024 - len(b.data); remaining > 0 {
		b.data = append(b.data, p[:min(len(p), remaining)]...)
	}
	return n, nil
}

func (b *limitedCloneOutput) String() string { return string(b.data) }

func transientCloneFailure(output string) bool {
	output = strings.ToLower(output)
	for _, permanent := range []string{"authentication failed", "permission denied", "repository not found", "could not read username", "host key verification failed", "access denied"} {
		if strings.Contains(output, permanent) {
			return false
		}
	}
	for _, transient := range []string{"could not resolve host", "could not resolve hostname", "failed to connect", "connection timed out", "connection reset", "connection refused", "remote end hung up", "early eof", "http 502", "http 503", "http 504", "returned error: 502", "returned error: 503", "returned error: 504", "operation timed out", "tls connection was non-properly terminated"} {
		if strings.Contains(output, transient) {
			return true
		}
	}
	return false
}

func newClonePreparationID() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func readClonePreparationID(path string) (string, bool, error) {
	marker := filepath.Join(path, ".git", clonePreparationMarker)
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, errors.New("clone preparation marker is not a regular file")
	}
	value, err := os.ReadFile(marker)
	if err != nil {
		return "", false, err
	}
	id := strings.TrimSpace(string(value))
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 32 {
		return "", false, errors.New("invalid clone preparation marker")
	}
	return id, true, nil
}

func sameClonePreparationID(actual, requested string) bool {
	actualBytes := []byte(actual)
	requestedBytes := []byte(strings.TrimSpace(requested))
	return len(actualBytes) == len(requestedBytes) && subtle.ConstantTimeCompare(actualBytes, requestedBytes) == 1
}

func removeClonePreparationMarker(path string) {
	_ = os.Remove(filepath.Join(path, ".git", clonePreparationMarker))
}

func cloneRepositoryName(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\r\n\t ") || strings.HasPrefix(raw, "-") {
		return "", invalidCloneURL()
	}

	var remotePath string
	if match := scpStyleGitURL.FindStringSubmatch(raw); len(match) == 2 {
		remotePath = match[1]
	} else {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" {
			return "", invalidCloneURL()
		}
		if _, ok := allowedCloneSchemes[strings.ToLower(parsed.Scheme)]; !ok {
			return "", invalidCloneURL()
		}
		scheme := strings.ToLower(parsed.Scheme)
		hasPassword := false
		if parsed.User != nil {
			_, hasPassword = parsed.User.Password()
		}
		if ((scheme == "http" || scheme == "https") && (parsed.User != nil || parsed.RawQuery != "")) || hasPassword {
			return "", apierr.Invalid("GIT_URL_CONTAINS_CREDENTIALS", "Use your configured Git credentials or an SSH URL instead of putting credentials in the repository URL.", nil)
		}
		remotePath = parsed.Path
	}

	remotePath = strings.TrimRight(remotePath, "/\\")
	name := strings.TrimSuffix(filepath.Base(remotePath), ".git")
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\<>:"|?*`) {
		return "", invalidCloneURL()
	}
	return name, nil
}

func invalidCloneURL() error {
	return apierr.Invalid("INVALID_GIT_URL", "Enter a valid HTTPS, SSH, Git, or file repository URL.", nil)
}
