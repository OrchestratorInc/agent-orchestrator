package testfixture

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var secretDiagnostic = regexp.MustCompile(`(?i)((?:api[_-]?key|authorization|access[_-]?token|refresh[_-]?token|password|secret)["' :=]+)[^\s,"'}]+`)
var bearerDiagnostic = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]+`)

// SafeDiagnostic bounds and redacts text from an isolated synthetic run. It
// must not be used as authorization to read a real provider account's logs.
func SafeDiagnostic(text string, limit int) string {
	text = secretDiagnostic.ReplaceAllString(text, "${1}[redacted]")
	text = bearerDiagnostic.ReplaceAllString(text, "Bearer [redacted]")
	text = strings.ReplaceAll(text, "ao-fixture-key", "[fixture-key]")
	if len(text) > limit {
		text = text[:limit] + "..."
	}
	return text
}

// CaptureFailureLogs registers bounded native log capture before the caller's
// temporary directories are removed. Only explicitly owned scratch roots may
// be passed. Raw provider stderr is discarded by the production persistent host;
// these native log files and normalized protocol errors are separate evidence.
func CaptureFailureLogs(t *testing.T, roots ...string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, root := range roots {
			var files []string
			_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					return nil
				}
				if strings.HasSuffix(path, ".log") || strings.Contains(filepath.ToSlash(path), "/logs/") {
					files = append(files, path)
				}
				return nil
			})
			sort.Strings(files)
			if len(files) > 4 {
				files = files[len(files)-4:]
			}
			for _, path := range files {
				info, err := os.Stat(path)
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				file, err := os.Open(path)
				if err != nil {
					continue
				}
				offset := max(int64(0), info.Size()-4096)
				buffer := make([]byte, min(int64(4096), info.Size()))
				n, _ := file.ReadAt(buffer, offset)
				_ = file.Close()
				t.Logf("owned native log %s (tail): %s", path, SafeDiagnostic(string(buffer[:n]), 4096))
			}
		}
	})
}
