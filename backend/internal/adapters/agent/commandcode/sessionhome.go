package commandcode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Command Code reads user-tier memory in both Terminal and Chat, including after
// compaction. Each session gets an isolated home so AO roles cannot overwrite
// each other; repository memory still loads alongside the composed user memory.

// Profile inputs are mirrored independently; missing files are allowed.
var mirroredProfileFiles = []string{"auth.json", "config.json", "providers.json", "settings.json", "mcp.json"}

// Link real credentials without copying secrets; existing session entries win.
// Links are best-effort on platforms without symlink privilege.
var linkedHomeEntries = []string{".ssh", ".npmrc", ".netrc", ".docker", ".aws"}

// SessionHomeDir returns durable harness state outside disposable prompts/<id>.
func SessionHomeDir(dataDir string, sessionID domain.SessionID) string {
	dataDir = strings.TrimSpace(dataDir)
	sessionID = domain.SessionID(strings.TrimSpace(string(sessionID)))
	if dataDir == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(dataDir, "harnesses", "command-code", string(sessionID))
}

// realCommandCodeDir returns the user's own ~/.commandcode, the source for the
// profile files this session mirrors.
func realCommandCodeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".commandcode")
}

// PrepareSessionHome mirrors the profile and composes user memory with AO's role.
// Memory writes and stale-profile removal are required; native history is preserved.
func PrepareSessionHome(dataDir string, sessionID domain.SessionID, systemPrompt string) (string, error) {
	home := SessionHomeDir(dataDir, sessionID)
	if home == "" {
		return "", nil
	}
	profile := filepath.Join(home, ".commandcode")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return "", fmt.Errorf("command-code: create session profile: %w", err)
	}
	source := realCommandCodeDir()
	if source != "" && source != profile {
		if err := mirrorProfileFiles(source, profile); err != nil {
			return "", err
		}
		// Refresh installed mods without disturbing native transcripts.
		mirrorDirectoryTree(filepath.Join(source, "mods"), filepath.Join(profile, "mods"))
	}
	if realHome, err := os.UserHomeDir(); err == nil && strings.TrimSpace(realHome) != "" && realHome != home {
		linkRealDotfiles(realHome, home)
		preserveGitConfiguration(realHome, home)
	}
	memory := composeSessionMemory(systemPrompt, source, profile)
	if err := writePrivateFile(filepath.Join(profile, "AGENTS.md"), memory); err != nil {
		return "", fmt.Errorf("command-code: write session memory: %w", err)
	}
	return home, nil
}

// composeSessionMemory reads the real user memory afresh so restore never duplicates it.
func composeSessionMemory(systemPrompt, sourceProfile, sessionProfile string) []byte {
	instructions := strings.TrimRight(systemPrompt, "\n") + "\n"
	if strings.TrimSpace(systemPrompt) == "" {
		instructions = ""
	}
	if sourceProfile == "" || sourceProfile == sessionProfile {
		return []byte(instructions)
	}
	contents, err := os.ReadFile(filepath.Join(sourceProfile, "AGENTS.md"))
	if err != nil {
		return []byte(instructions)
	}
	userMemory := strings.TrimRight(string(contents), "\n")
	if strings.TrimSpace(userMemory) == "" {
		return []byte(instructions)
	}
	if instructions == "" {
		return []byte(userMemory + "\n")
	}
	return []byte(userMemory + "\n\n" + instructions)
}

// MigrateNativeHistory copies only the captured conversation and sidecars.
// Existing destination files win; missing source history is allowed.
func MigrateNativeHistory(dataDir string, sessionID domain.SessionID, agentSessionID string) {
	agentSessionID = strings.TrimSpace(agentSessionID)
	home := SessionHomeDir(dataDir, sessionID)
	source := realCommandCodeDir()
	if agentSessionID == "" || home == "" || source == "" {
		return
	}
	matches, err := filepath.Glob(filepath.Join(source, "projects", "*", agentSessionID+".*"))
	if err != nil || len(matches) == 0 {
		return
	}
	for _, match := range matches {
		rel, err := filepath.Rel(filepath.Join(source, "projects"), match)
		if err != nil {
			continue
		}
		destination := filepath.Join(home, ".commandcode", "projects", rel)
		if _, err := os.Stat(destination); err == nil {
			continue
		}
		contents, err := os.ReadFile(match) //nolint:gosec // match comes from our own glob of a fixed filename
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			continue
		}
		_ = writePrivateFile(destination, contents)
	}
}

// SessionHomeEnv points both supported home variables at the isolated profile.
func SessionHomeEnv(home string) map[string]string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	return map[string]string{
		"HOME":        home,
		"USERPROFILE": home,
	}
}

// SessionProcessEnv preserves explicit daemon/project overrides and gh's XDG lookup.
// An anchored XDG chain uses the session global config to avoid reading it twice.
func SessionProcessEnv(dataDir string, sessionID domain.SessionID, base map[string]string) map[string]string {
	home := SessionHomeDir(dataDir, sessionID)
	env := SessionHomeEnv(home)
	if env == nil {
		return nil
	}
	effective := effectiveEnv(base)
	if !hasEnvKey(effective, "GIT_CONFIG_GLOBAL") && gitConfigGlobalScope(home) {
		env["GIT_CONFIG_GLOBAL"] = filepath.Join(home, ".gitconfig")
	}
	realHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(realHome) == "" {
		return env
	}
	if !hasEnvKey(effective, "GH_CONFIG_DIR") && strings.TrimSpace(effective["XDG_CONFIG_HOME"]) == "" {
		if dir := ghConfigDir(realHome); dir != "" {
			env["GH_CONFIG_DIR"] = dir
		}
	}
	return env
}

// gitConfigGlobalScope reports whether only the anchored XDG copy should be read.
func gitConfigGlobalScope(home string) bool {
	return isRegularFile(filepath.Join(home, sessionGitConfigCopiesDir, gitConfigGlobalScopeMarker))
}

// effectiveEnv overlays session variables onto the daemon environment.
func effectiveEnv(base map[string]string) map[string]string {
	effective := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || hasEnvKey(effective, key) {
			continue
		}
		effective[key] = value
	}
	for key, value := range base {
		effective[key] = value
	}
	return effective
}

// hasEnvKey reports whether an environment map already defines a key,
// case-insensitively so Windows key folding cannot duplicate it.
func hasEnvKey(env map[string]string, key string) bool {
	for existing := range env {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}

// ghConfigDir finds the default gh profile when no explicit or XDG override applies.
func ghConfigDir(realHome string) string {
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		if dir := filepath.Join(appData, "GitHub CLI"); isDirectory(dir) {
			return dir
		}
	}
	if dir := filepath.Join(realHome, ".config", "gh"); isDirectory(dir) {
		return dir
	}
	return ""
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// linkRealDotfiles keeps developer credentials live without overwriting session entries.
func linkRealDotfiles(realHome, sessionHome string) {
	entries := append([]string(nil), linkedHomeEntries...)
	// Keep .config itself session-owned: preserveGitConfiguration rewrites its
	// git child and must never write through a link into the real git config.
	// Other tools' configuration stays live-linked, including credential files.
	if configs, err := os.ReadDir(filepath.Join(realHome, ".config")); err == nil {
		sessionConfig := filepath.Join(sessionHome, ".config")
		info, statErr := os.Lstat(sessionConfig)
		if os.IsNotExist(statErr) {
			if err := os.MkdirAll(sessionConfig, 0o700); err == nil {
				info, statErr = os.Lstat(sessionConfig)
			}
		}
		if statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			for _, config := range configs {
				if !strings.EqualFold(config.Name(), "git") {
					entries = append(entries, filepath.Join(".config", config.Name()))
				}
			}
		}
	}
	for _, name := range entries {
		source := filepath.Join(realHome, name)
		if !isDirectory(source) && !isRegularFile(source) {
			continue
		}
		link := filepath.Join(sessionHome, name)
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		_ = os.Symlink(source, link)
	}
}

// absoluteXDGConfigHome returns an absolute XDG override, or the native HOME fallback.
func absoluteXDGConfigHome() string {
	xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if xdg == "" || !filepath.IsAbs(xdg) {
		return ""
	}
	return xdg
}

// Markers bound AO's block; copies retain includes whose HOME bases move.
const (
	sessionGitConfigIncludeBegin = "# AO: the real user git configuration, included so it applies inside this session"
	sessionGitConfigIncludeEnd   = "# AO: end of the real user git configuration"
	sessionGitConfigCopiesDir    = ".ao-gitconfig"
	gitConfigIncludeMaxDepth     = 8

	// gitConfigGlobalScopeMarker records that git must read the session
	// .gitconfig as its global file, because the anchored XDG copy is the only
	// place the XDG source may be read from. Its presence, not its contents,
	// carries the decision; SessionProcessEnv turns it into GIT_CONFIG_GLOBAL.
	gitConfigGlobalScopeMarker = "global-scope"
)

// preserveGitConfiguration retains XDG-before-global precedence and session settings.
// Read an anchored XDG source exactly once by selecting the combined session config.
// Remove stale includes before returning to native discovery on restore.
func preserveGitConfiguration(realHome, sessionHome string) {
	rewriter := &gitConfigRewriter{
		realHome:    realHome,
		sessionHome: sessionHome,
		copies:      map[string]string{},
	}
	var includes []string
	xdgRebased := false

	// Git reads the XDG file before ~/.gitconfig; keep that order.
	if xdg := absoluteXDGConfigHome(); xdg != "" {
		if source := filepath.Join(xdg, "git", "config"); isRegularFile(source) && rewriter.rebaseNeeded(source, 0) {
			// If anchoring fails, retain native discovery without duplicating the source.
			if copyPath := rewriter.materialize(source); copyPath != source {
				includes = append(includes, copyPath)
				xdgRebased = true
			}
		}
	} else {
		// Anchor the mirrored XDG config against its real source directory.
		sourceDir := filepath.Join(realHome, ".config", "git")
		mirrored := filepath.Join(sessionHome, ".config", "git")
		mirrorDirectoryTree(sourceDir, mirrored)
		if isRegularFile(filepath.Join(mirrored, "config")) {
			_ = rewriter.relocate(filepath.Join(sourceDir, "config"), filepath.Join(mirrored, "config"))
		}
	}

	if source := filepath.Join(realHome, ".gitconfig"); isRegularFile(source) {
		includes = append(includes, rewriter.reference(source))
	}

	path := filepath.Join(sessionHome, ".gitconfig")
	marker := filepath.Join(sessionHome, sessionGitConfigCopiesDir, gitConfigGlobalScopeMarker)
	if len(includes) == 0 {
		// Clear the stale copy reference before restoring native discovery.
		dropGitConfigIncludeBlock(path)
		_ = os.Remove(marker)
		return
	}

	var block strings.Builder
	block.WriteString(sessionGitConfigIncludeBegin + "\n")
	for _, include := range includes {
		block.WriteString("[include]\n\tpath = " + gitConfigString(filepath.ToSlash(include)) + "\n")
	}
	block.WriteString(sessionGitConfigIncludeEnd + "\n")

	existing, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return
	}
	updated := replaceGitConfigIncludeBlock(string(existing), block.String())
	if !existed || updated != string(existing) {
		if err := writePrivateFile(path, []byte(updated)); err != nil {
			// Without the file, pointing git's global scope here would drop the
			// user's configuration entirely.
			_ = os.Remove(marker)
			return
		}
	}
	if !xdgRebased {
		_ = os.Remove(marker)
		return
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return
	}
	_ = writePrivateFile(marker, nil)
}

// replaceGitConfigIncludeBlock swaps AO's include block in an existing
// .gitconfig, or inserts it ahead of content that AO did not write.
func replaceGitConfigIncludeBlock(existing, block string) string {
	begin := strings.Index(existing, sessionGitConfigIncludeBegin)
	if begin >= 0 {
		if offset := strings.Index(existing[begin:], sessionGitConfigIncludeEnd); offset >= 0 {
			end := begin + offset + len(sessionGitConfigIncludeEnd)
			if end < len(existing) && existing[end] == '\n' {
				end++
			}
			return existing[:begin] + block + existing[end:]
		}
	}
	if existing == "" {
		return block
	}
	return block + "\n" + existing
}

// dropGitConfigIncludeBlock preserves session settings and removes an AO-only file.
func dropGitConfigIncludeBlock(path string) {
	existing, err := os.ReadFile(path)
	if err != nil {
		return
	}
	updated := replaceGitConfigIncludeBlock(string(existing), "")
	if updated == string(existing) {
		return
	}
	if strings.TrimSpace(updated) == "" {
		_ = os.Remove(path)
		return
	}
	_ = writePrivateFile(path, []byte(updated))
}

// gitConfigRewriter anchors include chains in session-owned copies.
type gitConfigRewriter struct {
	realHome    string
	sessionHome string
	copies      map[string]string
	depth       int
}

// reference selects the real config or its anchored session copy.
func (r *gitConfigRewriter) reference(target string) string {
	target = filepath.Clean(target)
	if copyPath, ok := r.copies[target]; ok {
		return copyPath
	}
	if !r.rebaseNeeded(target, 0) {
		return target
	}
	return r.materialize(target)
}

// rebaseNeeded checks whether this include chain depends on the original HOME.
func (r *gitConfigRewriter) rebaseNeeded(target string, depth int) bool {
	if depth > gitConfigIncludeMaxDepth {
		return false
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		return false
	}
	if needsGitConfigAnchoring(string(contents)) {
		return true
	}
	for _, nested := range gitIncludeTargets(string(contents), filepath.Dir(target), r.realHome) {
		if r.rebaseNeeded(nested, depth+1) {
			return true
		}
	}
	return false
}

// materialize writes an anchored copy, retaining the real path on failure.
func (r *gitConfigRewriter) materialize(target string) string {
	if copyPath, ok := r.copies[target]; ok {
		return copyPath
	}
	if r.depth >= gitConfigIncludeMaxDepth {
		return target
	}
	directory := filepath.Join(r.sessionHome, sessionGitConfigCopiesDir)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return target
	}
	dest := filepath.Join(directory, gitConfigCopyName(target))
	// Record the copy before rewriting: an include cycle then resolves to a real
	// path instead of recursing, and git reports the bad config itself.
	r.copies[target] = dest
	r.depth++
	err := r.relocate(target, dest)
	r.depth--
	if err != nil {
		delete(r.copies, target)
		return target
	}
	return dest
}

// relocate anchors includes and conditions to their original directory and home.
func (r *gitConfigRewriter) relocate(source, dest string) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writePrivateFile(dest, []byte(r.rewrite(string(contents), filepath.Dir(source))))
}

// rewrite changes include paths and gitdir conditions while retaining other raw lines.
func (r *gitConfigRewriter) rewrite(contents, sourceDir string) string {
	var out strings.Builder
	section := ""
	for _, line := range splitGitConfigLines(contents) {
		name, subsection, header := gitSectionHeader(line.text)
		if header {
			section = name
			if name == "includeif" {
				if condition, ok := r.anchorCondition(subsection, sourceDir); ok {
					out.WriteString("[includeIf " + gitConfigString(condition) + "]\n")
					continue
				}
			}
			out.WriteString(line.raw)
			continue
		}
		if section == "include" || section == "includeif" {
			if value, ok := gitIncludePathValue(line.text); ok {
				if nested, ok := gitExpandConfigPath(value, sourceDir, r.realHome); ok {
					indent := line.text[:len(line.text)-len(strings.TrimLeft(line.text, " \t"))]
					out.WriteString(indent + "path = " + gitConfigString(filepath.ToSlash(r.reference(nested))) + "\n")
					continue
				}
			}
		}
		out.WriteString(line.raw)
	}
	return out.String()
}

// anchorCondition rebases gitdir and gitdir/i patterns only.
func (r *gitConfigRewriter) anchorCondition(condition, sourceDir string) (string, bool) {
	prefix, pattern, ok := gitConditionPattern(condition)
	if !ok {
		return "", false
	}
	anchored, ok := anchorConfigPattern(pattern, sourceDir, r.realHome)
	if !ok {
		return "", false
	}
	return prefix + anchored, true
}

// gitConditionPattern preserves the condition keyword and its case sensitivity.
func gitConditionPattern(condition string) (string, string, bool) {
	lower := strings.ToLower(condition)
	for _, keyword := range []string{"gitdir/i:", "gitdir:"} {
		if strings.HasPrefix(lower, keyword) {
			return condition[:len(keyword)], condition[len(keyword):], true
		}
	}
	return "", "", false
}

// anchorConfigPattern changes the base while preserving wildcards and trailing slashes.
func anchorConfigPattern(pattern, sourceDir, realHome string) (string, bool) {
	var base, rest string
	switch {
	case pattern == "." || strings.HasPrefix(pattern, "./"):
		base, rest = sourceDir, strings.TrimPrefix(strings.TrimPrefix(pattern, "."), "/")
	case pattern == "~" || strings.HasPrefix(pattern, "~/"):
		base, rest = realHome, strings.TrimPrefix(strings.TrimPrefix(pattern, "~"), "/")
	default:
		// Absolute paths, `~user`, and bare globs (which git matches anywhere)
		// already mean the same thing from any location.
		return "", false
	}
	anchored := filepath.ToSlash(base)
	if rest == "" {
		// A directory pattern only matches with its trailing separator.
		return anchored + "/", true
	}
	joined := filepath.ToSlash(filepath.Join(base, filepath.FromSlash(rest)))
	if strings.HasSuffix(rest, "/") && !strings.HasSuffix(joined, "/") {
		// Keep a trailing separator: git matches it literally.
		joined += "/"
	}
	return joined, true
}

// gitIncludeTargets resolves every include target in contents.
func gitIncludeTargets(contents, sourceDir, realHome string) []string {
	var targets []string
	section := ""
	for _, line := range splitGitConfigLines(contents) {
		if name, _, header := gitSectionHeader(line.text); header {
			section = name
			continue
		}
		if section != "include" && section != "includeif" {
			continue
		}
		if value, ok := gitIncludePathValue(line.text); ok {
			if target, ok := gitExpandConfigPath(value, sourceDir, realHome); ok {
				targets = append(targets, target)
			}
		}
	}
	return targets
}

// needsGitConfigAnchoring detects HOME-relative includes and gitdir conditions.
func needsGitConfigAnchoring(contents string) bool {
	section := ""
	for _, line := range splitGitConfigLines(contents) {
		if name, subsection, header := gitSectionHeader(line.text); header {
			section = name
			if name == "includeif" {
				if _, pattern, ok := gitConditionPattern(subsection); ok && isHomeRelativePath(pattern) {
					return true
				}
			}
			continue
		}
		if section != "include" && section != "includeif" {
			continue
		}
		if value, ok := gitIncludePathValue(line.text); ok && isHomeRelativePath(value) {
			return true
		}
	}
	return false
}

func isHomeRelativePath(value string) bool {
	return value == "~" || strings.HasPrefix(value, "~/")
}

// gitConfigLine is one logical config line: the raw physical bytes, and the same
// line with backslash continuations joined for parsing.
type gitConfigLine struct {
	raw  string
	text string
}

// splitGitConfigLines joins unescaped continuations for parsing and keeps original bytes.
func splitGitConfigLines(contents string) []gitConfigLine {
	var lines []gitConfigLine
	for contents != "" {
		var raw, text strings.Builder
		for {
			end := strings.IndexByte(contents, '\n')
			if end < 0 {
				raw.WriteString(contents)
				text.WriteString(contents)
				contents = ""
				break
			}
			physical := contents[:end]
			raw.WriteString(physical)
			raw.WriteByte('\n')
			contents = contents[end+1:]
			// A CRLF file must parse like git does; the raw bytes keep the CR.
			body := strings.TrimSuffix(physical, "\r")
			if trailingBackslashes(body)%2 == 1 {
				text.WriteString(body[:len(body)-1])
				continue
			}
			text.WriteString(body)
			break
		}
		lines = append(lines, gitConfigLine{raw: raw.String(), text: text.String()})
	}
	return lines
}

// trailingBackslashes counts the backslashes at the end of a physical line. An
// odd count leaves the last one unescaped, which is what continues the line.
func trailingBackslashes(line string) int {
	count := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		count++
	}
	return count
}

// gitSectionName returns the lowercased section name of a section header line,
// or "" when the line is not one.
func gitSectionName(line string) string {
	name, _, _ := gitSectionHeader(line)
	return name
}

// gitSectionHeader handles quoted subsections, including embedded closing brackets.
func gitSectionHeader(line string) (string, string, bool) {
	line = strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(line, "[") {
		return "", "", false
	}
	closing := -1
	quoted := false
	for i := 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if quoted {
				i++
			}
		case '"':
			quoted = !quoted
		case ']':
			if !quoted {
				closing = i
			}
		}
		if closing >= 0 {
			break
		}
	}
	if closing < 0 {
		return "", "", false
	}
	header := strings.TrimSpace(line[1:closing])
	name := header
	subsection := ""
	if cut := strings.IndexAny(header, " \t"); cut >= 0 {
		name = header[:cut]
		if rest := strings.TrimSpace(header[cut:]); strings.HasPrefix(rest, "\"") {
			if parsed, ok := gitConfigValue(rest); ok {
				subsection = parsed
			}
		}
	}
	return strings.ToLower(name), subsection, true
}

// gitExpandConfigPath resolves real-home and file-relative paths; ~user stays native.
func gitExpandConfigPath(value, sourceDir, realHome string) (string, bool) {
	switch {
	case value == "":
		return "", false
	case value == "~":
		return realHome, true
	case strings.HasPrefix(value, "~/"):
		return filepath.Join(realHome, filepath.FromSlash(value[2:])), true
	case strings.HasPrefix(value, "~"):
		return "", false
	case filepath.IsAbs(value):
		return filepath.Clean(value), true
	default:
		return filepath.Join(sourceDir, filepath.FromSlash(value)), true
	}
}

// gitIncludePathValue returns the value of a `path` assignment on an include
// line, following git's unquoting and comment rules.
func gitIncludePathValue(line string) (string, bool) {
	line = strings.TrimLeft(line, " \t")
	if line == "" || line[0] == '#' || line[0] == ';' {
		return "", false
	}
	equals := strings.IndexByte(line, '=')
	if equals < 0 || !strings.EqualFold(strings.TrimSpace(line[:equals]), "path") {
		return "", false
	}
	return gitConfigValue(line[equals+1:])
}

// gitConfigValue parses a config value, removing quotes and inline comments.
func gitConfigValue(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	var value strings.Builder
	quoted := false
	for i := 0; i < len(raw); i++ {
		char := raw[i]
		if quoted {
			if char == '\\' && i+1 < len(raw) {
				i++
				switch raw[i] {
				case 'n':
					value.WriteByte('\n')
				case 't':
					value.WriteByte('\t')
				case 'b':
					value.WriteByte('\b')
				case '\\':
					value.WriteByte('\\')
				case '"':
					value.WriteByte('"')
				default:
					value.WriteByte(raw[i])
				}
				continue
			}
			if char == '"' {
				quoted = false
				continue
			}
			value.WriteByte(char)
			continue
		}
		switch char {
		case '"':
			quoted = true
		case '#', ';':
			return strings.TrimRight(value.String(), " \t"), true
		default:
			value.WriteByte(char)
		}
	}
	return strings.TrimRight(value.String(), " \t"), true
}

// gitConfigString renders a value for a git config file. Backslashes are
// escapes there, so a Windows path is written with forward slashes before it is
// quoted, and quotes or backslashes that survive are escaped.
func gitConfigString(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + escaped + `"`
}

// gitConfigCopyName derives a stable, path-safe name for a materialized config.
func gitConfigCopyName(target string) string {
	sum := sha256.Sum256([]byte(target))
	return hex.EncodeToString(sum[:8]) + ".gitconfig"
}

// mirrorDirectoryTree refreshes regular files without following links or deleting additions.
func mirrorDirectoryTree(source, destination string) {
	if !isDirectory(source) {
		return
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return
	}
	for _, entry := range entries {
		sourceEntry := filepath.Join(source, entry.Name())
		destinationEntry := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			mirrorDirectoryTree(sourceEntry, destinationEntry)
			continue
		}
		// Regular files only: links are deliberately not followed, since a
		// mirrored tree must never drag real-home state behind them.
		if !entry.Type().IsRegular() {
			continue
		}
		contents, err := os.ReadFile(sourceEntry)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destinationEntry), 0o700); err != nil {
			continue
		}
		if err := os.WriteFile(destinationEntry, contents, 0o600); err != nil {
			continue
		}
	}
}

// mirrorProfileFiles refreshes available inputs and removes mirrors of deleted ones.
// A stale mirror that cannot be removed prevents launch.
func mirrorProfileFiles(source, destination string) error {
	for _, name := range mirroredProfileFiles {
		contents, err := os.ReadFile(filepath.Join(source, name)) //nolint:gosec // name is a fixed allowlist
		if err != nil {
			if os.IsNotExist(err) {
				if err := os.Remove(filepath.Join(destination, name)); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("command-code: remove stale profile %s: %w", name, err)
				}
			}
			continue
		}
		if name == "settings.json" {
			contents = rebaseSettingsHomeRefs(contents, filepath.Dir(source))
		}
		_ = writePrivateFile(filepath.Join(destination, name), contents)
	}
	return nil
}

// rebaseSettingsHomeRefs anchors skill/mod paths and hook commands on the session copy.
// Permission rules and malformed JSON remain untouched.
func rebaseSettingsHomeRefs(contents []byte, realHome string) []byte {
	realHome = strings.TrimSpace(realHome)
	if realHome == "" {
		return contents
	}
	var settings map[string]any
	if err := json.Unmarshal(contents, &settings); err != nil {
		return contents
	}
	changed := false
	rebaseList := func(key string) {
		list, ok := settings[key].([]any)
		if !ok {
			return
		}
		for i, entry := range list {
			if path, ok := entry.(string); ok {
				if rebased, ok := rebaseHomeRef(path, realHome); ok {
					list[i] = rebased
					changed = true
				}
			}
		}
	}
	rebaseList("skills")
	if mods, ok := settings["mods"].(map[string]any); ok {
		if paths, ok := mods["paths"].([]any); ok {
			for i, path := range paths {
				if entry, ok := path.(string); ok {
					if rebased, ok := rebaseHomeRef(entry, realHome); ok {
						paths[i] = rebased
						changed = true
					}
				}
			}
		}
	}
	if hooks, ok := settings["hooks"].(map[string]any); ok {
		for _, groups := range hooks {
			entries, ok := groups.([]any)
			if !ok {
				continue
			}
			for _, entry := range entries {
				entryMap, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				handlers, ok := entryMap["hooks"].([]any)
				if !ok {
					continue
				}
				for _, handler := range handlers {
					handlerMap, ok := handler.(map[string]any)
					if !ok {
						continue
					}
					command, ok := handlerMap["command"].(string)
					if !ok || command == "" {
						continue
					}
					if rebased, ok := rebaseHookCommand(command, realHome); ok {
						handlerMap["command"] = rebased
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return contents
	}
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return contents
	}
	return encoded
}

// rebaseHomeRef resolves a configured home prefix without changing other paths.
func rebaseHomeRef(path, realHome string) (string, bool) {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(realHome, strings.TrimPrefix(path, "~/")), true
	}
	if path == "~" {
		return realHome, true
	}
	if strings.HasPrefix(path, "$HOME/") {
		return filepath.Join(realHome, strings.TrimPrefix(path, "$HOME/")), true
	}
	if path == "$HOME" {
		return realHome, true
	}
	return path, false
}

// shellWordDelimiters are the characters that end a command word when a
// rebuilt expansion must stop pretending to be one word.
func isShellDelimiter(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '<', '>':
		return true
	}
	return false
}

// homeVarEnd matches HOME at a variable boundary, excluding HOMEBREW-style names.
func homeVarEnd(command string, index int) int {
	if !strings.HasPrefix(command[index:], "$HOME") {
		return -1
	}
	end := index + len("$HOME")
	if end < len(command) {
		boundary := command[end]
		if boundary >= 'a' && boundary <= 'z' || boundary >= 'A' && boundary <= 'Z' || boundary >= '0' && boundary <= '9' || boundary == '_' {
			return -1
		}
	}
	return end
}

// rebaseHookCommand preserves shell quoting and variable boundaries.
// Quote only the replaced home prefix, keeping wildcard suffixes expandable.
func rebaseHookCommand(command, realHome string) (string, bool) {
	realHome = strings.TrimSpace(realHome)
	if realHome == "" {
		return command, false
	}
	if strings.ContainsAny(realHome, "\"\n") {
		return command, false
	}
	needsQuote := strings.ContainsAny(realHome, " \t")
	var out strings.Builder
	changed := false
	inSingle := false
	inDouble := false

	for index := 0; index < len(command); index++ {
		char := command[index]
		atTokenStart := index == 0 || isShellDelimiter(command[index-1])

		if inSingle {
			if char == '\'' {
				inSingle = false
			}
			out.WriteByte(char)
			continue
		}
		if inDouble {
			if char == '\\' && index+1 < len(command) && strings.IndexByte("$\"\\\n", command[index+1]) >= 0 {
				out.WriteByte(char)
				out.WriteByte(command[index+1])
				index++
				continue
			}
			if char == '"' {
				inDouble = false
				out.WriteByte(char)
				continue
			}
			if end := homeVarEnd(command, index); end >= 0 {
				out.WriteString(realHome)
				changed = true
				index = end - 1
				continue
			}
			out.WriteByte(char)
			continue
		}
		// Unquoted context.
		if char == '\'' {
			inSingle = true
			out.WriteByte(char)
			continue
		}
		if char == '"' {
			inDouble = true
			out.WriteByte(char)
			continue
		}
		if char == '\\' && index+1 < len(command) {
			out.WriteByte(char)
			out.WriteByte(command[index+1])
			index++
			continue
		}
		if char == '~' && atTokenStart {
			rest := command[index+1:]
			endsWord := rest == "" || isShellDelimiter(rest[0])
			if endsWord || strings.HasPrefix(rest, "/") {
				writeExpandedHome(&out, realHome, needsQuote)
				changed = true
				// Copy the remainder verbatim but do not consume it: it may
				// itself contain references to rewrite, and the suffix's own
				// quoting and wildcards have to keep working.
				continue
			}
		}
		if char == '$' {
			if end := homeVarEnd(command, index); end >= 0 {
				writeExpandedHome(&out, realHome, needsQuote)
				changed = true
				index = end - 1
				continue
			}
		}
		out.WriteByte(char)
	}
	if !changed {
		return command, false
	}
	return out.String(), true
}

// writeExpandedHome quotes only the prefix so suffix expansions remain active.
func writeExpandedHome(out *strings.Builder, realHome string, needsQuote bool) {
	if !needsQuote {
		out.WriteString(realHome)
		return
	}
	out.WriteByte('"')
	out.WriteString(realHome)
	out.WriteByte('"')
}

// writePrivateFile writes contents atomically with owner-only permissions, so a
// mirrored credential is never briefly world-readable.
func writePrivateFile(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ao-command-code-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
