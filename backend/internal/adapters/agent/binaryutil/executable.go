package binaryutil

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

const (
	discoveryLauncherReadLimit = 64 << 10
	discoveryLauncherFileLimit = 32
)

type discoveryInspection struct {
	active    map[string]bool
	completed map[string]discoveryInspectionResult
}

type discoveryInspectionResult struct {
	verified bool
	native   bool
}

// VerifyExecutable inspects an agent launcher before a background probe runs.
// It returns the original absolute launcher path, preserving alias semantics.
//
// Agent installers can replace another tool's wrapper while retaining its name.
// Inspect launchers without running even --version: a redirected probe can open
// an interactive login before printing anything. This is a local routing check,
// not an attestation of arbitrary executable contents or a full shell evaluator.
// Opaque native copies/renames cannot be authenticated by their launch paths.
func VerifyExecutable(ctx context.Context, agentID, binaryPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	resolved, err := exec.LookPath(binaryPath)
	if err != nil {
		return "", fmt.Errorf("%s executable verification: resolve executable: %w", agentID, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	// Inspection follows symlinks internally. Keep the selected launcher path
	// for execution so aliases retain argv[0] and package entry-point semantics.
	inspection := &discoveryInspection{active: map[string]bool{}, completed: map[string]discoveryInspectionResult{}}
	verified, err := inspectDiscoveryExecutable(ctx, agentID, resolved, inspection)
	if err != nil {
		return "", fmt.Errorf("%s executable verification: refusing executable %q: %w; repair or reinstall the intended agent's launcher", agentID, resolved, err)
	}
	if !verified {
		return "", fmt.Errorf("%s executable verification: cannot verify executable %q belongs to %s; use that agent's own launcher instead of a shared agent alias", agentID, resolved, agentID)
	}
	return resolved, nil
}

// These are executable/package names, not words to search for in help output.
// OpenCode's two registry entries intentionally share the same executable.
var discoveryExecutableOwners = map[string]string{
	"aider": "aider", "amp": "amp", "ampcode": "amp", "auggie": "auggie",
	"autohand": "autohand", "autohand-code": "autohand", "autohand-cli": "autohand", "ah": "autohand",
	"claude": "claude-code", "claude-code": "claude-code", "codex": "codex",
	"copilot": "copilot", "crush": "crush", "cursor-agent": "cursor",
	"grok": "grok", "grok-cli": "grok", "agy": "agy", "antigravity": "agy",
	"kilocode": "kilocode", "kilo": "kilocode", "pi": "pi", "kimchi": "kimchi",
	"prime-agent": "prime-agent", "kimi": "kimi", "kiro": "kiro", "kiro-cli": "kiro",
	"omp": "omp", "codewhale": "codewhale", "codew": "codewhale", "droid": "droid", "fx": "fx",
	"pi-coding-agent": "pi",
	"mimo":            "mimo-code", "mimo-code": "mimo-code",
	"opencode": "opencode", "opencode2": "opencode", "opencode-ai": "opencode",
	"mimo-ai":      "mimo-code",
	"command-code": "command-code", "cmdc": "command-code",
	"gemini": "gemini", "gemini-cli": "gemini", "qwen": "qwen", "qwen-code": "qwen", "goose": "goose",
	"cline": "cline", "cn": "continue", "continuedev": "continue", "dsh": "deepseek-harness", "deepseek-harness": "deepseek-harness",
	"devin": "devin", "hermes": "hermes", "hermes-agent": "hermes", "memcode": "memcode", "muse": "muse",
	"openhands": "openhands", "vibe": "vibe", "mistral-vibe": "vibe", "unreal-agent": "unreal-agent",
}

func discoveryExecutableOwner(path string) string {
	path = strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	path = strings.ReplaceAll(path, "%~dp0", "/")
	packageOwner := discoveryPackageOwner(path)
	name := path[strings.LastIndex(path, "/")+1:]
	for _, ext := range []string{".exe", ".com", ".cmd", ".bat", ".ps1", ".js", ".mjs", ".cjs", ".py"} {
		name = strings.TrimSuffix(name, ext)
	}
	if owner := discoveryExecutableOwners[name]; owner != "" {
		if name == "pi-coding-agent" && packageOwner == "omp" {
			return packageOwner
		}
		return owner
	}
	if match := discoveryVersionedExecutable.FindStringSubmatch(name); len(match) > 1 {
		if owner := discoveryExecutableOwners[match[1]]; owner != "" {
			return owner
		}
	}
	if name == "cmd" && !strings.HasSuffix(path, ".exe") {
		return "command-code"
	}
	if packageOwner != "" {
		return packageOwner
	}
	// Cursor's native installer retains an agent.ps1 entry point beside its
	// cursor-agent launcher. Inspect it below even when its directory identifies
	// it: an Autohand overwrite in that directory must still be rejected.
	if name == "agent" {
		for _, part := range strings.Split(path, "/") {
			if part == "cursor-agent" {
				return "cursor"
			}
		}
	}
	return ""
}

func discoveryPackageOwner(path string) string {
	path = strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	index := strings.LastIndex(path, "/node_modules/")
	if index < 0 {
		return ""
	}
	parts := strings.Split(path[index+len("/node_modules/"):], "/")
	if strings.HasPrefix(parts[0], "@") && len(parts) > 1 {
		// Scope is identity: OMP and Pi publish the same unscoped package name.
		switch parts[0] + "/" + parts[1] {
		case "@oh-my-pi/pi-coding-agent":
			return "omp"
		case "@mariozechner/pi-coding-agent", "@earendil-works/pi-coding-agent":
			return "pi"
		case "@kilocode/cli":
			return "kilocode"
		case "@mimo-ai/cli":
			return "mimo-code"
		case "@factory/cli":
			return "droid"
		}
		parts = parts[:2]
	} else {
		parts = parts[:1]
	}
	for _, part := range parts {
		if owner := discoveryExecutableOwners[strings.TrimPrefix(part, "@")]; owner != "" {
			return owner
		}
	}
	return ""
}

var discoveryVersionedExecutable = regexp.MustCompile(`^([a-z][a-z0-9-]*?)-(?:v?\d|windows-|darwin-|linux-)`)

func inspectDiscoveryExecutable(ctx context.Context, agentID, path string, inspection *discoveryInspection) (verified bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if agentID == "opencode-v2" {
		agentID = "opencode"
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	if inspection.active[canonical] || len(inspection.active) >= 8 {
		return false, fmt.Errorf("launcher chain is cyclic or too deep")
	}
	if result, ok := inspection.completed[canonical]; ok {
		return result.verified || (result.native && discoveryExecutableOwner(path) == agentID), nil
	}
	if len(inspection.active)+len(inspection.completed) >= discoveryLauncherFileLimit {
		return false, fmt.Errorf("launcher exceeds total file inspection limit (%d)", discoveryLauncherFileLimit)
	}
	inspection.active[canonical] = true
	native := false
	defer func() {
		delete(inspection.active, canonical)
		if err == nil {
			result := discoveryInspectionResult{verified: verified, native: native}
			if native {
				// Cache canonical identity only. Alias evidence belongs to this
				// invocation, not every other link to the same manager binary.
				result.verified = discoveryExecutableOwner(canonical) == agentID
			}
			inspection.completed[canonical] = result
		}
	}()
	owner := discoveryExecutableOwner(canonical)
	if owner != "" && owner != agentID {
		return false, fmt.Errorf("launcher targets %s instead of %s", owner, agentID)
	}
	verified = owner == agentID
	file, err := os.Open(canonical)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("launcher is not a regular file")
	}
	// The package-owned application behind an npm shim is not another launcher;
	// scanning its implementation would mistake provider integrations for redirects.
	ext := strings.ToLower(filepath.Ext(canonical))
	application := ext == ".js" || ext == ".mjs" || ext == ".cjs" || ext == ".py"
	if application && strings.Contains(strings.ReplaceAll(canonical, `\`, "/"), "/node_modules/") && verified {
		return true, nil
	}
	body, err := io.ReadAll(io.LimitReader(file, discoveryLauncherReadLimit+1))
	if err != nil {
		return false, err
	}
	// Extensionless npm bin entries are JavaScript applications too. Parsing
	// their // comments and implementation as shell commands invents redirects.
	if discoveryPackageOwner(canonical) == agentID && discoveryNodeShebang(body) {
		return true, nil
	}
	// Native executables are opaque. Version managers can expose an agent-named
	// symlink to a shared native shim, so retain the selected entry point as
	// routing evidence. Known foreign canonical owners were rejected above;
	// script launchers still require inspection and never use this fallback.
	launcher := ext == ".cmd" || ext == ".bat" || ext == ".ps1"
	if !launcher && (bytes.HasPrefix(body, []byte("MZ")) || bytes.HasPrefix(body, []byte("\x7fELF")) || bytes.ContainsRune(body, '\x00')) {
		native = true
		return verified || discoveryExecutableOwner(path) == agentID, nil
	}
	if len(body) > discoveryLauncherReadLimit {
		return false, fmt.Errorf("cannot verify launcher identity: text exceeds inspection limit (%d bytes)", discoveryLauncherReadLimit)
	}
	if bytes.HasPrefix(body, []byte{0xff, 0xfe}) || bytes.HasPrefix(body, []byte{0xfe, 0xff}) {
		var order binary.ByteOrder = binary.LittleEndian
		if body[0] == 0xfe {
			order = binary.BigEndian
		}
		units := make([]uint16, (len(body)-2)/2)
		for i := range units {
			units[i] = order.Uint16(body[2+i*2:])
		}
		body = []byte(string(utf16.Decode(units)))
	}
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	if bytes.ContainsRune(body, '\x00') {
		return false, fmt.Errorf("launcher is not inspectable text")
	}
	if handled, managerErr := inspectDiscoveryManagerShim(ctx, agentID, path, body, inspection); handled {
		return managerErr == nil, managerErr
	}
	variables := map[string]string{}
	batch := (ext == ".cmd" || ext == ".bat") && !bytes.HasPrefix(body, []byte("#!"))
	variableBytes := 0
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "@"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "::") || strings.HasPrefix(strings.ToLower(line), "rem ") {
			continue
		}
		assignmentLine := line
		batchSet := batch && strings.HasPrefix(strings.ToUpper(line), "SET ")
		if batchSet {
			assignmentLine = strings.Trim(strings.TrimSpace(line[4:]), `"`)
		}
		if assignment := discoveryLauncherAssignment.FindStringSubmatch(assignmentLine); len(assignment) > 2 {
			name := assignment[1]
			if batchSet {
				name = strings.ToUpper(name)
			}
			value, expandErr := discoveryExpandLauncherTarget(strings.Trim(assignment[2], `"'`), variables)
			if expandErr != nil {
				return false, expandErr
			}
			variableBytes += len(value) - len(variables[name])
			if variableBytes > discoveryLauncherReadLimit {
				return false, fmt.Errorf("launcher variable data exceeds inspection limit")
			}
			variables[name] = value
		}
		if strings.HasPrefix(line, "set -- ") {
			if arguments := discoveryLauncherTokens.FindAllString(strings.TrimPrefix(line, "set -- "), -1); len(arguments) > 0 {
				value, expandErr := discoveryExpandLauncherTarget(strings.Trim(arguments[0], `"'`), variables)
				if expandErr != nil {
					return false, expandErr
				}
				variableBytes += len(value) - len(variables["@"])
				if variableBytes > discoveryLauncherReadLimit {
					return false, fmt.Errorf("launcher variable data exceeds inspection limit")
				}
				variables["@"] = value
			}
		}
		var references []string
		explicitTargets := map[string]bool{}
		ownershipTargets := map[string]bool{}
		for _, invocation := range discoveryLauncherInvocations(line, batch) {
			if invocation.refusal != "" {
				return false, fmt.Errorf("%s", invocation.refusal)
			}
			target, expandErr := discoveryExpandLauncherTarget(invocation.target, variables)
			if expandErr != nil {
				return false, expandErr
			}
			references = append(references, target)
			explicitTargets[target] = explicitTargets[target] || invocation.explicit
			ownershipTargets[target] = ownershipTargets[target] || !invocation.dependency
			for _, application := range discoveryInterpreterApplications(target, invocation.arguments) {
				applicationTarget, expandErr := discoveryExpandLauncherTarget(application.target, variables)
				if expandErr != nil {
					return false, expandErr
				}
				references = append(references, applicationTarget)
				explicitTargets[applicationTarget] = true
				ownershipTargets[applicationTarget] = ownershipTargets[applicationTarget] || application.main
			}
		}
		lineReferences := make(map[string]bool, len(references))
		for _, reference := range references {
			reference = strings.Trim(reference, `"'`)
			if lineReferences[reference] {
				continue
			}
			lineReferences[reference] = true
			targetOwner := discoveryExecutableOwner(reference)
			if targetOwner != "" && targetOwner != agentID {
				return false, fmt.Errorf("launcher redirects to %s instead of %s", targetOwner, agentID)
			}
			if ownershipTargets[reference] {
				verified = verified || targetOwner == agentID
			}
			target := discoveryLauncherTarget(canonical, reference)
			ext := strings.ToLower(filepath.Ext(reference))
			script := ext == ".cmd" || ext == ".bat" || ext == ".ps1" || ext == ".sh"
			// Wrapper ownership cannot vouch for an unidentified script or
			// extensionless delegate. Do not guess the shell's PATH/PATHEXT
			// or working directory: require a path we can actually inspect.
			requireInspection := targetOwner == "" && (script || (explicitTargets[reference] && ext == "" && !discoveryRuntimeTarget(reference)))
			if target == "" {
				if requireInspection {
					return false, fmt.Errorf("cannot verify launcher command %q; use an explicit agent-owned launcher path", reference)
				}
				continue
			}
			if _, statErr := os.Stat(target); statErr != nil {
				if requireInspection {
					return false, fmt.Errorf("cannot verify launcher command %q: %w", reference, statErr)
				}
				continue
			}
			targetVerified, inspectErr := inspectDiscoveryExecutable(ctx, agentID, target, inspection)
			if inspectErr != nil {
				return false, inspectErr
			}
			if ownershipTargets[reference] {
				verified = verified || targetVerified
			}
		}
	}
	return verified, nil
}

func discoveryNodeShebang(body []byte) bool {
	line, _, _ := strings.Cut(string(body), "\n")
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "#!"))
	if !bytes.HasPrefix(body, []byte("#!")) || len(fields) == 0 {
		return false
	}
	program := fields[0]
	if filepath.Base(program) == "env" && len(fields) > 1 {
		program = fields[1]
	}
	return program == "node" || program == "bun" || filepath.Base(program) == "node" || filepath.Base(program) == "bun"
}

func discoveryLauncherTarget(launcher, reference string) string {
	reference = strings.ReplaceAll(reference, `\`, "/")
	for _, prefix := range []string{"%~dp0", "%dp0%", "%SCRIPT_DIR%", "$PSScriptRoot", "$scriptPath", "$basedir", "${basedir}"} {
		if strings.HasPrefix(strings.ToLower(reference), strings.ToLower(prefix)) {
			relative := strings.TrimPrefix(reference[len(prefix):], "/")
			if relative == "" {
				return "" // A directory-variable assignment is not an executable.
			}
			return filepath.Join(filepath.Dir(launcher), filepath.FromSlash(relative))
		}
	}
	if strings.ContainsAny(reference, "%$") {
		return ""
	}
	if filepath.IsAbs(filepath.FromSlash(reference)) {
		return filepath.FromSlash(reference)
	}
	return ""
}
