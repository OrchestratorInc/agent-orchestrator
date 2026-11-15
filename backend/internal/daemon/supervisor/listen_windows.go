//go:build windows

package supervisor

import (
	"net"
	"path/filepath"
	"regexp"

	"github.com/Microsoft/go-winio"
)

var unsafePipeChars = regexp.MustCompile(`[^a-zA-Z0-9\-]`)

func pipeNameFromRunFile(runFilePath string) string {
	if runFilePath == "" {
		return `\\.\pipe\ao-supervise`
	}
	dir := filepath.Base(filepath.Dir(runFilePath))
	if dir == ".ao" || dir == "." || dir == "" {
		return `\\.\pipe\ao-supervise`
	}
	return `\\.\pipe\ao-supervise-` + unsafePipeChars.ReplaceAllString(dir, "-")
}

func Listen(runFilePath string) (net.Listener, string, error) {
	name := pipeNameFromRunFile(runFilePath)
	ln, err := winio.ListenPipe(name, nil)
	if err != nil {
		return nil, "", err
	}
	return ln, name, nil
}
