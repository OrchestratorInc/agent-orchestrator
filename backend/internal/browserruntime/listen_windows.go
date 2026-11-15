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

func Listen(runFilePath string) (net.Listener, string, error) {
	return ListenNamed(runFilePath, "browser")
}

func ListenNamed(runFilePath, runtimeName string) (net.Listener, string, error) {
	name := pipeNameFromRunFile(runFilePath)
	if runtimeName != "browser" {
		name += "-" + unsafePipeChars.ReplaceAllString(runtimeName, "-")
	}
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;OW)",
	})
	if err != nil {
		return nil, "", err
	}
	return ln, name, nil
}
