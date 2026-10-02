package tmux

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func retainedStdinSink(entries []processEntry, pid int) bool {
	descendants := descendantPIDs(entries, pid)
	found := false
	for _, entry := range entries {
		if entry.pid == pid {
			found = entry.command == "cat" || entry.command == "/bin/cat" || entry.command == "/usr/bin/cat"
		} else if descendants[entry.pid] {
			return false
		}
	}
	return found
}

func (r *Runtime) probeRetainedPane(ctx context.Context, ref ports.FencedRuntimeRef, entries []processEntry, pid int) ports.FencedProbeResult {
	unknown := ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonIdentityMissing}
	if !retainedStdinSink(entries, pid) {
		return unknown
	}
	panes, err := r.runForSession(ctx, ref.Handle.ID, "list-panes", "-s", "-t", exactSessionTarget(ref.Handle.ID), "-F", "#{pane_pid}")
	if err != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	if strings.TrimSpace(string(panes)) != strconv.Itoa(pid) {
		return unknown
	}
	origin, err := r.runForSession(ctx, ref.Handle.ID, "display-message", "-p", "-t", exactSessionTarget(ref.Handle.ID)+":", "#{pane_start_command}")
	if err != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	// The immutable start command belongs to this pane, unlike an inherited
	// environment variable or session option. Never include it in diagnostics.
	generation, ok := retainedPaneGeneration(string(origin), string(ref.SessionID))
	if !ok {
		return unknown
	}
	if generation != ref.Generation {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonGenerationMismatch}
	}
	current, currentPID, err := r.supervisedProcessTree(ctx, ref.Handle)
	if err != nil {
		return ports.FencedProbeResult{Liveness: ports.FencedUnknown, Reason: ports.FencedReasonProbeFailed}
	}
	if currentPID != pid || !retainedStdinSink(current, pid) {
		return unknown
	}
	return ports.FencedProbeResult{Liveness: ports.FencedDead, Reason: ports.FencedReasonExactAbsent}
}

// Recognize only buildLaunchCommand's supervised, non-interpreting exit path.
// Quoted prompt/env contents cannot supply commands or ownership evidence.
func retainedPaneGeneration(origin, session string) (string, bool) {
	outer, ok := retainedTmuxArgv(origin)
	if !ok || len(outer) != 3 || !filepath.IsAbs(outer[0]) || outer[1] != "-c" {
		return "", false
	}
	commands, ok := retainedShellCommands(outer[2])
	if !ok || len(commands) < 3 {
		return "", false
	}
	first, last := commands[0], commands[len(commands)-1]
	if len(first) != 4 || first[0] != "cd" || first[2] != "\x00||" || first[3] != "exit" {
		return "", false
	}
	if len(last) != 4 || last[0] != "exec" || last[1] != "cat" || last[2] != "\x00>" || last[3] != "/dev/null" {
		return "", false
	}
	var generation, supervised string
	for _, command := range commands[1 : len(commands)-2] {
		if len(command) == 2 && command[0] == "unset" && command[1] == "NO_COLOR" {
			continue
		}
		if len(command) != 2 || command[0] != "export" {
			return "", false
		}
		key, value, found := strings.Cut(command[1], "=")
		if !found || !validEnvKey(key) {
			return "", false
		}
		switch key {
		case "AO_RUNTIME_LAUNCH_ID":
			if generation != "" {
				return "", false
			}
			generation = value
		case "AO_SUPERVISED_PROCESS":
			if supervised != "" {
				return "", false
			}
			supervised = value
		}
	}
	launch := commands[len(commands)-2]
	if supervised != "1" || generation == "" || len(launch) < 9 || launch[1] != "agent-process" || launch[2] != "supervise" || launch[3] != "--session" || launch[4] != session || launch[5] != "--launch" || launch[6] != generation || launch[7] != "--" {
		return "", false
	}
	return generation, true
}

// tmux renders argv with vis C-style escapes, not shell expansion rules.
func retainedTmuxArgv(input string) ([]string, bool) {
	var args []string
	for input = strings.TrimSpace(input); input != ""; input = strings.TrimLeft(input, " \t\r\n") {
		end, quote := 0, byte(0)
		if input[0] == '\'' || input[0] == '"' {
			quote, end = input[0], 1
		}
		for end < len(input) {
			if input[end] == '\\' {
				end += 2
				continue
			}
			if (quote != 0 && input[end] == quote) || (quote == 0 && strings.ContainsRune(" \t\r\n", rune(input[end]))) {
				break
			}
			end++
		}
		if end > len(input) || (quote != 0 && end == len(input)) {
			return nil, false
		}
		encoded := input[:end]
		if quote != 0 {
			encoded = input[1:end]
			end++
		}
		if quote != '"' {
			encoded = strings.ReplaceAll(encoded, `"`, `\"`)
		}
		decoded, err := strconv.Unquote(`"` + encoded + `"`)
		if err != nil || strings.IndexByte(decoded, 0) >= 0 {
			return nil, false
		}
		args = append(args, decoded)
		input = input[end:]
		if input != "" && !strings.ContainsRune(" \t\r\n", rune(input[0])) {
			return nil, false
		}
	}
	return args, true
}

// These tokens are data only. Expansion, substitution, and unsupported shell
// operators are rejected rather than interpreted during an ownership probe.
func retainedShellTokens(input string) ([]string, bool) {
	if strings.IndexByte(input, 0) >= 0 {
		return nil, false
	}
	var tokens []string
	var word strings.Builder
	started := false
	flush := func() {
		if started {
			tokens = append(tokens, word.String())
			word.Reset()
			started = false
		}
	}
	for i := 0; i < len(input); i++ {
		c := input[i]
		switch c {
		case 0, '$', '`', '&', '<', '(', ')':
			return nil, false
		case ' ', '\t', '\n', '\r':
			flush()
		case ';', '>', '|':
			flush()
			op := string(c)
			if c == '|' {
				if i+1 >= len(input) || input[i+1] != '|' {
					return nil, false
				}
				i++
				op = "||"
			}
			tokens = append(tokens, "\x00"+op)
		case '\\':
			if i+1 >= len(input) {
				return nil, false
			}
			i++
			started = true
			word.WriteByte(input[i])
		case '\'', '"':
			started = true
			quote := c
			closed := false
			for i++; i < len(input); i++ {
				c = input[i]
				if c == 0 {
					return nil, false
				}
				if c == quote {
					closed = true
					break
				}
				if quote == '"' {
					if c == '$' || c == '`' {
						return nil, false
					}
					if c == '\\' && i+1 < len(input) && strings.ContainsRune("\\\"$`\n", rune(input[i+1])) {
						i++
						c = input[i]
						if c == '\n' {
							continue
						}
					}
				}
				word.WriteByte(c)
			}
			if !closed {
				return nil, false
			}
		default:
			started = true
			word.WriteByte(c)
		}
	}
	flush()
	return tokens, true
}

func retainedShellCommands(input string) ([][]string, bool) {
	tokens, ok := retainedShellTokens(input)
	if !ok {
		return nil, false
	}
	var commands [][]string
	var command []string
	for _, token := range tokens {
		if token == "\x00;" {
			if len(command) == 0 {
				return nil, false
			}
			commands = append(commands, command)
			command = nil
		} else {
			command = append(command, token)
		}
	}
	if len(command) == 0 {
		return nil, false
	}
	commands = append(commands, command)
	return commands, true
}
