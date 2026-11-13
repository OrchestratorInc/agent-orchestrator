package dictation

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type asset struct{ name, checksum string }

func uvAsset() (asset, bool) {
	assets := map[string]asset{
		"darwin/arm64":  {"uv-aarch64-apple-darwin.tar.gz", "81b811b57e4624df7ed3aac148e0359100d9962f3d9dce00aec533dc2e086731"},
		"darwin/amd64":  {"uv-x86_64-apple-darwin.tar.gz", "20c47e4c1f630b8abaff8c291fa9b9aa300fef92c4fbff95adf198b55287cda7"},
		"linux/arm64":   {"uv-aarch64-unknown-linux-gnu.tar.gz", "71279f8d3c67241e807b9d8db872d1bf541579fb9f08b7d1eb1682196d1e57dc"},
		"linux/amd64":   {"uv-x86_64-unknown-linux-gnu.tar.gz", "c5221ff41b7892bfc223543883600125f6a47fe845093e0f5bdff4f359196d83"},
		"windows/amd64": {"uv-x86_64-pc-windows-msvc.zip", "0cdc5b38fe5a8168ed6a25814f5ba43c21d313dd3187314fccb37b7cec46e021"},
	}
	a, ok := assets[runtime.GOOS+"/"+runtime.GOARCH]
	return a, ok
}

// Install starts a cancellable, checksum-verified model installation.
func (s *Service) Install(requestCtx context.Context, id string) error {
	if err := requestCtx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := findModel(id)
	if !ok {
		return errors.New("unknown model")
	}
	if _, ok := uvAsset(); !ok {
		return errors.New("local speech models are not supported on this platform")
	}
	if s.closed || s.recording || s.installing != "" {
		return errors.New("wait for dictation or installation to finish")
	}
	if s.installed(m) {
		return nil
	}
	s.stopWorker()
	// A leftover marker must not claim the model is ready while it is being fetched again.
	_ = os.Remove(s.marker(m))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	s.cancelInstall = cancel
	s.installing = id
	s.stage = "Preparing runtime"
	s.failure = ""
	s.installDone = make(chan struct{})
	go func() {
		defer cancel()
		err := s.install(ctx, m)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			if ctx.Err() != nil {
				s.failure = "Installation cancelled. You can retry."
			} else {
				s.failure = err.Error()
			}
		}
		s.installing = ""
		s.stage, s.progressDone, s.progressTotal = "", 0, 0
		s.cancelInstall = nil
		s.prepare()
		close(s.installDone)
	}()
	return nil
}
func (s *Service) install(ctx context.Context, m modelSpec) error {
	if m.gguf() {
		return s.installGGUF(ctx, m)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	uv, err := s.ensureUV(ctx)
	if err != nil {
		return err
	}
	runtimePath := filepath.Dir(filepath.Dir(s.python()))
	marker := filepath.Join(runtimePath, "ao-ready")
	if _, err := os.Stat(marker); err != nil {
		// Interrupted dependency installs are rebuilt; no half-installed runtime is used.
		if err := os.RemoveAll(runtimePath); err != nil {
			return err
		}
		s.stageIs("Installing Python runtime")
		if err := s.run(ctx, uv, "venv", "--python", "3.12.15", runtimePath); err != nil {
			return err
		}
		s.stageIs("Installing speech engine (first download may take several minutes)")
		requirement := "fermion-research==" + runtimeVersion
		if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
			requirement = "fermion-research[mlx]==" + runtimeVersion
		}
		args := []string{"pip", "install", "--python", s.python(), requirement}
		if runtime.GOOS != "darwin" {
			args = append(args, "--torch-backend", "cpu")
		}
		if err := s.run(ctx, uv, args...); err != nil {
			return err
		}
		if err := os.WriteFile(marker, []byte(runtimeVersion), 0o600); err != nil {
			return err
		}
	}
	s.stageIs("Downloading and verifying " + m.name)
	// Fermion 0.2.15 pins each model's SHA-256 and verifies unpacked members.
	stopWatching := s.watchBlobs(filepath.Join(s.root, "models", "blobs"), m.bytes)
	err = s.run(ctx, s.python(), "-c", "from fermion.cli import main; main()", "transcribe", m.id, "--download-only")
	stopWatching()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(s.marker(m), []byte(runtimeVersion), 0o600)
}
func (s *Service) ensureUV(ctx context.Context) (string, error) {
	a, ok := uvAsset()
	if !ok {
		return "", errors.New("unsupported platform")
	}
	name := "uv"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := filepath.Join(s.root, "tools", "uv-0.13.7", name)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/astral-sh/uv/releases/download/0.13.7/"+a.name, http.NoBody)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{Timeout: 10 * time.Minute}).Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("runtime download returned HTTP %d", response.StatusCode)
	}
	archive, err := os.CreateTemp(filepath.Dir(dest), "download-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(archive.Name()) }()
	defer func() { _ = archive.Close() }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(response.Body, 100*1024*1024+1))
	if err != nil {
		return "", err
	}
	if n > 100*1024*1024 || hex.EncodeToString(hash.Sum(nil)) != a.checksum {
		return "", errors.New("runtime download failed checksum verification")
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	output, err := os.OpenFile(dest+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700) //nolint:gosec // The checksum-verified runtime must be executable, accessible only to its owner.
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(dest + ".tmp") }()
	err = extractUV(archive, n, a.name, name, output)
	closeErr := output.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(dest+".tmp", dest); err != nil {
		return "", err
	}
	return dest, nil
}
func extractUV(archive *os.File, size int64, archiveName, name string, output io.Writer) error {
	copyBinary := func(reader io.Reader) error {
		n, err := io.Copy(output, io.LimitReader(reader, 100*1024*1024+1))
		if n > 100*1024*1024 {
			return errors.New("runtime binary exceeds limit")
		}
		return err
	}
	if strings.HasSuffix(archiveName, ".zip") {
		z, err := zip.NewReader(archive, size)
		if err != nil {
			return err
		}
		for _, file := range z.File {
			if file.Name != name && file.Name != strings.TrimSuffix(archiveName, ".zip")+"/"+name {
				continue
			}
			r, err := file.Open()
			if err != nil {
				return err
			}
			copyErr := copyBinary(r)
			closeErr := r.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
	} else {
		gz, err := gzip.NewReader(archive)
		if err != nil {
			return err
		}
		defer func() { _ = gz.Close() }()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if h.Name == strings.TrimSuffix(archiveName, ".tar.gz")+"/"+name && h.Typeflag == tar.TypeReg {
				return copyBinary(tr)
			}
		}
	}
	return errors.New("runtime archive does not contain uv")
}
