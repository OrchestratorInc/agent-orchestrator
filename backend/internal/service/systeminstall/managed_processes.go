package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Detached provider hubs can outlive the AO launch that created them. Durable
// session leases alone cannot prove that their executable/files are unused.
type managedProcessSnapshot struct {
	commands []string
	opaque   []string
}

func (s *Service) snapshotManagedProcesses(ctx context.Context) (managedProcessSnapshot, error) {
	var snapshot managedProcessSnapshot
	if s.commands == nil {
		return snapshot, errors.New("process inventory unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	argv := []string{"ps", "-axww", "-o", "pid=,comm=,args="}
	if s.goos == "windows" {
		// InputObject @() preserves an array even in Windows PowerShell 5.1.
		argv = []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; ConvertTo-Json -Compress -InputObject @(Get-CimInstance Win32_Process -ErrorAction Stop | Select-Object Name,ExecutablePath,CommandLine)"}
	}
	const maxSnapshotBytes = 16 << 20
	out := &capturedOutput{max: maxSnapshotBytes}
	if err := s.commands.Run(ctx, argv, out, io.Discard); err != nil {
		return snapshot, err
	}
	text := out.String()
	if strings.TrimSpace(text) == "" || len(text) >= maxSnapshotBytes {
		return snapshot, errors.New("process inventory empty or truncated")
	}
	if s.goos == "windows" {
		text = strings.TrimPrefix(text, "\xef\xbb\xbf")
		var processes []struct {
			Name           string
			ExecutablePath string
			CommandLine    string
		}
		if err := json.Unmarshal([]byte(text), &processes); err != nil {
			return snapshot, errors.New("invalid process inventory")
		}
		if len(processes) == 0 {
			return snapshot, errors.New("process inventory empty")
		}
		for _, process := range processes {
			if process.Name == "" {
				return snapshot, errors.New("process identity missing")
			}
			snapshot.commands = append(snapshot.commands, process.ExecutablePath, process.CommandLine)
			if process.CommandLine == "" {
				name := strings.ToLower(process.Name)
				if process.ExecutablePath == "" || managedRuntimeExecutable(name) {
					snapshot.opaque = append(snapshot.opaque, name)
				}
			}
		}
	} else {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return snapshot, errors.New("invalid process inventory")
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil || pid <= 0 {
				return snapshot, errors.New("invalid process identity")
			}
			snapshot.commands = append(snapshot.commands, strings.TrimSpace(line[len(fields[0]):]))
		}
	}
	for i, command := range snapshot.commands {
		snapshot.commands[i] = s.managedProcessPath(command)
	}
	return snapshot, nil
}

func managedRuntimeExecutable(name string) bool {
	switch name {
	case "node.exe", "bun.exe", "python.exe", "python3.exe", "pythonw.exe", "cmd.exe", "powershell.exe", "pwsh.exe":
		return true
	default:
		return false
	}
}

func (s *Service) managedProcessPath(path string) string {
	path = strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
	if s.goos == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func (s *Service) managedReleaseHasProcess(snapshot managedProcessSnapshot, target Target, record managedRecord, release string) bool {
	cli := record.CLI
	if target == TargetOpencodeV2 {
		cli = "opencode-v2"
	}
	for _, name := range snapshot.opaque {
		// A loader's native child, such as copilot-win32-x64.exe, counts too.
		if name == strings.ToLower(record.CLI)+".exe" || managedRuntimeExecutable(name) ||
			(strings.HasPrefix(name, strings.ToLower(record.CLI)+"-") && strings.HasSuffix(name, ".exe")) {
			// An unreadable script command line cannot rule out an old module.
			return true
		}
	}
	root := s.managedTargetDir(target)
	paths := []string{filepath.Join(root, "versions", release), filepath.Join(root, "current"), filepath.Join(s.ManagedBinDir(), cli)}
	if s.goos == "windows" {
		paths = append(paths, filepath.Join(s.ManagedBinDir(), cli+".cmd"))
	}
	// ps may show canonical paths while AO's data directory uses a symlink.
	if canonical, err := filepath.EvalSymlinks(root); err == nil && canonical != root {
		paths = append(paths, filepath.Join(canonical, "versions", release), filepath.Join(canonical, "current"), filepath.Join(filepath.Dir(canonical), "bin", cli))
		if s.goos == "windows" {
			paths = append(paths, filepath.Join(filepath.Dir(canonical), "bin", cli+".cmd"))
		}
	}
	return s.processSnapshotUsesPaths(snapshot, paths...)
}

func (s *Service) processSnapshotUsesPaths(snapshot managedProcessSnapshot, paths ...string) bool {
	for _, path := range paths {
		path = s.managedProcessPath(path)
		for _, command := range snapshot.commands {
			for offset := 0; offset < len(command); {
				index := strings.Index(command[offset:], path)
				if index < 0 {
					break
				}
				end := offset + index + len(path)
				if end == len(command) || strings.ContainsRune("/ \t\r\n\"'", rune(command[end])) {
					return true
				}
				offset = end
			}
		}
	}
	return false
}

// A formula's Cellar also holds old provider hubs that have no session lease.
func (s *Service) brewCleanupHasProcess(ctx context.Context, target Target, formula string, snapshot managedProcessSnapshot) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := &capturedOutput{max: maxOutputBytes}
	if err := s.commands.Run(ctx, []string{"brew", "--cellar", formula}, out, io.Discard); err != nil {
		return false, err
	}
	cellar := strings.TrimSpace(out.String())
	if !filepath.IsAbs(cellar) || len(cellar) >= maxOutputBytes {
		return false, errors.New("formula Cellar could not be verified")
	}
	var binary string
	if resolver, ok := s.verifier.(interface {
		Resolve(context.Context, Target) (string, error)
	}); ok {
		var err error
		binary, err = resolver.Resolve(ctx, target)
		if err != nil {
			return false, err
		}
	} else if s.verifier != nil {
		result, err := s.verifier.Verify(ctx, target)
		if err != nil {
			return false, err
		}
		binary = result.ResolvedPath
	}
	if binary == "" {
		return false, errors.New("formula executable could not be verified")
	}
	paths := []string{cellar, binary}
	if canonical, err := filepath.EvalSymlinks(cellar); err == nil && canonical != cellar {
		paths = append(paths, canonical)
	}
	return s.processSnapshotUsesPaths(snapshot, paths...), nil
}
