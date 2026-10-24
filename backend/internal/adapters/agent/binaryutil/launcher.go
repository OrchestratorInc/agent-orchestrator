package binaryutil

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type discoveryInvocation struct {
	target     string
	explicit   bool
	arguments  []string
	dependency bool
}

// Quotes keep spaces and shell operators inside an argument. Unquoted shell
// operators start another command, including commands inside conditional blocks.
var discoveryLauncherTokens = regexp.MustCompile(`(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s"';&|()])+|[;&|()]`)

var discoveryLauncherAssignment = regexp.MustCompile(`^(?:export\s+)?([a-zA-Z_][a-zA-Z0-9_]*)=(.*)$`)
var discoveryLauncherVariable = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*|@)\}|\$([a-zA-Z_][a-zA-Z0-9_]*|@)|%[a-zA-Z_][a-zA-Z0-9_]*%`)

func discoveryExpandLauncherTarget(target string, variables map[string]string) (string, error) {
	var expanded strings.Builder
	start := 0
	for _, match := range discoveryLauncherVariable.FindAllStringIndex(target, -1) {
		reference := target[match[0]:match[1]]
		name := strings.Trim(strings.TrimPrefix(reference, "$"), "{}%")
		value, ok := variables[name]
		if !ok && strings.HasPrefix(reference, "%") {
			value, ok = variables[strings.ToUpper(name)]
		}
		if !ok {
			value = reference
		}
		if expanded.Len()+match[0]-start+len(value) > discoveryLauncherReadLimit {
			return "", fmt.Errorf("launcher variable expansion exceeds inspection limit")
		}
		expanded.WriteString(target[start:match[0]])
		expanded.WriteString(value)
		start = match[1]
	}
	if expanded.Len()+len(target)-start > discoveryLauncherReadLimit {
		return "", fmt.Errorf("launcher variable expansion exceeds inspection limit")
	}
	expanded.WriteString(target[start:])
	return expanded.String(), nil
}

func discoveryLauncherInvocations(line string, batch bool) []discoveryInvocation {
	return discoveryLauncherInvocationsAtDepth(line, batch, 0)
}

func discoveryLauncherInvocationsAtDepth(line string, batch bool, depth int) []discoveryInvocation {
	if depth >= 8 {
		return []discoveryInvocation{{target: "uninspectable nested shell command", explicit: true}}
	}
	tokens := discoveryLauncherTokens.FindAllString(line, -1)
	var invocations []discoveryInvocation
	start := 0
	explicit := false
	for i := 0; i <= len(tokens); i++ {
		if i < len(tokens) && (len(tokens[i]) != 1 || !strings.ContainsAny(tokens[i], ";&|()")) {
			continue
		}
		invocations = append(invocations, discoveryLauncherSegment(tokens[start:i], batch, explicit, depth)...)
		explicit = i < len(tokens) && tokens[i] == "&" && (i == 0 || tokens[i-1] == "|")
		start = i + 1
	}
	return invocations
}

func discoveryLauncherSegment(tokens []string, batch, explicit bool, depth int) []discoveryInvocation {
	var invocations []discoveryInvocation
	for i := 0; i < len(tokens); {
		target := strings.TrimPrefix(strings.Trim(tokens[i], `"'`), "@")
		if strings.HasPrefix(target, "#") {
			break
		}
		if assignment := strings.IndexByte(target, '='); assignment > 0 && !strings.ContainsAny(target[:assignment], `/\`) {
			i++ // Environment assignment preceding a POSIX command.
			continue
		}
		commandName := strings.ToLower(filepath.Base(filepath.FromSlash(strings.ReplaceAll(target, `\`, "/"))))
		if commandName == "cmd" || commandName == "cmd.exe" || strings.EqualFold(target, "%COMSPEC%") {
			for j := i + 1; j < len(tokens) && strings.HasPrefix(tokens[j], "/"); j++ {
				if !strings.EqualFold(tokens[j], "/c") && !strings.EqualFold(tokens[j], "/k") {
					continue
				}
				payload := strings.Join(tokens[j+1:], " ")
				if j+2 == len(tokens) && strings.HasPrefix(payload, `"`) && strings.HasSuffix(payload, `"`) {
					inner := strings.TrimSuffix(strings.TrimPrefix(payload, `"`), `"`)
					// A quoted executable path keeps its quotes; a quoted command
					// string must be inspected for commands and operators within it.
					switch strings.ToLower(filepath.Ext(inner)) {
					case ".exe", ".com", ".cmd", ".bat", ".ps1", ".js", ".mjs", ".cjs", ".py", ".sh":
					default:
						payload = inner
					}
				}
				commands := discoveryLauncherInvocationsAtDepth(payload, batch, depth+1)
				for k := range commands {
					// CMD builtins do not resolve through PATH. External delegates
					// must retain the same inspection requirement as CALL/exec.
					switch strings.ToLower(commands[k].target) {
					case "assoc", "break", "cd", "chdir", "cls", "color", "copy", "date", "del", "dir", "echo", "endlocal", "erase", "exit", "for", "ftype", "goto", "if", "md", "mkdir", "mklink", "move", "path", "pause", "popd", "prompt", "pushd", "rd", "rem", "ren", "rename", "rmdir", "set", "setlocal", "shift", "time", "title", "type", "ver", "verify", "vol":
					default:
						commands[k].explicit = true
					}
				}
				return append(invocations, commands...)
			}
		}
		switch strings.ToLower(target) {
		case "exec":
			explicit = true
			i++
			for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
				option := tokens[i]
				i++
				if option == "-a" && i < len(tokens) {
					i++ // argv[0], not the command.
				}
				if option == "--" {
					break
				}
			}
		case "call":
			i++
			if batch && i < len(tokens) && strings.HasPrefix(tokens[i], ":") {
				return invocations // Local batch subroutine; its body is scanned separately.
			}
			explicit = true
		case ".", "source":
			explicit = true
			i++ // Loading a profile is not an extensionless executable redirect.
		case "then", "else", "do":
			i++
		case "--":
			i++
		case "env", "npx", "pnpx":
			explicit = true
			i++
			for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
				option := tokens[i]
				i++
				if pkg, ok := strings.CutPrefix(option, "--package="); ok {
					invocations = append(invocations, discoveryInvocation{target: strings.Trim(pkg, `"'`), dependency: true})
				}
				if (option == "-u" || option == "--unset" || option == "-p" || option == "--package") && i < len(tokens) {
					// Package names can themselves identify a foreign agent.
					if option == "-p" || option == "--package" {
						invocations = append(invocations, discoveryInvocation{target: strings.Trim(tokens[i], `"'`), dependency: true})
					}
					i++
				}
			}
		case "npm", "pnpm", "bun":
			if i+1 < len(tokens) && (tokens[i+1] == "exec" || tokens[i+1] == "dlx" || tokens[i+1] == "x") {
				explicit = true
				i += 2
			} else {
				return append(invocations, discoveryInvocation{target: target, explicit: explicit, arguments: tokens[i+1:]})
			}
		case "start":
			explicit = true
			i++
			if i < len(tokens) && strings.HasPrefix(tokens[i], `"`) {
				i++ // Windows START's optional quoted window title.
			}
			for i < len(tokens) && strings.HasPrefix(tokens[i], "/") {
				i++
			}
		case "if":
			i++
			if i < len(tokens) && strings.EqualFold(tokens[i], "not") {
				i++
			}
			if i < len(tokens) && strings.EqualFold(tokens[i], "exist") {
				i += 2 // Skip EXIST and the tested filename.
			} else {
				return invocations // POSIX condition; subsequent segments/body are inspected.
			}
		default:
			if program, arguments, ok := discoveryVersionManagerProgram(commandName, tokens[i+1:]); ok {
				// The manager resolves the program among its installed tool
				// versions, so only the program name is routing evidence; the
				// manager's own dispatcher is not an agent launcher.
				return append(invocations, discoveryInvocation{target: program, arguments: arguments})
			}
			return append(invocations, discoveryInvocation{target: target, explicit: explicit, arguments: tokens[i+1:]})
		}
	}
	return invocations
}

var discoveryVersionManagers = map[string]bool{
	"asdf": true, "mise": true, "rtx": true, "pyenv": true, "nodenv": true,
	"rbenv": true, "goenv": true, "jenv": true, "plenv": true, "phpenv": true,
}

// Version-manager shims run `<manager> exec <program> "$@"` (or mise's
// `x -- <program>`). It returns the dispatched program and its arguments.
func discoveryVersionManagerProgram(commandName string, arguments []string) (string, []string, bool) {
	manager := strings.TrimSuffix(commandName, ".exe")
	if !discoveryVersionManagers[manager] || len(arguments) < 2 {
		return "", nil, false
	}
	if subcommand := strings.Trim(arguments[0], `"'`); subcommand != "exec" && subcommand != "x" {
		return "", nil, false
	}
	rest := arguments[1:]
	if manager == "mise" || manager == "rtx" {
		// mise accepts tool specs before `--`: `mise x node@22 -- <program>`.
		for i, argument := range rest {
			if argument == "--" {
				rest = rest[i+1:]
				break
			}
		}
	}
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return "", nil, false
	}
	return strings.Trim(rest[0], `"'`), rest[1:], true
}

func discoveryRuntimeTarget(target string) bool {
	// Standard shims delegate through variables or a Node interpreter. Their
	// literal application paths and other command segments are still inspected.
	if strings.ContainsAny(target, "$%") {
		return true
	}
	name := strings.ToLower(filepath.Base(filepath.FromSlash(strings.ReplaceAll(target, `\`, "/"))))
	return name == "node" || name == "node.exe"
}

// Only an interpreter's program argument is executable ownership evidence.
// Paths printed by echo, tested by IF, assigned by SET, or embedded in eval
// strings must not vouch for an otherwise unidentified launcher.
type discoveryApplication struct {
	target string
	main   bool
}

func discoveryInterpreterApplications(target string, arguments []string) []discoveryApplication {
	name := strings.ToLower(filepath.Base(filepath.FromSlash(strings.ReplaceAll(target, `\`, "/"))))
	if name == "powershell" || name == "powershell.exe" || name == "pwsh" || name == "pwsh.exe" {
		for i := 0; i+1 < len(arguments); i++ {
			if strings.EqualFold(arguments[i], "-Command") || strings.EqualFold(arguments[i], "-EncodedCommand") {
				return nil
			}
			if strings.EqualFold(arguments[i], "-File") {
				return []discoveryApplication{{target: strings.Trim(arguments[i+1], `"'`), main: true}}
			}
		}
		return nil
	}
	if !discoveryRuntimeTarget(target) && name != "python" && name != "python3" && name != "python.exe" && name != "python3.exe" {
		return nil
	}
	var applications []discoveryApplication
	for i := 0; i < len(arguments); i++ {
		argument := strings.Trim(arguments[i], `"'`)
		if strings.HasPrefix(argument, "-") {
			if strings.HasPrefix(argument, "--eval=") || strings.HasPrefix(argument, "--print=") {
				return applications
			}
			if len(argument) > 2 && (strings.HasPrefix(argument, "-e") || strings.HasPrefix(argument, "-p") || strings.HasPrefix(argument, "-c") || strings.HasPrefix(argument, "-m")) {
				return applications
			}
			switch argument {
			case "-e", "--eval", "-p", "--print", "-c", "-m":
				return applications
			case "-r", "--require", "--loader", "--import":
				i++
				if i < len(arguments) {
					applications = append(applications, discoveryApplication{target: strings.Trim(arguments[i], `"'`)})
				}
			default:
				for _, prefix := range []string{"--require=", "--loader=", "--import="} {
					if value, ok := strings.CutPrefix(argument, prefix); ok {
						applications = append(applications, discoveryApplication{target: strings.Trim(value, `"'`)})
					}
				}
			}
			continue
		}
		switch strings.ToLower(filepath.Ext(argument)) {
		case ".js", ".mjs", ".cjs", ".py", ".ps1":
			return append(applications, discoveryApplication{target: argument, main: true})
		default:
			return applications // The first positional argument is the program, not later CLI data.
		}
	}
	return applications
}
