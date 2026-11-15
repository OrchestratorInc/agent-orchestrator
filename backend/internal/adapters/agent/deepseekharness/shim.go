package deepseekharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type npmShimProgram struct {
	UsesInterpreter bool
	Target          string
}

func resolveWindowsShimArgv(argv []string, lookPath func(string) (string, error)) []string {
	if len(argv) == 0 || !isWindowsBatchFile(argv[0]) {
		return argv
	}
	shim := argv[0]
	content, err := os.ReadFile(shim) //nolint:gosec // path is the resolved harness binary
	if err != nil {
		return argv
	}
	program, ok := npmShimInvocation(filepath.Dir(shim), string(content))
	if !ok {
		return argv
	}

	target := ""
	var executable string
	if program.UsesInterpreter {
		if !isRegularFile(program.Target) {
			return argv
		}
		target = program.Target
		executable = resolveNodeRuntime(filepath.Dir(shim), lookPath)
	} else {
		executable = program.Target
	}
	if executable == "" || !isRegularFile(executable) {
		return argv
	}

	rewritten := make([]string, 0, len(argv)+1)
	rewritten = append(rewritten, executable)
	if target != "" {
		rewritten = append(rewritten, target)
	}
	rewritten = append(rewritten, argv[1:]...)
	return rewritten
}

func isWindowsBatchFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return true
	default:
		return false
	}
}

func npmShimInvocation(dir, content string) (npmShimProgram, bool) {
	var found npmShimProgram
	ok := false
	interpreter := npmShimInterpreter(content)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.Contains(line, "%*") {
			continue
		}
		tokens := quotedTokens(line)
		if len(tokens) == 0 {
			continue
		}
		program, parsed := npmShimProgramFromTokens(dir, tokens, interpreter)
		if !parsed {
			continue
		}
		found, ok = program, true
	}
	return found, ok
}

func npmShimInterpreter(content string) string {
	const elseMarker = ") ELSE ("
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(strings.ToUpper(line), strings.ToUpper(elseMarker)) {
			continue
		}
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(strings.TrimSuffix(next, "\r"))
			name, ok := strings.CutPrefix(next, `SET "_prog=`)
			if !ok {
				continue
			}
			name = strings.Trim(strings.TrimSuffix(name, `"`), `"`)
			if isSupportedInterpreter(name) {
				return name
			}
			return ""
		}
	}
	return ""
}

func isSupportedInterpreter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "node", "nodejs":
		return true
	default:
		return false
	}
}

func npmShimProgramFromTokens(dir string, tokens []string, interpreter string) (npmShimProgram, bool) {
	if strings.Contains(tokens[0], "%_prog%") {
		if len(tokens) < 2 {
			return npmShimProgram{}, false
		}
		target := expandShimPath(tokens[len(tokens)-1], dir)
		if target == "" || interpreter == "" {
			return npmShimProgram{}, false
		}
		return npmShimProgram{UsesInterpreter: true, Target: target}, true
	}
	target := expandShimPath(tokens[0], dir)
	if target == "" {
		return npmShimProgram{}, false
	}
	return npmShimProgram{Target: target}, true
}

func quotedTokens(line string) []string {
	line = invocationSegment(line)
	var tokens []string
	var current strings.Builder
	inQuotes := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
		}
		current.Reset()
	}
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '"':
			if inQuotes {
				flush()
				inQuotes = false
			} else {
				inQuotes = true
			}
		case (c == ' ' || c == '\t') && !inQuotes:
			flush()
		default:
			current.WriteByte(c)
		}
	}
	flush()
	if n := len(tokens); n > 0 && strings.EqualFold(tokens[n-1], "%*") {
		tokens = tokens[:n-1]
	}
	return tokens
}

func invocationSegment(line string) string {
	inQuotes := false
	lastSep := -1
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuotes = !inQuotes
		case '&':
			if !inQuotes {
				lastSep = i
			}
		}
	}
	if lastSep >= 0 {
		return line[lastSep+1:]
	}
	return line
}

func expandShimPath(token, dir string) string {
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	expanded := strings.ReplaceAll(token, "%~dp0", prefix)
	expanded = strings.ReplaceAll(expanded, "%dp0%", prefix)
	if expanded == "" || strings.Contains(expanded, "%") {
		return ""
	}
	return filepath.Clean(expanded)
}

func resolveNodeRuntime(shimDir string, lookPath func(string) (string, error)) string {
	if local := filepath.Join(shimDir, "node.exe"); isRegularFile(local) {
		return local
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	for _, name := range []string{"node.exe", "node"} {
		if path, err := lookPath(name); err == nil && isRegularFile(path) {
			return path
		}
	}
	return ""
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
