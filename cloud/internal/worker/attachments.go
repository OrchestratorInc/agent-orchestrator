package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/attachments"
	"github.com/google/uuid"
)

const AttachmentCapability = "attachments.images.v1"

type AttachmentReader interface {
	AttachmentReadGrant(context.Context, string) (attachments.ReadGrant, error)
}

var materializeMu sync.Mutex

func AttachmentPath(m attachments.Metadata) string {
	return ".ao/attachments/image-" + m.ID + attachments.Extensions[m.MIMEType]
}

// Install exclusions before the first file is written. Cloud workspaces are
// independent clones; a Git directory outside this clone is not accepted.
func excludeAttachments(ctx context.Context, workspace string, root *os.Root) error {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-path", "info/exclude")
	cmd.Dir = workspace
	raw, err := cmd.Output()
	if err != nil {
		return errors.New("attachment Git exclusion could not be installed")
	}
	path := strings.TrimSpace(string(raw))
	if filepath.IsAbs(path) {
		path, err = filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
	}
	if path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return errors.New("Git exclusion is outside workspace")
	}
	if err := root.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	existing, err := root.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.Contains(string(existing), "/.ao/attachments/\n") {
		return nil
	}
	file, err := root.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString("\n/.ao/attachments/\n")
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func MaterializeAttachments(ctx context.Context, workspace, baseURL string, reader AttachmentReader, manifest []attachments.Metadata) ([]string, error) {
	materializeMu.Lock()
	defer materializeMu.Unlock()
	paths := make([]string, 0, len(manifest))
	if len(manifest) == 0 {
		return paths, nil
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := excludeAttachments(ctx, workspace, root); err != nil {
		return nil, err
	}
	for _, dir := range []string{".ao", ".ao/attachments"} {
		info, err := root.Lstat(dir)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("attachment directory is a symlink")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := root.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	client := &http.Client{Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	for _, m := range manifest {
		if _, err := uuid.Parse(m.ID); err != nil || attachments.ValidateMetadata(m) != nil {
			return nil, attachments.ErrInvalid
		}
		path := AttachmentPath(m)
		if file, err := root.Open(path); err == nil {
			data, err := io.ReadAll(io.LimitReader(file, m.Size+1))
			file.Close()
			sum := sha256.Sum256(data)
			if err == nil && int64(len(data)) == m.Size && hex.EncodeToString(sum[:]) == m.SHA256 {
				paths = append(paths, path)
				continue
			}
		}
		grant, err := reader.AttachmentReadGrant(ctx, m.ID)
		if err != nil {
			return nil, errors.New("image read grant unavailable; retry attachment preparation")
		}
		u, err := url.Parse(grant.URL)
		if err != nil {
			return nil, errors.New("invalid image read grant")
		}
		if !u.IsAbs() {
			base, err := url.Parse(baseURL)
			if err != nil {
				return nil, err
			}
			u = base.ResolveReference(u)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, errors.New("invalid image read grant")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, errors.New("invalid image request")
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, errors.New("image download failed; retry attachment preparation")
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return nil, errors.New("image download grant expired or unavailable")
		}
		data, err := attachments.Verify(response.Body, m)
		response.Body.Close()
		if err != nil {
			return nil, errors.New("downloaded image failed size, checksum or format verification")
		}
		temp := path + "." + uuid.NewString() + ".tmp"
		file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = file.Write(data)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = root.Rename(temp, path)
		}
		_ = root.Remove(temp)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}
func ImageToolPrompt(text string, paths []string) string {
	if len(paths) == 0 {
		return text
	}
	return text + "\n\nRead each attached image with your image-reading tool before responding. Do not infer its contents from the filename:\n- " + strings.Join(paths, "\n- ")
}
