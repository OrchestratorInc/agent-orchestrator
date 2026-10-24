package systemcheck

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var githubVersionPattern = regexp.MustCompile(`^gh version v?([0-9]+)\.([0-9]+)\.([0-9]+)([-+][^\s]+)?(?:\s|$)`)

// Version checks belong to the advisory probe, never the process-free startup
// gate. Unknown banners and failed probes are not evidence of an old CLI.
func (s *Service) githubCLIVersion(ctx context.Context) (string, bool) {
	if s.commands == nil {
		return "", false
	}
	path, err := s.executables.LookPath("gh")
	if err != nil || path == "" {
		return "", false
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := s.commands.Run(probeCtx, []string{path, "--version"}, &output, io.Discard); err != nil {
		return "", false
	}
	parts := githubVersionPattern.FindStringSubmatch(strings.TrimSpace(output.String()))
	if parts == nil {
		return "", false
	}
	var numbers [3]int
	for i := range numbers {
		numbers[i], err = strconv.Atoi(parts[i+1])
		if err != nil {
			return "", false
		}
	}
	version := strings.Join(parts[1:4], ".") + parts[4]
	old := numbers[0] < 2 || numbers[0] == 2 && (numbers[1] < 4 || numbers[1] == 4 && numbers[2] == 0 && strings.HasPrefix(parts[4], "-"))
	return version, old
}
