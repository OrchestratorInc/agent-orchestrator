// Package hermes supervises the official Hermes Agent classic interactive CLI.
// It uses native invocation configuration and AO-titled native sessions, never
// installs global plugins or changes native safety/approval implementations.
package hermes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite" // Register the driver for read-only native history queries.
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Plugin supervises the official native interactive terminal.
type Plugin struct{ agentbase.Base }

// Revalidate these chrome markers whenever qualifiedReleaseLines changes. AO
// launches Hermes with HERMES_LANGUAGE=en so the busy placeholder is English.
const (
	nativePrompt         = "❯"
	nativeNamedPrompt    = "☤ ❯"
	nativeWarningPrompt  = "⚠ ❯"
	nativeQuestionPrompt = "? ❯"
	nativeUserEcho       = "● "
	nativeCommandEcho    = "⚙️  "
	nativeTranscriptRule = "────────────────────────────────────────"
)

var (
	// Source and Homebrew installs append a date, channel, or commit after a space.
	nativeVersionLine   = regexp.MustCompile(`^Hermes Agent v([0-9]+)\.([0-9]+)\.([0-9]+)(?:\+[0-9A-Za-z.]+)?(?: .*)?$`)
	titleUnsafe         = regexp.MustCompile(`[^A-Za-z0-9._-]`)
	nativeStyleSequence = regexp.MustCompile(`\x1b\[[0-9;:]*m`)
	// A tagless clean checkout; a ".dirty" suffix never matches.
	nativeSourceVersionLine = regexp.MustCompile(`^Hermes Agent vgit\.([0-9a-f]{7,40})(?: .*)?$`)
	// A Homebrew bottle is built from the release tarball, which has no git
	// metadata, so Hermes's git-derived banner version reads "unknown".
	nativeUnknownVersionLine = regexp.MustCompile(`^Hermes Agent vunknown(?: .*)?$`)
	// A Homebrew keg directory names the formula version, plus `_N` when the
	// formula was revised without a new upstream release.
	homebrewKegDir   = regexp.MustCompile(`^(\d+\.\d+\.\d+)(?:_\d+)?$`)
	stableReleaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
)

// systemLaunchers are the package-manager install locations ResolveBinary
// probes before the per-user ones. Tests clear it so a host's own Hermes
// install cannot take precedence over a fixture.
var systemLaunchers = []string{"/opt/homebrew/bin/hermes", "/usr/local/bin/hermes", "/home/linuxbrew/.linuxbrew/bin/hermes", "/usr/bin/hermes"}

// New creates the native TUI adapter.
func New() *Plugin { return &Plugin{} }

// Manifest implements the scoped native adapter contract.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "hermes", Name: "Hermes Agent", Description: "Official Nous Research Hermes Agent interactive terminal; native approvals and exact session restore.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// ResolveBinary implements the scoped native adapter contract.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	timedOut := false
	var unconfirmed []string
	validate := func(ctx context.Context, path string) bool {
		if identityConfirmed(ctx, path) {
			return true
		}
		// Retry once: a probe can fail transiently on a loaded machine.
		var reason string
		for range 2 {
			ok, why, timeout := probeNativeVersion(ctx, path)
			if ok {
				rememberIdentity(path)
				return true
			}
			if timeout {
				timedOut = true
				return false
			}
			if ctx.Err() != nil {
				return false
			}
			reason = why
		}
		// A fresh rejection outranks an earlier confirmation of this launcher.
		forgetIdentity(path)
		unconfirmed = append(unconfirmed, path+": "+reason)
		return false
	}
	// The official installers publish launchers under a custom HERMES_HOME.
	for _, candidate := range customHomeCandidates() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() && validate(ctx, candidate) {
			return candidate, nil
		}
	}
	// Probe only the official installers' locations. A PATH-first --version probe
	// would execute an unrelated earlier binary during passive agent discovery.
	path, err := binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "hermes", UnixPaths: systemLaunchers, UnixHomePaths: [][]string{
		{".local", "bin", "hermes"},
		{".hermes", "hermes-agent", ".hermes", "bin", "hermes"},
		{".hermes", "hermes-agent", ".venv", "bin", "hermes"},
	}, WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinLocalAppData, Parts: []string{"hermes", "bin", "hermes.exe"}},
		{Base: binaryutil.WinLocalAppData, Parts: []string{"hermes", "bin", "hermes.cmd"}},
		{Base: binaryutil.WinLocalAppData, Parts: []string{"hermes", "hermes-agent", "venv", "Scripts", "hermes.exe"}},
	}, ValidateIdentity: validate})
	// A slow probe is not proof of absence: report the timeout so readiness
	// retries instead of recording Hermes as not installed.
	if err != nil && timedOut {
		return "", fmt.Errorf("hermes: native identity check timed out: %w", context.DeadlineExceeded)
	}
	// An executable at an official install location that will not confirm its
	// version is present but unverified, not absent.
	if errors.Is(err, ports.ErrAgentBinaryNotFound) && len(unconfirmed) > 0 {
		return "", fmt.Errorf("hermes: %s: %w", strings.Join(unconfirmed, "; "), ports.ErrAgentBinaryIdentityUnknown)
	}
	return path, err
}

// customHomeCandidates are the launchers the official installers publish when
// HERMES_HOME is not the platform default.
func customHomeCandidates() []string {
	home, err := ResolveHome(nil)
	if err != nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" && strings.EqualFold(filepath.Clean(home), filepath.Join(local, "hermes")) {
			return nil
		}
		return []string{filepath.Join(home, "bin", "hermes.exe"), filepath.Join(home, "bin", "hermes.cmd"), filepath.Join(home, "hermes-agent", "venv", "Scripts", "hermes.exe")}
	}
	if userHome, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == filepath.Join(userHome, ".hermes") {
		return nil
	}
	return []string{filepath.Join(home, "hermes-agent", ".hermes", "bin", "hermes")}
}

// confirmedIdentities remembers launchers whose --version banner passed, so a
// spawn or restore right after readiness's launch check reuses that probe
// instead of waiting on another Python start. A launcher can forward to code
// that changes without touching the launcher file, so an entry is also keyed
// on the installation's revision and expires after confirmedIdentityTTL.
var confirmedIdentities sync.Map

// confirmedIdentityTTL is long enough to span AO's launch readiness check and
// the spawn that follows it, and short enough that later launches re-probe.
const confirmedIdentityTTL = time.Minute

type binaryIdentity struct {
	target   string
	size     int64
	modTime  time.Time
	revision string
}

type confirmedIdentity struct {
	binaryIdentity
	at time.Time
}

func currentIdentity(path string) (binaryIdentity, bool) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return binaryIdentity{}, false
	}
	st, err := os.Stat(target)
	if err != nil {
		return binaryIdentity{}, false
	}
	return binaryIdentity{target: target, size: st.Size(), modTime: st.ModTime(), revision: installRevision(path)}, true
}

// installRevision names the commit of the source checkout a launcher runs,
// falling back to the official installation under HERMES_HOME. Homebrew
// installs are already distinguished by their versioned Cellar target.
func installRevision(launcher string) string {
	if root := launcherSourceRoot(launcher); root != "" {
		return gitHead(root)
	}
	home, err := ResolveHome(nil)
	if err != nil {
		return ""
	}
	return gitHead(filepath.Join(home, "hermes-agent"))
}

// launcherSourceRoot follows the official source launchers to the checkout
// they run: the public wrapper is `exec <root>/.hermes/bin/hermes "$@"`, and
// a venv console script lives in <root>/venv/bin or <root>/.venv/bin. Hermes
// takes its project .env from that checkout. It returns "" when the launcher
// is not one of these, such as a Homebrew bottle.
func launcherSourceRoot(launcher string) string {
	path := launcher
	for range 4 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return ""
		}
		bin := filepath.Dir(target)
		switch parent := filepath.Base(filepath.Dir(bin)); {
		case filepath.Base(bin) == "bin" && parent == ".hermes",
			filepath.Base(bin) == "bin" && (parent == "venv" || parent == ".venv"),
			filepath.Base(bin) == "Scripts" && parent == "venv":
			return filepath.Dir(filepath.Dir(bin))
		}
		next := wrapperTarget(target)
		if next == "" {
			return ""
		}
		path = next
	}
	return ""
}

// wrapperTarget reads the program a published `#!/bin/sh` wrapper execs.
func wrapperTarget(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, 4096)
	n, _ := f.Read(b)
	text := string(b[:n])
	if !strings.HasPrefix(text, "#!/bin/sh\n") {
		return ""
	}
	for _, line := range strings.Split(text, "\n")[1:] {
		if rest, ok := strings.CutPrefix(line, "exec "); ok {
			return firstShellWord(rest)
		}
	}
	return ""
}

// firstShellWord reads the first POSIX shell word, joining adjacent quoted
// and unquoted segments as the shell does. shlex.join writes an apostrophe
// as '"'"', so /a/Owner's Hermes becomes '/a/Owner'"'"'s Hermes'.
func firstShellWord(text string) string {
	var word strings.Builder
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case ' ', '\t':
			return word.String()
		case '\'':
			end := strings.IndexByte(text[i+1:], '\'')
			if end < 0 {
				return ""
			}
			word.WriteString(text[i+1 : i+1+end])
			i += end + 1
		case '"':
			i++
			for ; i < len(text) && text[i] != '"'; i++ {
				if text[i] == '\\' && i+1 < len(text) && strings.IndexByte("$`\"\\\n", text[i+1]) >= 0 {
					i++
				} else if text[i] == '$' || text[i] == '`' {
					return "" // expansion: not a literal path
				}
				word.WriteByte(text[i])
			}
			if i >= len(text) {
				return ""
			}
		case '\\':
			if i+1 < len(text) {
				i++
				word.WriteByte(text[i])
			}
		case '$', '`', ';', '&', '|', '<', '>', '(', ')':
			return ""
		default:
			word.WriteByte(c)
		}
	}
	return word.String()
}

// sourceRoots are the checkouts whose project .env Hermes may load: those
// the installed launchers run, plus the official installer's checkout under
// both the effective and the default home.
func sourceRoots(home string, env map[string]string) []string {
	roots := []string{filepath.Join(home, "hermes-agent")}
	defaultEnv := maps.Clone(env)
	if defaultEnv == nil {
		defaultEnv = map[string]string{}
	}
	defaultEnv["HERMES_HOME"] = ""
	if root, err := ResolveHome(defaultEnv); err == nil {
		roots = append(roots, filepath.Join(root, "hermes-agent"))
	}
	defaultEnv["HERMES_DATA_DIR_SUFFIX"] = ""
	if root, err := ResolveHome(defaultEnv); err == nil {
		roots = append(roots, filepath.Join(root, "hermes-agent"))
	}
	for _, launcher := range installedLaunchers() {
		if root := launcherSourceRoot(launcher); root != "" {
			roots = append(roots, root)
		}
	}
	slices.Sort(roots)
	return slices.Compact(roots)
}

// installedLaunchers are the launcher locations ResolveBinary considers.
func installedLaunchers() []string {
	launchers := customHomeCandidates()
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			launchers = append(launchers, filepath.Join(local, "hermes", "bin", "hermes.exe"), filepath.Join(local, "hermes", "bin", "hermes.cmd"), filepath.Join(local, "hermes", "hermes-agent", "venv", "Scripts", "hermes.exe"))
		}
		return launchers
	}
	launchers = append(launchers, "/opt/homebrew/bin/hermes", "/usr/local/bin/hermes", "/home/linuxbrew/.linuxbrew/bin/hermes", "/usr/bin/hermes")
	if userHome, err := os.UserHomeDir(); err == nil {
		launchers = append(launchers,
			filepath.Join(userHome, ".local", "bin", "hermes"),
			filepath.Join(userHome, ".hermes", "hermes-agent", ".hermes", "bin", "hermes"),
			filepath.Join(userHome, ".hermes", "hermes-agent", ".venv", "bin", "hermes"))
	}
	return launchers
}

// gitHead reads a checkout's HEAD commit without running git: a detached SHA,
// or the branch ref from its loose file or packed-refs. A linked worktree keeps
// HEAD in its own git directory and branch refs in the one named by commondir.
func gitHead(checkout string) string {
	gitDir, refsDir := gitDirs(checkout)
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref:")
	if !ok {
		return strings.TrimSpace(string(head))
	}
	ref = strings.TrimSpace(ref)
	for _, dir := range []string{gitDir, refsDir} {
		if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	packed, err := os.ReadFile(filepath.Join(refsDir, "packed-refs"))
	if err != nil {
		return ref
	}
	for _, line := range strings.Split(string(packed), "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref {
			return sha
		}
	}
	return ref
}

// gitDirs locates a checkout's git directory and the directory holding its
// shared refs and objects (the same, except in a linked worktree).
func gitDirs(checkout string) (gitDir, commonDir string) {
	gitDir = filepath.Join(checkout, ".git")
	if b, err := os.ReadFile(gitDir); err == nil {
		if dir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:"); ok {
			gitDir = strings.TrimSpace(dir)
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(checkout, gitDir)
			}
		}
	}
	commonDir = gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		if commonDir = strings.TrimSpace(string(b)); !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	}
	return gitDir, commonDir
}

func identityConfirmed(ctx context.Context, path string) bool {
	current, ok := currentIdentity(path)
	if !ok {
		return false
	}
	// A shell wrapper whose source checkout AO cannot name could forward to
	// changed code with no visible revision, so it is always re-probed.
	// Homebrew's versioned Cellar target already names its release.
	root := launcherSourceRoot(path)
	if !inCellar(current.target) && isShellScript(current.target) && root == "" {
		return false
	}
	remembered, found := confirmedIdentities.Load(path)
	entry, ok := remembered.(confirmedIdentity)
	if !found || !ok || entry.binaryIdentity != current || time.Since(entry.at) >= confirmedIdentityTTL {
		return false
	}
	// A tracked edit changes neither the launcher nor HEAD; the probe's
	// clean-source condition must still hold when its result is reused.
	if root != "" && !sourceUnmodified(ctx, root) {
		return false
	}
	return true
}

func inCellar(path string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "Cellar")
}

func isShellScript(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, 2)
	n, _ := f.Read(b)
	return n == 2 && string(b) == "#!"
}

func rememberIdentity(path string) {
	if current, ok := currentIdentity(path); ok {
		confirmedIdentities.Store(path, confirmedIdentity{binaryIdentity: current, at: time.Now()})
	}
}

func forgetIdentity(path string) { confirmedIdentities.Delete(path) }

// probeNativeVersion runs `hermes --version` and checks its banner. It reports
// why a probe that ran was rejected, and whether it hit its own deadline.
func probeNativeVersion(ctx context.Context, path string) (ok bool, reason string, timedOut bool) {
	// A fresh install's first launch compiles Hermes's Python bytecode, which
	// can take far longer than a warm run; the probe must outlast it.
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := aoprocess.CommandContext(probeCtx, path, "--version")
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 100 * time.Millisecond
	b, err := cmd.CombinedOutput()
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return false, "", true
	}
	firstLine, _, _ := strings.Cut(string(b), "\n")
	firstLine = strings.TrimSuffix(firstLine, "\r")
	if err == nil && supportedNativeVersion(firstLine) {
		// Hermes's release banner carries no dirty marker, so a source
		// checkout with edited tracked files (locales, chrome) still prints
		// it. Refuse when git reports such edits.
		if root := launcherSourceRoot(path); root != "" && !sourceUnmodified(ctx, root) {
			return false, fmt.Sprintf("source checkout %s is modified or its state could not be verified with git; AO qualifies only unmodified releases", root), false
		}
		return true, "", false
	}
	if err == nil && qualifiedSourceCheckout(ctx, path, firstLine) {
		return true, "", false
	}
	if err == nil && qualifiedHomebrewKeg(path, firstLine) {
		return true, "", false
	}
	if len(firstLine) > 120 {
		firstLine = firstLine[:120]
	}
	if err != nil {
		return false, fmt.Sprintf("version probe failed (%v): %q", err, firstLine), false
	}
	return false, fmt.Sprintf("version banner %q is not a Hermes release line AO has qualified", firstLine), false
}

// qualifiedReleaseLines are the major.minor release lines whose composer,
// busy, modal and transcript chrome AO's terminal detection was checked
// against (v0.21.6 on Linux, v0.22.2 on macOS). A newer line stays installed
// but unverified until its markers are revalidated. Only major.minor is
// compared: Hermes derives its banner from git tags, so the same build can
// report v0.21.6 or v0.21.5+N.g<sha> depending on a checkout under HERMES_HOME.
var qualifiedReleaseLines = [][2]int{{0, 21}, {0, 22}}

// qualifiedSourceCheckout accepts the banner Hermes prints when its own git
// lookup finds no release tag (`vgit.<sha>`, e.g. after a shallow or cold
// checkout) if the launcher's source checkout is exactly a qualified release.
func qualifiedSourceCheckout(ctx context.Context, launcher, line string) bool {
	tag := SourceReleaseTag(ctx, launcher, line)
	return tag != "" && supportedNativeVersion("Hermes Agent "+tag)
}

// qualifiedHomebrewKeg accepts the `vunknown` banner a Homebrew bottle prints
// when the launcher resolves into a hermes-agent keg on a qualified line. The
// banner names no release, so Homebrew's own record must: the keg directory
// and its INSTALL_RECEIPT.json have to agree on it. installedVersionText in
// systeminstall already takes a Homebrew install's version from the same keg.
func qualifiedHomebrewKeg(launcher, banner string) bool {
	if !nativeUnknownVersionLine.MatchString(banner) {
		return false
	}
	version := homebrewKegVersion(launcher)
	return version != "" && supportedNativeVersion("Hermes Agent v"+version)
}

// homebrewKegVersion returns the release of the hermes-agent keg a launcher
// resolves into (…/Cellar/hermes-agent/<version>[_<revision>]/…). It returns
// "" when the launcher is not inside such a keg or the keg's
// INSTALL_RECEIPT.json records a different release.
func homebrewKegVersion(launcher string) string {
	target, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(target), "/")
	// The executable lives below the keg directory, never as the keg itself.
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] != "Cellar" || parts[i+1] != "hermes-agent" {
			continue
		}
		m := homebrewKegDir.FindStringSubmatch(parts[i+2])
		if m == nil {
			return ""
		}
		keg := filepath.FromSlash(strings.Join(parts[:i+3], "/"))
		b, err := os.ReadFile(filepath.Join(keg, "INSTALL_RECEIPT.json"))
		if err != nil {
			return ""
		}
		var receipt struct {
			Source struct {
				Versions struct {
					Stable string `json:"stable"`
				} `json:"versions"`
			} `json:"source"`
		}
		if json.Unmarshal(b, &receipt) != nil || receipt.Source.Versions.Stable != m[1] {
			return ""
		}
		return m[1]
	}
	return ""
}

// SourceReleaseTag names the release a tagless `vgit.<sha>` banner was built
// from. The banner alone proves neither: Hermes can print it from a stored
// source stamp. So git itself must report that the launcher's source
// checkout is at the banner's commit, has no tracked modifications, and
// carries an effective vX.Y.Z tag on that commit (preferring a qualified
// line). Asking git covers packed and loose refs, shadowing, annotated tags
// in packs and alternates. It returns "" otherwise, including without git.
func SourceReleaseTag(ctx context.Context, launcher, banner string) string {
	firstLine, _, _ := strings.Cut(banner, "\n")
	m := nativeSourceVersionLine.FindStringSubmatch(strings.TrimSuffix(firstLine, "\r"))
	if m == nil {
		return ""
	}
	root := launcherSourceRoot(launcher)
	if root == "" {
		return ""
	}
	head, ok := runGit(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if !ok || len(head) != 40 || !strings.HasPrefix(head, m[1]) {
		return ""
	}
	if modified, known := sourceModified(ctx, root); !known || modified {
		return ""
	}
	listed, ok := runGit(ctx, root, "tag", "--points-at", head, "--list", "v*")
	if !ok {
		return ""
	}
	var tags []string
	for _, tag := range strings.Split(listed, "\n") {
		if stableReleaseTag.MatchString(tag) {
			tags = append(tags, tag)
		}
	}
	slices.Sort(tags)
	for _, tag := range tags {
		if supportedNativeVersion("Hermes Agent " + tag) {
			return tag
		}
	}
	if len(tags) > 0 {
		return tags[0]
	}
	return ""
}

// sourceUnmodified reports whether a recognized source checkout can be
// qualified: a git checkout must be verified clean, so an unknown answer
// refuses. A tree without .git has no git state to compare.
func sourceUnmodified(ctx context.Context, root string) bool {
	if _, err := os.Lstat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
		return true
	}
	modified, known := sourceModified(ctx, root)
	return known && !modified
}

// sourceModified asks git whether a checkout has modified tracked files, or
// an untracked (including ignored) English catalog in the bundled locales or
// plugins directories Hermes reads from the checkout. known is false when
// git cannot answer, including when the repository git finds is not rooted
// at the checkout itself.
func sourceModified(ctx context.Context, root string) (modified, known bool) {
	// Only git's line terminator is dropped: a checkout path may end in
	// whitespace, which TrimSpace would strip into a different directory.
	top, ok := runGitOutput(ctx, root, "rev-parse", "--show-toplevel")
	top = strings.TrimSuffix(strings.TrimSuffix(top, "\n"), "\r")
	if !ok || !sameDir(top, root) {
		return false, false
	}
	status, ok := runGit(ctx, root, "status", "--porcelain", "--untracked-files=no")
	if !ok {
		return false, false
	}
	if status != "" {
		return true, true
	}
	// Without --exclude-standard, --others also lists ignored files.
	untracked, ok := runGitOutput(ctx, root, "ls-files", "-z", "--others", "--", "locales", "plugins")
	if !ok {
		return false, false
	}
	for _, path := range strings.Split(untracked, "\x00") {
		if path == "" {
			continue
		}
		dir, name := filepath.Split(filepath.FromSlash(path))
		if englishLocaleFile.MatchString(name) && filepath.Base(filepath.Clean(dir)) == "locales" {
			return true, true
		}
		// Git lists an untracked symlink but never what it points to, while
		// native plugin discovery follows it; the release ships none here.
		if st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); err != nil || st.Mode()&os.ModeSymlink != 0 {
			return true, true
		}
	}
	return false, true
}

func sameDir(a, b string) bool {
	a, errA := filepath.EvalSymlinks(a)
	b, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && filepath.Clean(a) == filepath.Clean(b)
}

func runGit(ctx context.Context, root string, args ...string) (string, bool) {
	out, ok := runGitOutput(ctx, root, args...)
	return strings.TrimSpace(out), ok
}

// gitExecutable finds git when the daemon's PATH may be minimal, like
// Hermes's _resolve_git_executable, plus the Git for Windows build the
// official Windows installer pins under <HERMES_HOME>/tools (or
// HERMES_RUNTIME_DIR) without adding it to PATH.
func gitExecutable() string {
	if found, err := exec.LookPath("git"); err == nil {
		return found
	}
	var candidates []string
	if runtime.GOOS == "windows" {
		stores := []string{os.Getenv("HERMES_RUNTIME_DIR")}
		if home, err := ResolveHome(nil); err == nil {
			stores = append(stores, filepath.Join(home, "tools"))
		}
		for _, store := range stores {
			if store != "" {
				pinned, _ := filepath.Glob(filepath.Join(store, "git-*", "cmd", "git.exe"))
				candidates = append(candidates, pinned...)
			}
		}
		roots := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			roots = append(roots, filepath.Join(local, "Programs"))
		}
		for _, root := range roots {
			if root != "" {
				candidates = append(candidates, filepath.Join(root, "Git", "cmd", "git.exe"), filepath.Join(root, "Git", "bin", "git.exe"))
			}
		}
	} else {
		candidates = []string{"/usr/bin/git", "/usr/local/bin/git", "/opt/homebrew/bin/git", "/bin/git"}
	}
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

// runGitOutput preserves raw output, including filename whitespace and NULs.
// It runs a read-only git query in a checkout without taking optional
// locks, so it cannot strand an index.lock in the user's installation.
// AO's own GIT_* variables (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE, ...)
// would redirect the query to another repository, so none are inherited.
func runGitOutput(ctx context.Context, root string, args ...string) (string, bool) {
	gitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	git := gitExecutable()
	if git == "" {
		return "", false
	}
	cmd := aoprocess.CommandContext(gitCtx, git, append([]string{"--no-optional-locks", "-C", root}, args...)...)
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(strings.ToUpper(kv), "GIT_")
	})
	env = append(env, "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	cmd.Env = env
	out, err := cmd.Output()
	return string(out), err == nil
}

// supportedNativeVersion accepts a Hermes --version first line from a
// qualified release line.
func supportedNativeVersion(line string) bool {
	m := nativeVersionLine.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	major, errMajor := strconv.Atoi(m[1])
	minor, errMinor := strconv.Atoi(m[2])
	if errMajor != nil || errMinor != nil {
		return false
	}
	for _, release := range qualifiedReleaseLines {
		if release == [2]int{major, minor} {
			return true
		}
	}
	return false
}

// GetConfigSpec implements the scoped native adapter contract.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelEffortConfigSpec(ctx,
		"Native Hermes --model override; provider and credentials remain native configuration.",
		"Native Hermes --reasoning level: none, minimal, low, medium, high, xhigh, max, or ultra.")
}
func validate(cfg ports.LaunchConfig) error {
	if len(cfg.AllowedTools)+len(cfg.DisallowedTools) > 0 {
		return fmt.Errorf("hermes: AO tool allow/deny restrictions are unsupported")
	}
	return nil
}

// permissionArgs maps AO approval modes onto native Hermes approvals. Hermes has
// no accept-edits or auto tier, so those keep native prompts rather than
// approving more than requested; only explicit bypass skips them.
func permissionArgs(mode ports.PermissionMode) []string {
	if ports.NormalizePermissionMode(mode) == ports.PermissionModeBypassPermissions {
		return []string{"--yolo"}
	}
	return nil
}

// GetLaunchCommand implements the scoped native adapter contract.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	// Every fresh launch mints a new title, so a relaunch never adopts an older
	// conversation; restore reuses the stored one.
	title, err := newSessionTitle(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	cmd, err := p.sessionCommand(ctx, cfg, title, true)
	if err != nil {
		return nil, err
	}
	if cfg.Prompt != "" {
		dir := sessionDir(cfg.DataDir, cfg.SessionID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "initial-query.txt")
		if err := os.WriteFile(path, []byte(cfg.Prompt), 0o600); err != nil {
			return nil, err
		}
		cmd = append(cmd, "--query-file", path)
	}
	return cmd, nil
}
func sessionDir(data, id string) string { return filepath.Join(data, "agents", "hermes", id) }

// sessionCommand starts or resumes the native conversation AO titled for this
// session. Hermes records the title as user-provided, so auto-titling never
// renames it, and resolves it forward through context compression itself.
func (p *Plugin) sessionCommand(ctx context.Context, cfg ports.LaunchConfig, title string, create bool) ([]string, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "chat", "--cli", "--in", cfg.WorkspacePath, "--continue", title}
	if create {
		cmd = append(cmd, "--create-if-missing")
	}
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	agentbase.AppendEffortFlag(&cmd, cfg.Config, "--reasoning")
	return append(cmd, permissionArgs(cfg.Permissions)...), nil
}

// newSessionTitle records a fresh native title for an AO launch. The random
// suffix keeps titles unique when several AO data directories share one
// Hermes home and reuse session IDs.
func newSessionTitle(data, id string) (string, error) {
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	safeID := titleUnsafe.ReplaceAllString(id, "-")
	if len(safeID) > 64 {
		safeID = safeID[:64]
	}
	title := "ao-" + safeID + "-" + hex.EncodeToString(suffix)
	dir := sessionDir(data, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "native-title"), []byte(title), 0o600); err != nil {
		return "", err
	}
	return title, nil
}

func sessionTitle(data, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(sessionDir(data, id), "native-title"))
	if err != nil {
		return "", err
	}
	if title := strings.TrimSpace(string(b)); title != "" {
		return title, nil
	}
	return "", fmt.Errorf("hermes: empty native session title")
}

// ResolveHome mirrors Hermes's own HERMES_HOME default: ~/.hermes on macOS and
// Linux, %LOCALAPPDATA%\hermes on Windows. The launch env wins over the daemon's.
func ResolveHome(env map[string]string) (string, error) {
	lookup := childEnvLookup(env)
	get := func(name string) string {
		value, _ := lookup(name)
		return value
	}
	home, err := nativeHomeFrom(lookup, get)
	if err != nil {
		return "", err
	}
	// Hermes resolves a relative home against its own working directory, the
	// session workspace, so AO would inspect a different directory.
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("hermes: native home %q must be an absolute path; set HERMES_HOME or HOME to an absolute directory", home)
	}
	// Hermes follows a symlink before "..", where filepath.Clean folds it
	// lexically and would name a different directory.
	if slices.Contains(strings.FieldsFunc(home, func(r rune) bool { return r == '/' || r == filepath.Separator }), "..") {
		return "", fmt.Errorf("hermes: native home %q contains a \"..\" component; set HERMES_HOME or HOME to a path without it", home)
	}
	return filepath.Clean(home), nil
}

func nativeHomeFrom(lookup envLookup, get func(string) string) (string, error) {
	if dir := pyStrip(get("HERMES_HOME")); dir != "" {
		return expandNativePath(dir, lookup)
	}
	// _get_platform_default_hermes_home: LOCALAPPDATA (or Path.home() /
	// AppData/Local) on Windows, else Path.home(), with the unstripped
	// HERMES_DATA_DIR_SUFFIX appended to the directory name.
	suffix := get("HERMES_DATA_DIR_SUFFIX")
	if runtime.GOOS == "windows" {
		local := pyStrip(get("LOCALAPPDATA"))
		if local == "" {
			home, err := expandWindowsUser("~", lookup)
			if err != nil || home == "~" {
				return "", fmt.Errorf("hermes: cannot determine the user home directory")
			}
			local = filepath.Join(home, "AppData", "Local")
		}
		return local + string(filepath.Separator) + "hermes" + suffix, nil
	}
	home, err := expandPOSIXUser("~", lookup)
	if err != nil {
		return "", err
	}
	// Not filepath.Join: ResolveHome must see a ".." the suffix introduces.
	return home + string(filepath.Separator) + ".hermes" + suffix, nil
}

// expandNativePath applies Hermes's HERMES_HOME expansion,
// os.path.expanduser(os.path.expandvars(value)), against the child
// environment, so AO reads and records the directory Hermes writes to.
// Undefined variables stay literal, as in Python.
func expandNativePath(value string, lookup envLookup) (string, error) {
	if runtime.GOOS == "windows" {
		return expandWindowsUser(expandWindowsVars(value, lookup), lookup)
	}
	return expandPOSIXUser(expandPOSIXVars(value, lookup), lookup)
}

var posixVarReference = regexp.MustCompile(`\$(\w+|\{[^}]*\})`)

// expandPOSIXVars mirrors posixpath.expandvars: $NAME and ${NAME}, with
// unknown names left unchanged.
func expandPOSIXVars(value string, lookup envLookup) string {
	return posixVarReference.ReplaceAllStringFunc(value, func(ref string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(ref, "$"), "{"), "}")
		if v, ok := lookup(name); ok && name != "" {
			return v
		}
		return ref
	})
}

// expandPOSIXUser mirrors posixpath.expanduser for the current user's ~.
func expandPOSIXUser(value string, lookup envLookup) (string, error) {
	if value != "~" && !strings.HasPrefix(value, "~/") {
		return value, nil
	}
	// posixpath.expanduser uses HOME whenever it is defined, else the
	// password database.
	home, ok := lookup("HOME")
	if !ok {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	return strings.TrimSuffix(home, "/") + value[1:], nil
}

// expandWindowsVars mirrors ntpath.expandvars: %NAME%, $NAME and ${NAME},
// '%%' and '$$' escapes, and single-quoted literal runs; unknown names stay.
func expandWindowsVars(value string, lookup envLookup) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		switch c := value[i]; c {
		case '\'':
			end := strings.IndexByte(value[i+1:], '\'')
			if end < 0 {
				b.WriteString(value[i:])
				return b.String()
			}
			b.WriteString(value[i : i+end+2])
			i += end + 2
		case '%':
			if strings.HasPrefix(value[i:], "%%") {
				b.WriteByte('%')
				i += 2
				continue
			}
			end := strings.IndexByte(value[i+1:], '%')
			if end < 0 {
				b.WriteString(value[i:])
				return b.String()
			}
			name := value[i+1 : i+1+end]
			if v, ok := lookup(name); ok && name != "" {
				b.WriteString(v)
			} else {
				b.WriteString(value[i : i+end+2])
			}
			i += end + 2
		case '$':
			switch {
			case strings.HasPrefix(value[i:], "$$"):
				b.WriteByte('$')
				i += 2
			case strings.HasPrefix(value[i:], "${"):
				end := strings.IndexByte(value[i+2:], '}')
				if end < 0 {
					b.WriteString(value[i:])
					return b.String()
				}
				name := value[i+2 : i+2+end]
				if v, ok := lookup(name); ok && name != "" {
					b.WriteString(v)
				} else {
					b.WriteString(value[i : i+end+3])
				}
				i += end + 3
			default:
				j := i + 1
				for j < len(value) && (value[j] == '_' || value[j] == '-' || unicode.IsLetter(rune(value[j])) || unicode.IsDigit(rune(value[j]))) {
					j++
				}
				if v, ok := lookup(value[i+1 : j]); ok && j > i+1 {
					b.WriteString(v)
				} else {
					b.WriteString(value[i:j])
				}
				i = j
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// expandWindowsUser mirrors ntpath.expanduser for the current user's ~:
// USERPROFILE, then HOMEDRIVE+HOMEPATH. HOME is not consulted.
func expandWindowsUser(value string, lookup envLookup) (string, error) {
	if value != "~" && !strings.HasPrefix(value, "~/") && !strings.HasPrefix(value, `~\`) {
		return value, nil
	}
	home, ok := lookup("USERPROFILE")
	if !ok {
		path, ok := lookup("HOMEPATH")
		if !ok {
			return value, nil // ntpath.expanduser leaves ~ when neither is defined
		}
		drive, _ := lookup("HOMEDRIVE")
		home = drive + path
	}
	return home + value[1:], nil
}

// envLookup reports a variable's value and whether it is defined, so an
// explicitly empty variable expands to empty, as in Python.
type envLookup func(name string) (string, bool)

// childEnvLookup reads the launch environment, then the daemon's. Windows
// variable names are case-insensitive.
func childEnvLookup(env map[string]string) envLookup {
	return func(name string) (string, bool) {
		if value, ok := env[name]; ok {
			return value, true
		}
		if runtime.GOOS == "windows" {
			for key, value := range env {
				if strings.EqualFold(key, name) {
					return value, true
				}
			}
		}
		return os.LookupEnv(name)
	}
}

// GetAgentHooks only supplies process-local additive native configuration. The
// name is AO's existing launch-environment boundary; no native hooks are added.
func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.Env == nil {
		return fmt.Errorf("hermes: launch environment required")
	}
	home, err := ResolveHome(cfg.Env)
	if err != nil {
		return err
	}
	// AO pins the home it checks as an explicit HERMES_HOME, which Hermes strips
	// and expands, unlike the default home's literal data-dir suffix. Refuse a
	// home that pinning would move. A remaining variable reference would expand
	// against the final child environment, including variables AO sets below
	// (HERMES_LANGUAGE), so it is refused outright.
	pinned := maps.Clone(cfg.Env)
	pinned["HERMES_HOME"] = home
	reference := "$"
	if runtime.GOOS == "windows" {
		reference = "$%"
	}
	if again, err := ResolveHome(pinned); err != nil || again != home || strings.ContainsAny(home, reference) {
		return fmt.Errorf("hermes: native home %q changes when set as HERMES_HOME (Hermes strips and expands it); remove surrounding whitespace or $ from HERMES_DATA_DIR_SUFFIX", home)
	}
	if filepath.Base(filepath.Dir(home)) == "profiles" {
		return fmt.Errorf("hermes: named native profiles are not supported by this initial rendered-composer integration")
	}
	// Native sticky profiles can redirect a root even when HERMES_HOME is set.
	if b, e := os.ReadFile(filepath.Join(home, "active_profile")); e == nil && pyStrip(string(b)) != "" && pyStrip(string(b)) != "default" {
		return fmt.Errorf("hermes: sticky native profiles are not supported; clear active_profile or select the default native profile")
	}
	// Hermes resolves HERMES_MANAGED_DIR against its working directory, the
	// session workspace.
	for _, managed := range []string{os.Getenv("HERMES_MANAGED_DIR"), cfg.Env["HERMES_MANAGED_DIR"], "/etc/hermes"} {
		if managed = pyStrip(managed); managed == "" {
			continue
		}
		// Not filepath.Join: lexical cleaning would fold "link/.." where the
		// filesystem follows the symlink first.
		if !filepath.IsAbs(managed) && cfg.WorkspacePath != "" {
			managed = cfg.WorkspacePath + string(filepath.Separator) + managed
		}
		if st, e := os.Stat(managed); e == nil && st.IsDir() {
			return fmt.Errorf("hermes: managed native configuration is not supported by the additive instruction adapter")
		}
	}
	// Hermes reads its English chrome through locale layers above the bundled
	// catalog; any of them could reword the markers AO detects.
	if err := refuseEnglishLocaleOverrides(home, cfg); err != nil {
		return err
	}
	// Hermes loads <HERMES_HOME>/.env with override, and a source install's
	// project .env too (with override when the home file is absent), so a value
	// there would silently replace AO's instructions or chrome language, or
	// move Hermes to a home or managed scope AO has not checked.
	// The project root follows the installed checkout, not HERMES_HOME, and
	// .op.env bootstraps the environment before the managed scope is read.
	envFiles := []string{filepath.Join(home, ".env"), filepath.Join(home, ".op.env")}
	for _, root := range sourceRoots(home, cfg.Env) {
		envFiles = append(envFiles, filepath.Join(root, ".env"))
	}
	for _, envFile := range envFiles {
		if err := refuseDotenvOverrides(envFile); err != nil {
			return err
		}
	}
	b, readErr := os.ReadFile(filepath.Join(home, "config.yaml"))
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	var config map[string]any
	if len(b) > 0 {
		if err := unmarshalYAML11(b, &config); err != nil {
			return fmt.Errorf("hermes: invalid native configuration: %w", err)
		}
	}
	if err := refuseSecretSources(config); err != nil {
		return err
	}
	cfg.Env["HERMES_HOME"] = home
	// AO reads Hermes's English composer and busy chrome; HERMES_LANGUAGE
	// outranks display.language for this process only.
	cfg.Env["HERMES_LANGUAGE"] = "en"
	// Pin the scope and locale redirects AO checked as unset, so a secret
	// source's gap-fill cannot introduce them after these checks.
	for _, name := range []string{"HERMES_MANAGED_DIR", "HERMES_BUNDLED_LOCALES", "HERMES_BUNDLED_PLUGINS"} {
		if _, ok := childEnvLookup(cfg.Env)(name); !ok {
			cfg.Env[name] = ""
		}
	}
	original, exists := cfg.Env["AO_HERMES_ORIGINAL_EPHEMERAL"]
	if !exists {
		original = cfg.Env["HERMES_EPHEMERAL_SYSTEM_PROMPT"]
	}
	if original == "" {
		// Only parsed values the overlay renders matter: the installer's
		// generated config shows ${VAR} examples in comments.
		var envRef, untyped bool
		original, envRef, untyped = nativeOverlay(config)
		if envRef {
			return fmt.Errorf("hermes: environment-expanded native instruction configuration is unsupported; preserve it in an explicit native ephemeral prompt")
		}
		if untyped {
			return fmt.Errorf("hermes: native instruction configuration has a non-text value (YAML 1.1 reads yes, on, ~ or numbers as non-strings); quote it in config.yaml")
		}
	}
	cfg.Env["AO_HERMES_ORIGINAL_EPHEMERAL"] = original
	instructions := cfg.SystemPrompt
	if instructions == "" && cfg.SystemPromptFile != "" {
		b, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return err
		}
		instructions = string(b)
	}
	combined := original + instructions
	if original != "" && instructions != "" {
		combined = original + "\n\n" + instructions
	}
	// Linux MAX_ARG_STRLEN counts NAME= and the trailing NUL for each entry;
	// Windows caps a variable at 32,767 characters. The AO original-prompt
	// entry carries a second copy of the native text.
	maxEnvString := 128 * 1024
	if runtime.GOOS == "windows" {
		maxEnvString = 32767
	}
	if len("HERMES_EPHEMERAL_SYSTEM_PROMPT=")+len(combined)+1 > maxEnvString ||
		len("AO_HERMES_ORIGINAL_EPHEMERAL=")+len(original)+1 > maxEnvString {
		return fmt.Errorf("hermes: standing instructions exceed process environment limit")
	}
	if instructions != "" {
		if original != "" {
			original += "\n\n"
		}
		cfg.Env["HERMES_EPHEMERAL_SYSTEM_PROMPT"] = original + instructions
	}
	dir := sessionDir(cfg.DataDir, cfg.SessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "native-home"), []byte(home), 0o600)
}

// refuseEnglishLocaleOverrides refuses the catalog layers that can reword
// Hermes's English chrome (agent/i18n.py, i18n_layers.py): a sealed bundle
// named by HERMES_BUNDLED_LOCALES, the profile overlay
// <HERMES_HOME>/locales/en[.<surface>].yaml, and declarative plugin language
// packs under the user, project or HERMES_BUNDLED_PLUGINS plugin directories.
// Relative overrides resolve against Hermes's working directory, the session
// workspace, as they do natively.
func refuseEnglishLocaleOverrides(home string, cfg ports.WorkspaceHookConfig) error {
	childPath := func(path string) (string, bool) {
		if filepath.IsAbs(path) {
			return path, true
		}
		if cfg.WorkspacePath == "" {
			return "", false
		}
		// Not filepath.Join: lexical cleaning would fold "link/.." where the
		// filesystem follows the symlink first.
		return cfg.WorkspacePath + string(filepath.Separator) + path, true
	}
	for _, bundled := range []string{os.Getenv("HERMES_BUNDLED_LOCALES"), cfg.Env["HERMES_BUNDLED_LOCALES"]} {
		if bundled = pyStrip(bundled); bundled != "" {
			path, ok := childPath(bundled)
			if st, err := os.Stat(path); !ok || err == nil && st.IsDir() {
				return fmt.Errorf("hermes: HERMES_BUNDLED_LOCALES replaces the English terminal chrome AO reads; unset it for AO sessions")
			}
		}
	}
	dirs := []string{filepath.Join(home, "locales")}
	pluginRoots := []string{filepath.Join(home, "plugins")}
	if cfg.WorkspacePath != "" {
		pluginRoots = append(pluginRoots, filepath.Join(cfg.WorkspacePath, ".hermes", "plugins"))
	}
	// get_bundled_plugins_dir uses the raw value whenever it is non-empty.
	for _, bundled := range []string{os.Getenv("HERMES_BUNDLED_PLUGINS"), cfg.Env["HERMES_BUNDLED_PLUGINS"]} {
		if bundled == "" {
			continue
		}
		path, ok := childPath(bundled)
		if !ok {
			return fmt.Errorf("hermes: relative HERMES_BUNDLED_PLUGINS %q cannot be inspected without a session workspace", bundled)
		}
		pluginRoots = append(pluginRoots, path)
	}
	for _, root := range pluginRoots {
		// A plugin tree AO cannot read could hold a pack it never saw.
		found, err := localeDirs(root, 0)
		if err != nil {
			return fmt.Errorf("hermes: cannot inspect plugin language packs under %s: %w", root, err)
		}
		dirs = append(dirs, found...)
	}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if englishLocaleFile.MatchString(entry.Name()) && !entry.IsDir() {
				return fmt.Errorf("hermes: %s rewords the English terminal chrome AO reads; remove or rename it for AO sessions", filepath.Join(dir, entry.Name()))
			}
		}
	}
	return nil
}

// localeDirs lists the locales directories in a plugin tree. Like Hermes's
// plugin discovery (Path.is_dir), it follows directory symlinks; the depth
// bound covers flat and category layouts and stops symlink cycles.
func localeDirs(dir string, depth int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if depth == 0 && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, entry := range entries {
		// Not filepath.Join: lexical cleaning would fold a "link/.." in the
		// root where the filesystem follows the symlink first.
		path := dir + string(filepath.Separator) + entry.Name()
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			continue
		}
		if entry.Name() == "locales" {
			dirs = append(dirs, path)
		}
		if depth < 3 {
			nested, err := localeDirs(path, depth+1)
			if err != nil {
				return nil, err
			}
			dirs = append(dirs, nested...)
		}
	}
	return dirs, nil
}

// englishLocaleFile is an i18n_layers locale file for language "en".
var englishLocaleFile = regexp.MustCompile(`^en(?:\.(?:core|tui|desktop))?\.ya?ml$`)

// refuseSecretSources refuses enabled external secret sources (config.yaml
// secrets:, applied after the dotenv layers) that may override variables AO
// sets. Bitwarden and 1Password override by default; a source configured
// with override_existing: false only fills unset names, which AO pins.
// Startup applies sources only when one has enabled exactly true
// (env_loader.py), then fetches every source whose enabled is truthy
// (SecretSource.is_enabled).
func refuseSecretSources(config map[string]any) error {
	secrets, _ := config["secrets"].(map[string]any)
	if !slices.ContainsFunc(slices.Collect(maps.Values(secrets)), func(v any) bool {
		source, ok := v.(map[string]any)
		enabled, isBool := yaml11Bool(source["enabled"])
		return ok && isBool && enabled
	}) {
		return nil
	}
	for name, source := range secrets {
		source, ok := source.(map[string]any)
		if !ok || !pyTruthy(source["enabled"]) {
			continue
		}
		if override, isBool := yaml11Bool(source["override_existing"]); !isBool || override {
			return fmt.Errorf("hermes: secret source %q may override the language and instructions AO sets; set override_existing: false for it", name)
		}
	}
	return nil
}

func refuseDotenvOverrides(envFile string) error {
	bindings, err := dotenvBindings(envFile)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if !binding.assigned {
			continue // a bare name without "=" sets nothing
		}
		switch key := guardedEnvName(binding.key); key {
		case "HERMES_EPHEMERAL_SYSTEM_PROMPT":
			return fmt.Errorf("hermes: %s in %s would replace AO's standing instructions; move it to agent.system_prompt in config.yaml", key, envFile)
		case "HERMES_LANGUAGE":
			return fmt.Errorf("hermes: %s in %s would change the terminal chrome AO reads; set display.language in config.yaml instead", key, envFile)
		case "HERMES_BUNDLED_LOCALES":
			return fmt.Errorf("hermes: %s in %s would replace the English terminal chrome AO reads", key, envFile)
		case "HERMES_HOME", "HERMES_MANAGED_DIR":
			return fmt.Errorf("hermes: %s in %s would move Hermes away from the configuration AO checked; set it in AO's environment instead", key, envFile)
		}
	}
	return nil
}

// referencesEnv reports whether a parsed YAML value holds a ${VAR} reference.
// Hermes expands string values only, so mapping keys are not inspected.
func referencesEnv(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, "${")
	case map[string]any:
		for _, item := range v {
			if referencesEnv(item) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(v, referencesEnv)
	}
	return false
}

// nativeOverlay rebuilds the overlay Hermes renders (personality.py): the
// selected personality's prompt (system_prompt, tone and style for a
// structured one), or agent.system_prompt without one. envRef reports a
// ${VAR} reference in any value that rendering consumes; UI-only fields such
// as a personality's description are ignored. untyped reports a consumed
// value that is not text under Hermes's YAML 1.1 reading, whose Python str()
// rendering AO does not reproduce.
func nativeOverlay(config map[string]any) (overlay string, envRef, untyped bool) {
	agent, _ := config["agent"].(map[string]any)
	display, _ := config["display"].(map[string]any)
	consume := func(v any) {
		envRef = envRef || referencesEnv(v)
		untyped = untyped || !isText(v)
	}
	selection := display["personality"]
	name := pyLower(pyStrip(fmt.Sprint(selection)))
	if selection != nil && name != "" && name != "none" && name != "default" && name != "neutral" {
		consume(selection)
		definitions := map[string]any{}
		for k, v := range builtinPersonalities {
			definitions[k] = v
		}
		for _, src := range []any{config["personalities"], agent["personalities"]} {
			if m, ok := src.(map[string]any); ok {
				for k, v := range m {
					// Hermes expands values, never keys, so a ${VAR} name stays
					// literal; a name that retypes could select another definition.
					untyped = untyped || strings.HasPrefix(k, yaml11Sentinel)
					definitions[pyLower(pyStrip(k))] = v
				}
			}
		}
		if v, ok := definitions[name]; ok {
			if m, ok := v.(map[string]any); ok {
				// render_personality_prompt: str() of each part, stripped,
				// non-empty parts joined. An explicit null prompt renders as
				// "None"; tone and style appear only when truthy. AO
				// reproduces str() only for text, not for lists or maps.
				consumeStr := func(v any) string {
					consume(v)
					s, isString := v.(string)
					untyped = untyped || !isString
					return s
				}
				var parts []string
				if prompt, present := m["system_prompt"]; present {
					if prompt == nil {
						parts = append(parts, "None")
					} else {
						parts = append(parts, consumeStr(prompt))
					}
				}
				for _, k := range []string{"tone", "style"} {
					if pyTruthy(m[k]) {
						parts = append(parts, strings.ToUpper(k[:1])+k[1:]+": "+consumeStr(m[k]))
					}
				}
				var rendered []string
				for _, part := range parts {
					if part = pyStrip(part); part != "" {
						rendered = append(rendered, part)
					}
				}
				return strings.Join(rendered, "\n"), envRef, untyped
			}
			consume(v)
			return promptText(v), envRef, untyped
		}
	}
	consume(agent["system_prompt"])
	return promptText(agent["system_prompt"]), envRef, untyped
}

// isText accepts an absent value, a string, or a list of strings: the
// shapes whose Python str() AO reproduces exactly.
func isText(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return !strings.HasPrefix(v, yaml11Sentinel)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); !ok || strings.HasPrefix(s, yaml11Sentinel) {
				return false
			}
		}
		return true
	}
	return false
}

// pyLower is Python's str.lower(), with full Unicode case mapping.
func pyLower(s string) string {
	return cases.Lower(language.Und).String(s)
}

func promptText(v any) string {
	if v == nil {
		return ""
	}
	if a, ok := v.([]any); ok {
		var parts []string
		for _, item := range a {
			if s := pyStrip(fmt.Sprint(item)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	}
	return pyStrip(fmt.Sprint(v))
}
func nativeHome(data, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(sessionDir(data, id), "native-home"))
	return string(b), err
}

// GetRestoreCommand implements the scoped native adapter contract.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	launch := ports.LaunchConfig{DataDir: cfg.DataDir, SessionID: cfg.Session.ID, WorkspacePath: cfg.Session.WorkspacePath, Config: cfg.Config, Permissions: cfg.Permissions, AllowedTools: cfg.AllowedTools, DisallowedTools: cfg.DisallowedTools}
	if err := validate(launch); err != nil {
		return nil, false, err
	}
	title, err := sessionTitle(cfg.DataDir, cfg.Session.ID)
	if err != nil {
		return nil, false, fmt.Errorf("hermes: AO native session title unavailable: %w", err)
	}
	if _, err := p.titledSessionID(ctx, cfg.DataDir, cfg.Session.ID, title); err != nil {
		return nil, false, err
	}
	// Without --create-if-missing, a conversation renamed or deleted after this
	// check fails loudly instead of silently starting a fresh one.
	cmd, err := p.sessionCommand(ctx, launch, title, false)
	if err != nil {
		return nil, false, err
	}
	return cmd, true, nil
}

// titledSessionID reads the native conversation AO titled for a session.
// Compression continuations keep the root's lineage, which Hermes resolves on
// resume; the root ID is the stable identity AO reports.
func (*Plugin) titledSessionID(ctx context.Context, data, id, title string) (string, error) {
	home, err := nativeHome(data, id)
	if err != nil {
		return "", fmt.Errorf("hermes: native home was not recorded: %w", err)
	}
	db, err := sql.Open("sqlite", nativeStateDSN(home))
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	var nativeID string
	err = db.QueryRowContext(ctx, "SELECT id FROM sessions WHERE title=? ORDER BY started_at LIMIT 1", title).Scan(&nativeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("hermes: native conversation %q not found; it may have been renamed or deleted in Hermes", title)
	}
	if err != nil {
		var coded interface{ Code() int }
		if errors.As(err, &coded) && coded.Code() == sqlite3.SQLITE_READONLY_RECOVERY {
			return "", fmt.Errorf("hermes: native state database needs WAL recovery; resume Hermes natively once, then retry restore: %w", err)
		}
		return "", fmt.Errorf("hermes: native state database unavailable: %w", err)
	}
	return nativeID, nil
}

// SessionInfo implements the scoped native adapter contract.
func (p *Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	id, ok, err := p.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: ref.DataDir, SessionID: domain.SessionID(ref.ID)})
	return ports.SessionInfo{AgentSessionID: id}, ok, err
}

func nativeStateDSN(home string) string {
	path := filepath.ToSlash(filepath.Join(home, "state.db"))
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(1000)"
}

// ContinuouslyDetectTerminalActivity implements the scoped native adapter contract.
func (*Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// EmptyComposerProvesWaitingInputReady implements the scoped native adapter contract.
func (*Plugin) EmptyComposerProvesWaitingInputReady() bool { return true }

// DetectTerminalActivity implements the scoped native adapter contract.
func (*Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	// Native placeholders use italic; typed composer text has normal styling.
	composerOutput := nativeStyleSequence.ReplaceAllStringFunc(output, func(sequence string) string {
		params := strings.Split(strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m"), ";")
		for i := 0; i < len(params); i++ {
			code, _, colon := strings.Cut(params[i], ":")
			// Color payloads contain numbers that are not SGR attributes.
			if code == "38" || code == "48" || code == "58" {
				if !colon && i+1 < len(params) {
					switch params[i+1] {
					case "5":
						i = min(i+2, len(params)-1)
					case "2":
						i = min(i+4, len(params)-1)
					}
				}
				continue
			}
			// Insert beside the real attribute so later resets retain their order.
			switch code {
			case "3":
				params[i] += ";2"
			case "23":
				params[i] += ";22"
			}
		}
		return "\x1b[" + strings.Join(params, ";") + "m"
	})
	lines := terminalui.PlainTerminalLines(output)
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	// The running composer also has borders and an italic placeholder. Inspect
	// the newest prompt first so that chrome cannot satisfy the idle check.
	for i := last; i >= 0 && i >= last-7; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, nativeWarningPrompt) || strings.HasPrefix(line, nativeQuestionPrompt) {
			return domain.ActivityBlocked, true
		}
		if (strings.HasPrefix(line, nativePrompt) || strings.HasPrefix(line, nativeNamedPrompt)) && strings.Contains(line, "msg=interrupt") {
			return domain.ActivityActive, true
		}
		if strings.HasPrefix(line, nativePrompt) || strings.HasPrefix(line, nativeNamedPrompt) {
			break
		}
	}
	// Below 64 columns Hermes hides the bottom input rule and draws only the
	// state icon, without ❯ (`☤ ` busy; `⚠ `, `? `, `✎ ` and the credential icons
	// for decisions). Read that row only as the last line directly under the top
	// rule, where the compact composer sits.
	if last >= 1 {
		row, upper := strings.TrimSpace(lines[last]), strings.TrimSpace(lines[last-1])
		if len([]rune(upper)) >= 16 && strings.Trim(upper, "─") == "" && !strings.HasPrefix(row, nativeNamedPrompt) {
			if strings.HasPrefix(row, "☤ ") || row == "☤" {
				return domain.ActivityActive, true
			}
			for _, icon := range []string{"⚠", "?", "✎", "🔐", "🔑", "⚙"} {
				if row == icon || strings.HasPrefix(row, icon+" ") {
					return domain.ActivityBlocked, true
				}
			}
		}
	}
	if last >= 0 {
		bottom := strings.TrimSpace(lines[last])
		if len([]rune(bottom)) >= 16 && strings.Trim(bottom, "─") == "" &&
			(terminalui.LastBorderedPromptIsEmptyOrDimPlaceholder(composerOutput, nativePrompt) ||
				terminalui.LastBorderedPromptIsEmptyOrDimPlaceholder(composerOutput, nativeNamedPrompt)) {
			return domain.ActivityIdle, true
		}
	}
	// Below 64 columns Hermes hides the bottom input rule. Accept that compact
	// composer only when the prompt row is the last line, directly under the top
	// rule, and classify it as if the hidden rule were drawn.
	if last >= 1 {
		prompt, upper := strings.TrimSpace(lines[last]), strings.TrimSpace(lines[last-1])
		if (strings.HasPrefix(prompt, nativePrompt) || strings.HasPrefix(prompt, nativeNamedPrompt)) &&
			len([]rune(upper)) >= 16 && strings.Trim(upper, "─") == "" {
			closed := strings.TrimRight(composerOutput, "\n") + "\n" + upper
			if terminalui.LastBorderedPromptIsEmptyOrDimPlaceholder(closed, nativePrompt) ||
				terminalui.LastBorderedPromptIsEmptyOrDimPlaceholder(closed, nativeNamedPrompt) {
				return domain.ActivityIdle, true
			}
		}
	}
	for i := last; i >= 0 && i >= last-4; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, nativePrompt) || strings.HasPrefix(line, nativeNamedPrompt) {
			return domain.ActivityBlocked, true
		}
	}
	return "", false
}

// RequiresRenderedActivity implements the scoped native adapter contract.
func (*Plugin) RequiresRenderedActivity() bool { return true }

// ResolveNativeSessionID implements the scoped native adapter contract.
func (p *Plugin) ResolveNativeSessionID(ctx context.Context, cfg ports.NativeSessionResolveConfig) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	title, err := sessionTitle(cfg.DataDir, string(cfg.SessionID))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	id, err := p.titledSessionID(ctx, cfg.DataDir, string(cfg.SessionID), title)
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// ExitDetectionMode implements the scoped native adapter contract.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// ValidateTerminalMessage refuses ordinary AO text while native decisions own input.
func (p *Plugin) ValidateTerminalMessage(_, screen string) error {
	state, ok := p.DetectTerminalActivity(screen)
	if !ok || state != domain.ActivityIdle {
		return fmt.Errorf("hermes: ordinary AO sends require the current empty native composer; enter approval decisions directly in the terminal")
	}
	return nil
}

// TerminalMessageObserved accepts a running footer or a newly committed native
// transcript echo above an empty composer. A changed idle frame alone is not
// evidence of submission: it may be an unrelated redraw before Enter is handled.
func (p *Plugin) TerminalMessageObserved(message, baseline, screen string) bool {
	before, ready := p.DetectTerminalActivity(baseline)
	after, known := p.DetectTerminalActivity(screen)
	if screen == baseline || !ready || before != domain.ActivityIdle || !known {
		return false
	}
	if after == domain.ActivityActive {
		return true
	}
	if after != domain.ActivityIdle {
		return false
	}
	return committedMessageEchoes(message, screen) > committedMessageEchoes(message, baseline)
}

// Count complete matching native echoes outside the current composer. Comparing
// counts prevents old identical text from acknowledging a new send after a redraw.
// Ignore whitespace inserted by terminal line wrapping, but require the entire
// message between native transcript rules. Truncated previews remain unconfirmed.
func committedMessageEchoes(message, screen string) int {
	message = strings.TrimSpace(message)
	if message == "" {
		return 0
	}
	normalizedMessage := strings.Join(strings.Fields(message), "")
	lines := terminalui.PlainTerminalLines(screen)
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, nativePrompt) && !strings.HasPrefix(line, nativeNamedPrompt) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if row := strings.TrimSpace(lines[j]); len([]rune(row)) >= 16 && strings.Trim(row, "─") == "" {
				end = j
				break
			}
		}
		break
	}
	count := 0
	for i := 0; i < end; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(message, "/") && !strings.Contains(message, "\n") && line == nativeCommandEcho+message {
			count++
			continue
		}
		if i == 0 || strings.TrimSpace(lines[i-1]) != nativeTranscriptRule || !strings.HasPrefix(line, nativeUserEcho) {
			continue
		}
		var echo strings.Builder
		echo.WriteString(strings.TrimPrefix(line, nativeUserEcho))
		j := i + 1
		for j < end && strings.TrimSpace(lines[j]) != nativeTranscriptRule {
			echo.WriteString(strings.TrimSpace(lines[j]))
			j++
		}
		if j < end && strings.Join(strings.Fields(echo.String()), "") == normalizedMessage {
			count++
		}
	}
	return count
}

// yaml11Sentinel marks a plain scalar that Hermes's YAML 1.1 loader
// (hermes_yaml, ruamel at version 1.1) would not read as a string, such as
// yes, on, y, ~ or 0755. yaml.v3 follows YAML 1.2 and reads them as text.
const yaml11Sentinel = "\x00yaml1.1:"

var yaml11NonString = regexp.MustCompile(`^(?:` +
	`[yY]|[yY]es|YES|[nN]|[nN]o|NO|true|True|TRUE|false|False|FALSE|[oO]n|ON|[oO]ff|OFF` + // bool
	`|~|[nN]ull|NULL` + // null
	`|[-+]?0b[01_]+|[-+]?0[0-7_]+|[-+]?(?:0|[1-9][0-9_]*)|[-+]?0x[0-9a-fA-F_]+|[-+]?[1-9][0-9_]*(?::[0-5]?[0-9])+` + // int
	`|[-+]?(?:[0-9][0-9_]*)?\.[0-9_]*(?:[eE][-+]?[0-9]+)?|[-+]?[0-9][0-9_]*[eE][-+]?[0-9]+|[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+\.[0-9_]*|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN)` + // float
	`|[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}(?:(?:[Tt]|[ \t]+)[0-9]{1,2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]*)?(?:[ \t]*(?:Z|[-+][0-9]{1,2}(?::[0-9]{2})?))?)?` + // timestamp
	`|=` + // value
	`)$`)

// yaml11Bool reads a value decoded by unmarshalYAML11 as Hermes's YAML 1.1
// loader would: true/false, or a marked yes/no/on/off/y/n word.
func yaml11Bool(v any) (value, ok bool) {
	switch v := v.(type) {
	case bool:
		return v, true
	case string:
		word, marked := strings.CutPrefix(v, yaml11Sentinel)
		if !marked {
			return false, false
		}
		switch strings.ToLower(word) {
		case "y", "yes", "true", "on":
			return true, true
		case "n", "no", "false", "off":
			return false, true
		}
	}
	return false, false
}

// pyTruthy is Python's bool() of a value decoded by unmarshalYAML11 as
// Hermes's YAML 1.1 loader would type it.
func pyTruthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case int:
		return v != 0
	case float64:
		return v != 0
	case string:
		word, marked := strings.CutPrefix(v, yaml11Sentinel)
		if !marked {
			return v != ""
		}
		if value, ok := yaml11Bool(v); ok {
			return value
		}
		switch word {
		case "~", "null", "Null", "NULL":
			return false
		}
		// A number is false only when every digit is zero.
		if yaml11Number.MatchString(word) {
			digits := strings.TrimLeft(word, "+-")
			digits = strings.TrimPrefix(strings.TrimPrefix(digits, "0x"), "0b")
			digits, _, _ = strings.Cut(strings.ToLower(digits), "e")
			return strings.Trim(digits, "0_.:") != ""
		}
		return true
	case map[string]any:
		return len(v) > 0
	case []any:
		return len(v) > 0
	}
	return true
}

var yaml11Number = regexp.MustCompile(`^[-+]?(?:0b[01_]+|0x[0-9a-fA-F_]+|[0-9][0-9_:]*(?:\.[0-9_]*)?(?:[eE][-+]?[0-9]+)?|\.[0-9_]+(?:[eE][-+]?[0-9]+)?)$`)

// unmarshalYAML11 decodes YAML like yaml.Unmarshal, but marks every plain
// scalar that YAML 1.1 types as non-string with yaml11Sentinel, so callers
// can tell where Hermes would see a bool, null, number or date.
func unmarshalYAML11(b []byte, out any) error {
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return err
	}
	var mark func(n *yaml.Node)
	seen := map[*yaml.Node]bool{}
	mark = func(n *yaml.Node) {
		if n == nil || seen[n] {
			return
		}
		seen[n] = true
		// yaml.v3 decodes an explicit !!binary (or unknown) tag as a Go string,
		// where Hermes sees bytes and renders str() as b'...'.
		if n.Kind == yaml.ScalarNode && n.Style&yaml.TaggedStyle != 0 && !slices.Contains([]string{"!!str", "!!int", "!!float", "!!bool", "!!null"}, n.ShortTag()) {
			n.Tag, n.Style, n.Value = "!!str", yaml.DoubleQuotedStyle, yaml11Sentinel+n.Tag+" "+n.Value
		}
		if n.Kind == yaml.ScalarNode && n.Style == 0 && n.Value != "" {
			switch tag, nonString := n.ShortTag(), yaml11NonString.MatchString(n.Value); {
			case tag == "!!str" && nonString:
				n.Tag, n.Value = "!!str", yaml11Sentinel+n.Value
			case (tag == "!!int" || tag == "!!float") && !nonString:
				// YAML 1.2-only forms such as 0o755 are strings to YAML 1.1.
				n.Tag = "!!str"
			}
		}
		for _, child := range n.Content {
			mark(child)
		}
		mark(n.Alias)
	}
	mark(&root)
	return root.Decode(out)
}
