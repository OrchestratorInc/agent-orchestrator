package binaryutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Version managers are runtimes, like Node, rather than agent launchers. Only
// their generated dispatch forms qualify for this boundary: additional shell
// commands fall through to ordinary launcher inspection. This does not attest
// manager implementations or the user's version-selection configuration.
func inspectDiscoveryManagerShim(ctx context.Context, agentID, selected string, body []byte, inspection *discoveryInspection) (bool, error) {
	first, _, _ := strings.Cut(string(body), "\n")
	if strings.TrimSpace(first) != "#!/usr/bin/env bash" && strings.TrimSpace(first) != "#!/bin/bash" {
		return false, nil
	}
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return false, nil
	}
	args := discoveryLauncherTokens.FindAllString(lines[len(lines)-1], -1)
	if len(args) != 5 || args[0] != "exec" || args[2] != "exec" || args[4] != `"$@"` {
		return false, nil
	}
	managerPath := strings.Trim(args[1], `"`)
	manager := strings.TrimSuffix(strings.ToLower(filepath.Base(filepath.FromSlash(managerPath))), ".exe")
	if manager != "asdf" && manager != "pyenv" && manager != "nodenv" {
		return false, nil
	}
	command := strings.Trim(args[3], `"`)
	if command == "$program" && manager != "asdf" {
		command = filepath.Base(selected)
	}
	if discoveryExecutableOwner(command) != agentID {
		return true, fmt.Errorf("%s shim dispatches %q instead of %s", manager, command, agentID)
	}
	if !discoveryManagerPreamble(manager, lines[:len(lines)-1]) || strings.ContainsAny(managerPath, "$`%") {
		return false, nil
	}
	resolved := filepath.FromSlash(managerPath)
	if !filepath.IsAbs(resolved) {
		var err error
		resolved, err = exec.LookPath(managerPath)
		if err != nil {
			return true, fmt.Errorf("resolve %s runtime: %w", manager, err)
		}
	}
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return true, err
	}
	if inspection.active[canonical] {
		return true, fmt.Errorf("launcher chain is cyclic or too deep")
	}
	if owner := discoveryExecutableOwner(canonical); owner != "" && owner != agentID {
		return true, fmt.Errorf("%s runtime redirects to %s instead of %s", manager, owner, agentID)
	}
	if len(inspection.active)+len(inspection.completed) >= discoveryLauncherFileLimit {
		return true, fmt.Errorf("launcher exceeds total file inspection limit (%d)", discoveryLauncherFileLimit)
	}
	file, err := os.Open(canonical)
	if err != nil {
		return true, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return true, err
	}
	if !info.Mode().IsRegular() {
		return true, fmt.Errorf("%s runtime is not a regular file", manager)
	}
	content, err := io.ReadAll(io.LimitReader(file, discoveryLauncherReadLimit+1))
	if err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	native := bytes.HasPrefix(content, []byte("MZ")) || bytes.HasPrefix(content, []byte("\x7fELF")) || bytes.ContainsRune(content, '\x00')
	if !native {
		// asdf 0.16+ is native. pyenv/nodenv use their standard Bash command
		// dispatchers; parsing internal helpers as agents rejects every install.
		if manager == "asdf" || len(content) > discoveryLauncherReadLimit ||
			!bytes.HasPrefix(content, []byte("#!/usr/bin/env bash")) ||
			!bytes.Contains(content, []byte(manager+`-$command`)) ||
			!bytes.Contains(content, []byte(`exec "$command_path" "$@"`)) {
			return true, fmt.Errorf("cannot verify %s runtime dispatcher", manager)
		}
		// A known manager name must not hide an installer overwrite either.
		for _, line := range strings.Split(string(content), "\n") {
			for _, invocation := range discoveryLauncherInvocations(strings.TrimSpace(line), false) {
				if invocation.refusal != "" {
					return true, fmt.Errorf("%s runtime: %s", manager, invocation.refusal)
				}
				targets := []string{invocation.target}
				for _, app := range discoveryInterpreterApplications(invocation.target, invocation.arguments) {
					targets = append(targets, app.target)
				}
				for _, target := range targets {
					if owner := discoveryExecutableOwner(target); owner != "" && owner != agentID {
						return true, fmt.Errorf("%s runtime redirects to %s instead of %s", manager, owner, agentID)
					}
				}
			}
		}
	}
	inspection.completed[canonical] = discoveryInspectionResult{native: native}
	return true, nil
}

func discoveryManagerPreamble(manager string, lines []string) bool {
	if manager == "asdf" {
		return len(lines) == 0
	}
	upper := strings.ToUpper(manager)
	root := ""
	for _, line := range lines {
		if value, ok := strings.CutPrefix(line, "export "+upper+`_ROOT="`); ok {
			if root != "" || !strings.HasSuffix(value, `"`) {
				return false
			}
			root = strings.TrimSuffix(value, `"`)
			if root == "" || strings.ContainsAny(root, "$`\"") {
				return false
			}
		}
	}
	if root == "" {
		return false
	}
	program := false
	for _, line := range lines {
		switch line {
		case `program="${0##*/}"`:
			program = true
		case "set -e", "fi", "for arg; do", `case "$arg" in`, "-e* | -- ) break ;;", "*/* )", `if [ -f "$arg" ]; then`, "break", ";;", "esac", "done",
			`if [ "$program" = "node" ]; then`, `if [ "$program" = "python" ]; then`, `SHIM_PATH=${0%/*}`:
		default:
			if line != `[ -n "$`+upper+`_DEBUG" ] && set -x` &&
				line != "export "+upper+`_ROOT="`+root+`"` &&
				line != "export "+upper+`_DIR="${arg%/*}"` &&
				line != `if [[ $SHIM_PATH != "`+root+`/shims" ]]; then` &&
				line != "export _"+upper+`_SHIM_PATH="$SHIM_PATH"` {
				return false
			}
		}
	}
	return program
}
