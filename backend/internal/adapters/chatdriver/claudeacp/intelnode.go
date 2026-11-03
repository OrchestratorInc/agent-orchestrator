package claudeacp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Intel Macs (#3879, #6621): V8 on x64 makes JIT code executable with
// mprotect, which the hardened runtime allows only with the
// allow-unsigned-executable-memory entitlement. A release whose signer drops
// that entitlement ships a bundled Node that answers `node --version` but
// dies (exit 133, "Check failed: 12 == (*__error())") as soon as V8 tiers up,
// so the ACP adapter disconnects before answering initialize and every Chat
// session fails to start.
//
// The signing fix belongs in the release pipeline, but users must not be
// stranded until a fixed build reaches them. On darwin/amd64 the daemon
// therefore smoke-tests the bundled Node once; if it shows that crash, it
// copies the binary under the AO data dir, ad-hoc re-signs the copy with the
// entitlements it needs, and launches the adapter with the copy. The app
// bundle itself is never modified, so its signature stays intact.

// jitSmokeScript runs long enough for V8 to tier the loop up to optimized code.
const jitSmokeScript = `let s=0;for(let i=0;i<1e8;i++)s+=i;console.log("ok")`

const jitSmokeTimeout = 60 * time.Second

// nodeJITCrashMarker is V8's fatal check from OS::SetPermissions when the
// kernel refuses to make JIT pages executable (errno 12, ENOMEM).
const nodeJITCrashMarker = "Check failed: 12 == (*__error())"

const nodeJITEntitlements = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>com.apple.security.cs.allow-jit</key>
	<true/>
	<key>com.apple.security.cs.allow-unsigned-executable-memory</key>
	<true/>
</dict>
</plist>
`

// Seams for tests; production values target the real macOS toolchain.
var (
	jitRepairGOOS   = runtime.GOOS
	jitRepairGOARCH = runtime.GOARCH
	runNodeSmoke    = defaultRunNodeSmoke
	codesignNode    = defaultCodesignNode
	jitRepairRoot   = defaultJITRepairRoot
)

// jitNodes caches the Node to launch for each bundled Node path once that
// choice is settled, so the smoke test and any repair run once per daemon.
// Only settled choices are kept: an inconclusive smoke test (a timeout, a
// Node that could not start) or a failed repair is retried on the next start.
var (
	jitNodesMu sync.Mutex
	jitNodes   = map[string]string{}
)

// jitCapableNode returns the Node executable the adapter should run in place
// of node. Outside Intel macOS it is node itself.
func jitCapableNode(ctx context.Context, node string) (string, error) {
	if jitRepairGOOS != "darwin" || jitRepairGOARCH != "amd64" {
		return node, nil
	}
	jitNodesMu.Lock()
	defer jitNodesMu.Unlock()
	if cached, ok := jitNodes[node]; ok {
		return cached, nil
	}
	resolved, settled, err := resolveJITCapableNode(ctx, node)
	if err == nil && settled {
		jitNodes[node] = resolved
	}
	return resolved, err
}

// resolveJITCapableNode picks the Node to launch. settled is false when the
// choice rests on an inconclusive smoke test and should be made again later.
func resolveJITCapableNode(ctx context.Context, node string) (string, bool, error) {
	crashed, err := nodeCrashesOnJIT(ctx, node)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", false, ctxErr
		}
		// A smoke test that cannot run says nothing about the entitlement:
		// launch the bundled Node this time and let any real failure surface
		// through the normal ACP error path, but check again next start.
		return node, false, nil
	}
	if !crashed {
		return node, true, nil
	}
	repaired, err := repairNodeJIT(ctx, node)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", false, ctxErr
		}
		return "", false, fmt.Errorf("packaged Node at %s crashes when V8 compiles JavaScript on this Intel Mac "+
			"(its signature lacks com.apple.security.cs.allow-unsigned-executable-memory), and AO could not "+
			"re-sign a copy: %w. Switch the session to Terminal mode until an updated AO build is installed", node, err)
	}
	return repaired, true, nil
}

// nodeCrashesOnJIT reports whether node dies with the missing-entitlement V8
// crash. A false result with a nil error means the smoke test passed.
func nodeCrashesOnJIT(ctx context.Context, node string) (bool, error) {
	exitCode, stderr, err := runNodeSmoke(ctx, node)
	if err != nil {
		return false, err
	}
	if exitCode == 0 {
		return false, nil
	}
	// 133 is 128+SIGTRAP, how V8's fatal check ends the process.
	return exitCode == 133 || strings.Contains(stderr, nodeJITCrashMarker), nil
}

// nodeRunsJIT requires a successful smoke run. The bundled Node is judged by
// nodeCrashesOnJIT, which ignores unrelated failures, but a re-signed copy AO
// installs must actually work, or Chat would launch an unusable Node.
func nodeRunsJIT(ctx context.Context, node string) error {
	exitCode, stderr, err := runNodeSmoke(ctx, node)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		detail := strings.TrimSpace(stderr)
		if lines := strings.Split(detail, "\n"); len(lines) > 3 {
			detail = strings.Join(lines[len(lines)-3:], "\n")
		}
		return fmt.Errorf("exit %d: %s", exitCode, detail)
	}
	return nil
}

func repairNodeJIT(ctx context.Context, node string) (string, error) {
	sum, err := fileSHA256(node)
	if err != nil {
		return "", err
	}
	root, err := jitRepairRoot()
	if err != nil {
		return "", err
	}
	// Keyed by the source binary's digest, so an AO update with a new Node
	// gets a fresh copy instead of reusing a stale one.
	finalDir := filepath.Join(root, sum[:16])
	finalNode := filepath.Join(finalDir, "node")
	if _, statErr := os.Stat(finalNode); statErr == nil {
		if nodeRunsJIT(ctx, finalNode) == nil {
			return finalNode, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := os.RemoveAll(finalDir); err != nil {
			return "", fmt.Errorf("remove stale re-signed Node: %w", err)
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", root, err)
	}
	stage, err := os.MkdirTemp(root, sum[:16]+".tmp-")
	if err != nil {
		return "", fmt.Errorf("stage re-signed Node: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	stagedNode := filepath.Join(stage, "node")
	// A plain byte copy carries no extended attributes, so the copy has no
	// quarantine flag for Gatekeeper to assess.
	if err := copyExecutable(node, stagedNode); err != nil {
		return "", err
	}
	entitlements := filepath.Join(stage, "node.entitlements")
	if err := os.WriteFile(entitlements, []byte(nodeJITEntitlements), 0o600); err != nil {
		return "", fmt.Errorf("write Node entitlements: %w", err)
	}
	if err := codesignNode(ctx, stagedNode, entitlements); err != nil {
		return "", err
	}
	if err := nodeRunsJIT(ctx, stagedNode); err != nil {
		return "", fmt.Errorf("re-signed Node does not run JavaScript: %w", err)
	}
	if err := os.Rename(stage, finalDir); err != nil {
		return "", fmt.Errorf("install re-signed Node: %w", err)
	}
	return finalNode, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // AO's own packaged Node runtime.
	if err != nil {
		return "", fmt.Errorf("read packaged Node: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read packaged Node: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // AO's own packaged Node runtime.
	if err != nil {
		return fmt.Errorf("copy packaged Node: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755) //nolint:gosec // Executable copy under AO's data dir.
	if err != nil {
		return fmt.Errorf("copy packaged Node: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy packaged Node: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("copy packaged Node: %w", err)
	}
	return nil
}

func defaultRunNodeSmoke(ctx context.Context, node string) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, jitSmokeTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, node, "-e", jitSmokeScript) //nolint:gosec // AO's own packaged Node runtime.
	// Start from a clean NODE_OPTIONS so the user's flags cannot mask or cause
	// the result; the adapter launch keeps its own environment.
	cmd.Env = append(os.Environ(), "NODE_OPTIONS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, "", fmt.Errorf("run Node JIT smoke test: %w", ctxErr)
	}
	if err == nil {
		return 0, stderr.String(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code < 0 && strings.Contains(exitErr.String(), "trap") {
			// Killed by SIGTRAP without a shell to translate it into 133.
			code = 133
		}
		return code, stderr.String(), nil
	}
	return 0, "", fmt.Errorf("run Node JIT smoke test: %w", err)
}

func defaultCodesignNode(ctx context.Context, node, entitlements string) error {
	cmd := aoprocess.CommandContext(ctx, "/usr/bin/codesign", "--force", "--sign", "-", "--options", "runtime",
		"--entitlements", entitlements, node)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("codesign: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// defaultJITRepairRoot keeps the re-signed copy under AO's data dir
// (AO_DATA_DIR, default ~/.ao/data), never an OS app-data location.
func defaultJITRepairRoot() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("AO_DATA_DIR")); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("resolve AO_DATA_DIR: %w", err)
		}
		return filepath.Join(abs, "acp-runtime", "node-jit"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve AO data dir: %w", err)
	}
	return filepath.Join(home, ".ao", "data", "acp-runtime", "node-jit"), nil
}
