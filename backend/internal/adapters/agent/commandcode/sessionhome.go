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

var mirroredProfileFiles = []string{"auth.json", "config.json", "providers.json", "settings.json", "mcp.json"}

var linkedHomeEntries = []string{".ssh", ".npmrc", ".netrc", ".docker", ".aws"}

func SessionHomeDir(dataDir string, sessionID domain.SessionID) string {
	dataDir = strings.TrimSpace(dataDir)
	sessionID = domain.SessionID(strings.TrimSpace(string(sessionID)))
	if dataDir == "" || sessionID == "" {
		return ""
	}
	return filepath.Join(dataDir, "harnesses", "command-code", string(sessionID))
}

func realCommandCodeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".commandcode")
}

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

func SessionHomeEnv(home string) map[string]string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	return map[string]string{
		"HOME":        home,
		"USERPROFILE": home,
	}
}

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

func gitConfigGlobalScope(home string) bool {
	return isRegularFile(filepath.Join(home, sessionGitConfigCopiesDir, gitConfigGlobalScopeMarker))
}

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

func hasEnvKey(env map[string]string, key string) bool {
	for existing := range env {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}

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

func linkRealDotfiles(realHome, sessionHome string) {
	entries := append([]string(nil), linkedHomeEntries...)
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

func absoluteXDGConfigHome() string {
	xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if xdg == "" || !filepath.IsAbs(xdg) {
		return ""
	}
	return xdg
}

const (
	sessionGitConfigIncludeBegin = "# AO: the real user git configuration, included so it applies inside this session"
	sessionGitConfigIncludeEnd   = "# AO: end of the real user git configuration"
	sessionGitConfigCopiesDir    = ".ao-gitconfig"
	gitConfigIncludeMaxDepth     = 8

	gitConfigGlobalScopeMarker = "global-scope"
)

func preserveGitConfiguration(realHome, sessionHome string) {
	rewriter := &gitConfigRewriter{
		realHome:    realHome,
		sessionHome: sessionHome,
		copies:      map[string]string{},
	}
	var includes []string
	xdgRebased := false

	if xdg := absoluteXDGConfigHome(); xdg != "" {
		if source := filepath.Join(xdg, "git", "config"); isRegularFile(source) && rewriter.rebaseNeeded(source, 0) {
			if copyPath := rewriter.materialize(source); copyPath != source {
				includes = append(includes, copyPath)
				xdgRebased = true
			}
		}
	} else {
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

type gitConfigRewriter struct {
	realHome    string
	sessionHome string
	copies      map[string]string
	depth       int
}

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

func (r *gitConfigRewriter) relocate(source, dest string) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writePrivateFile(dest, []byte(r.rewrite(string(contents), source)))
}

func (r *gitConfigRewriter) rewrite(contents, source string) string {
	sourceDir := filepath.Dir(source)
	var out strings.Builder
	section := ""
	for _, line := range splitGitConfigLines(contents) {
		name, subsection, header := gitSectionHeader(line.text)
		if header {
			section = name
			if name == "includeif" {
				if condition, ok := r.anchorCondition(subsection, source); ok {
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

func (r *gitConfigRewriter) anchorCondition(condition, source string) (string, bool) {
	prefix, pattern, ok := gitConditionPattern(condition)
	if !ok {
		return "", false
	}
	anchored, ok := anchorConfigPattern(pattern, filepath.Dir(physicalPath(source)), physicalPath(r.realHome))
	if !ok {
		return "", false
	}
	return prefix + anchored, true
}

func physicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func gitConditionPattern(condition string) (string, string, bool) {
	lower := strings.ToLower(condition)
	for _, keyword := range []string{"gitdir/i:", "gitdir:"} {
		if strings.HasPrefix(lower, keyword) {
			return condition[:len(keyword)], condition[len(keyword):], true
		}
	}
	return "", "", false
}

func anchorConfigPattern(pattern, sourceDir, realHome string) (string, bool) {
	var base, rest string
	switch {
	case pattern == "." || strings.HasPrefix(pattern, "./"):
		base, rest = sourceDir, strings.TrimPrefix(strings.TrimPrefix(pattern, "."), "/")
	case pattern == "~" || strings.HasPrefix(pattern, "~/"):
		base, rest = realHome, strings.TrimPrefix(strings.TrimPrefix(pattern, "~"), "/")
	default:
		return "", false
	}
	anchored := filepath.ToSlash(base)
	if rest == "" {
		return anchored + "/", true
	}
	joined := filepath.ToSlash(filepath.Join(base, filepath.FromSlash(rest)))
	if strings.HasSuffix(rest, "/") && !strings.HasSuffix(joined, "/") {
		joined += "/"
	}
	return joined, true
}

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

type gitConfigLine struct {
	raw  string
	text string
}

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

func trailingBackslashes(line string) int {
	count := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		count++
	}
	return count
}

func gitSectionName(line string) string {
	name, _, _ := gitSectionHeader(line)
	return name
}

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

func gitConfigString(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + escaped + `"`
}

func gitConfigCopyName(target string) string {
	sum := sha256.Sum256([]byte(target))
	return hex.EncodeToString(sum[:8]) + ".gitconfig"
}

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

func isShellDelimiter(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '<', '>':
		return true
	}
	return false
}

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

func writeExpandedHome(out *strings.Builder, realHome string, needsQuote bool) {
	if !needsQuote {
		out.WriteString(realHome)
		return
	}
	out.WriteByte('"')
	out.WriteString(realHome)
	out.WriteByte('"')
}

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
