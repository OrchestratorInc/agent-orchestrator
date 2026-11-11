//go:build windows

package browserruntime

import (
	"net"
	"path/filepath"
	"regexp"

	"github.com/Microsoft/go-winio"
)

var unsafePipeChars = regexp.MustCompile(`[^a-zA-Z0-9\-]`)

func pipeNameFromRunFile(runFilePath string) string {
	if runFilePath == "" {
		return `\\.\pipe\ao-browser`
	}
	dir := filepath.Base(filepath.Dir(runFilePath))
	if dir == ".ao" || dir == "." || dir == "" {
		return `\\.\pipe\ao-browser`
	}
	return `\\.\pipe\ao-browser-` + unsafePipeChars.ReplaceAllString(dir, "-")
}

// Listen creates the local daemon-to-Electron browser bridge listener.
func Listen(runFilePath string) (net.Listener, string, error) {
	return ListenNamed(runFilePath, "browser")
}

// ListenNamed creates a browser bridge listener for the named runtime. The
// Electron runtime keeps its historical pipe name.
func ListenNamed(runFilePath, runtimeName string) (net.Listener, string, error) {
	name := pipeNameFromRunFile(runFilePath)
	if runtimeName != "browser" {
		name += "-" + unsafePipeChars.ReplaceAllString(runtimeName, "-")
	}
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{
		// Protected DACL: the creating owner and LocalSystem only. This prevents
		// another local account from connecting to or squatting the runtime pipe.
		SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;OW)",
	})
	if err != nil {
		return nil, "", err
	}
	return ln, name, nil
}
