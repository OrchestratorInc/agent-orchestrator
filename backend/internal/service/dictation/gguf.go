package dictation

import (
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
	"time"
)

const ggufVersion = "0.4.1"

func (s *Service) ggufPython() string {
	root := filepath.Join(s.root, "transcribe-"+ggufVersion)
	if runtime.GOOS == "windows" {
		return filepath.Join(root, "Scripts", "python.exe")
	}
	return filepath.Join(root, "bin", "python")
}
func (s *Service) vadPath() string { return filepath.Join(s.root, "models", "silero-v6.2.gguf") }

func (s *Service) installGGUF(ctx context.Context, m modelSpec) error {
	uv, err := s.ensureUV(ctx)
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(s.ggufPython()))
	marker := filepath.Join(root, "ao-ready")
	if _, err := os.Stat(marker); err != nil {
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		s.stageIs("Preparing local speech runtime")
		if err := s.run(ctx, uv, "venv", "--python", "3.12.15", root); err != nil {
			return err
		}
		if err := s.run(ctx, uv, "pip", "install", "--python", s.ggufPython(), "transcribe-cpp=="+ggufVersion, "transcribe-cpp-native=="+ggufVersion); err != nil {
			return err
		}
		if err := os.WriteFile(marker, []byte(ggufVersion), 0o600); err != nil {
			return err
		}
	}
	if _, err := os.Stat(s.vadPath()); err != nil {
		s.stageIs("Downloading silence detection")
		if err := downloadVerified(ctx, "https://huggingface.co/handy-computer/silero-vad-v6.2-gguf/resolve/97bbd75179f535e4708bccdf61e9278b8b60dfc7/silero-vad-v6.2-F32.gguf", s.vadPath(), 1241056, "0d871a800c420fdd5d1b17a3aea4296d1c39608f0ff4a9295eb76457eb1c88e6", nil); err != nil {
			return err
		}
	}
	s.stageIs("Downloading and verifying " + m.name)
	url := "https://huggingface.co/" + m.id + "/resolve/" + m.revision + "/" + m.filename
	if err := downloadVerified(ctx, url, s.modelPath(m), m.bytes, m.checksum, func(done int64) { s.progressIs(done, m.bytes) }); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(s.marker(m), []byte(ggufVersion), 0o600)
}

// progressWriter reports the running byte count to a callback.
type progressWriter struct {
	n  int64
	on func(int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	w.on(w.n)
	return len(p), nil
}

func downloadVerified(ctx context.Context, url, dest string, size int64, checksum string, onProgress func(int64)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 30 * time.Minute}).Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("model download returned HTTP %d", response.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	hash := sha256.New()
	writers := []io.Writer{file, hash}
	if onProgress != nil {
		writers = append(writers, &progressWriter{on: onProgress})
	}
	n, err := io.Copy(io.MultiWriter(writers...), io.LimitReader(response.Body, size+1))
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != checksum {
		return errors.New("model download failed checksum verification")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), dest)
}
