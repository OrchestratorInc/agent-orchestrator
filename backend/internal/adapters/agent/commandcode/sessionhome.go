package commandcode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Command Code accepts no system-prompt launch flag, and neither the ACP path's
// `_meta` nor its mod loader is a usable channel: `cmd acp` accepts `--mod`,
// ignores it, and starts normally.
//
// The shell hooks do run under ACP, but they cannot carry standing instructions
// either. SessionStart is the one hook the ACP path never fires, and the other
// three reach the model as tool-result context: a PreToolUse `additionalContext`
// arrives wrapped in a `<hook_context>` tag, only once a tool runs, and Command
// Code's own model discounts it -- asked to repeat a token delivered that way, it
// answered that the note was "injected content from tool output, not a note I
// was ever given". A role prompt needs to be authoritative, not advice attached
// to a tool result.
//
// Memory is the one surface Command Code reads in every mode, and its own
// documentation places it in the system prompt rather than the conversation:
//
//	Memory is part of the system prompt, not the conversation.
//	  - It's re-read every request.
//	  - It survives compaction.
//
// The project tier is first-existing-wins, so a repository's own root AGENTS.md
// would silently shadow ours. The user tier is additive and relocatable through
// the documented HOME / USERPROFILE override, which is what this file uses: each
// AO session gets its own home, so concurrent sessions never share a memory file
// and a repo's own memory still loads alongside AO's.

// mirroredProfileFiles are copied from the user's real ~/.commandcode into the
// session home so the session can authenticate and keep its preferences,
// including the user's own permission rules, hooks and MCP servers. settings.json
// and mcp.json are separate supported inputs from config.json, not fields of it,
// so omitting them would silently drop user policy once HOME is redirected. Each
// file is independent: one missing or unreadable file must not stop the session
// from launching.
var mirroredProfileFiles = []string{"auth.json", "config.json", "providers.json", "settings.json", "mcp.json"}

// linkedHomeEntries are real-home dotfiles exposed inside the session home as
// links rather than copies. They cover developer tooling that must resolve
// under HOME and offers no environment redirect: SSH reads keys, config and
// known_hosts from ~/.ssh, so git-over-SSH and ssh-based tooling would
// otherwise break behind the redirect. Links keep a single source of truth --
// a copied key would silently go stale on rotation -- and cross no privilege
// boundary since the session already runs as the same user.
//
// Linking is best-effort per entry: platforms without symlink privilege (stock
// Windows) skip the link and the session proceeds without it, exactly as
// before. Nothing is ever overwritten: an existing session-side entry wins.
var linkedHomeEntries = []string{".ssh"}

// SessionHomeDir returns the per-session home directory for a Command Code
// process, or "" when AO has no data directory to build it under.
//
// The home lives under dataDir/harnesses/command-code, alongside the other
// harness-owned state, and deliberately outside the disposable prompts/<id>
// directory: relaunchSessionWithPolicyAndGeneration wipes that whole directory
// through cleanupSystemPromptDir on ordinary restore failures, and a failed
// restore of a session created by this change must not permanently delete its
// previous conversation.
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

// PrepareSessionHome builds the per-session home and writes AO's standing
// instructions into its user-tier AGENTS.md. It returns the home directory to
// point HOME / USERPROFILE at.
//
// Mirroring is best-effort per file: a user who has never logged in has no
// auth.json, and Command Code can still start for an explicitly configured
// provider. Only a failure to write the memory file itself is fatal, because a
// session whose memory is empty would run with no AO role at all.
//
// Re-preparing an existing home rewrites only AO's own files (AGENTS.md and the
// mirrored profile inputs). Native transcripts the session has already written
// there are left untouched.
func PrepareSessionHome(dataDir string, sessionID domain.SessionID, systemPrompt string) (string, error) {
	home := SessionHomeDir(dataDir, sessionID)
	if home == "" {
		return "", nil
	}
	if strings.TrimSpace(systemPrompt) == "" {
		return "", nil
	}
	profile := filepath.Join(home, ".commandcode")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return "", fmt.Errorf("command-code: create session profile: %w", err)
	}
	if source := realCommandCodeDir(); source != "" && source != profile {
		mirrorProfileFiles(source, profile)
		// Command Code resolves the user's own mods from $HOME/.commandcode/
		// mods, npm installs there as well, and relative configured mod paths
		// resolve against the home: mirroring the real directory is what keeps
		// the user's mods loading under the redirected HOME. Refreshed on
		// every launch, so a change made to the real mods tree, including a
		// `mods update` run inside an earlier session, lands next launch.
		mirrorDirectoryTree(filepath.Join(source, "mods"), filepath.Join(profile, "mods"))
	}
	if realHome, err := os.UserHomeDir(); err == nil && strings.TrimSpace(realHome) != "" && realHome != home {
		linkRealDotfiles(realHome, home)
		mirrorUserGitConfig(realHome, home)
	}
	memory := strings.TrimRight(systemPrompt, "\n") + "\n"
	if err := writePrivateFile(filepath.Join(profile, "AGENTS.md"), []byte(memory)); err != nil {
		return "", fmt.Errorf("command-code: write session memory: %w", err)
	}
	return home, nil
}

// MigrateNativeHistory copies a captured native conversation into the session
// home so `--resume` and `session/load` keep working after AO switches the
// session's HOME.
//
// Sessions created before this change wrote their transcripts under the real
// user's ~/.commandcode/projects/<slug>/, and Command Code resolves resume
// targets inside the home-scoped project catalog. Without migration, restoring
// such a session from its new isolated home searches an empty catalog and the
// conversation is unreachable. Only files belonging to this exact conversation
// id are copied, only when the destination does not already have them, and a
// missing or unreadable source is not an error: there may simply be nothing to
// migrate, for example on a fresh launch.
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

// SessionHomeEnv returns the environment overlay that points a Command Code
// process at its per-session home. HOME and USERPROFILE are both set because
// Command Code resolves ~/.commandcode through either one.
func SessionHomeEnv(home string) map[string]string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	return map[string]string{
		"HOME":        home,
		"USERPROFILE": home,
	}
}

// SessionProcessEnv returns the full environment overlay for a Command Code
// process: the per-session home plus passthroughs that keep the worker's own
// developer tooling working under the redirected HOME.
//
// The overlay is inherited by Command Code's shell commands and hooks, not just
// its memory loader, and the generated home contains only mirrors and links,
// never the real profile itself. Without passthroughs, a user whose author
// identity or credential helper lives in ~/.gitconfig would fail `git commit`
// inside every AO session, and gh would lose its login. GIT_CONFIG_GLOBAL
// repoints git's global config lookup at the real file without copying it;
// GH_CONFIG_DIR does the same for gh. Each is set only when its target exists,
// so users without either are unaffected.
//
// Decisions are made against the environment the child actually starts from:
// the daemon's ambient environment overlaid by the caller's session
// environment. An explicit GH_CONFIG_DIR or GIT_CONFIG_GLOBAL, whether
// configured per project or simply inherited by the daemon, is therefore never
// overwritten. Git loads $XDG_CONFIG_HOME/git/config and ~/.gitconfig
// independently and the latter wins, so the git passthrough stays on whenever
// the real file exists even when XDG is set; the real XDG git config itself is
// mirrored in PrepareSessionHome when XDG is unset. gh resolves to
// $XDG_CONFIG_HOME/gh the moment that variable is set, so GH_CONFIG_DIR is
// filled in only when it is not. Comparison is case-insensitive because
// Windows folds environment keys.
func SessionProcessEnv(dataDir string, sessionID domain.SessionID, base map[string]string) map[string]string {
	env := SessionHomeEnv(SessionHomeDir(dataDir, sessionID))
	if env == nil {
		return nil
	}
	realHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(realHome) == "" {
		return env
	}
	effective := effectiveEnv(base)
	if !hasEnvKey(effective, "GIT_CONFIG_GLOBAL") {
		if gitconfig := filepath.Join(realHome, ".gitconfig"); isRegularFile(gitconfig) {
			env["GIT_CONFIG_GLOBAL"] = gitconfig
		}
	}
	if !hasEnvKey(effective, "GH_CONFIG_DIR") && strings.TrimSpace(effective["XDG_CONFIG_HOME"]) == "" {
		if dir := ghConfigDir(realHome); dir != "" {
			env["GH_CONFIG_DIR"] = dir
		}
	}
	return env
}

// effectiveEnv merges the daemon's ambient environment with the caller's
// session overlay, which is what the child resolves before our additions are
// applied on either launch path. The overlay wins case-insensitively, matching
// how the flat environment behaves after Windows key folding.
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

// ghConfigDir locates the user's gh configuration without moving it: the
// platform default (%APPDATA%\GitHub CLI on Windows, ~/.config/gh elsewhere),
// used only when no XDG layer exists. Callers check XDG first.
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

// linkRealDotfiles exposes entries from the real home inside the session home
// without copying them. Copying would fork the source of truth -- a rotated
// key would leave a stale copy behind -- and private keys must never be
// duplicated onto disk by an agent launcher.
func linkRealDotfiles(realHome, sessionHome string) {
	for _, name := range linkedHomeEntries {
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

// mirrorUserGitConfig copies the real ~/.config/git directory into the
// session home when no XDG layer redirects git's second user config path.
// Git computes that path from HOME at startup, so behind the redirect it
// would otherwise resolve to the generated home and disappear; a user's
// split-over-XDG identity or credentials would silently stop applying.
// With $XDG_CONFIG_HOME set, the real directory stays directly readable, so
// copying it would be redundant. Each launch refreshes the copy.
func mirrorUserGitConfig(realHome, sessionHome string) {
	if strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")) != "" {
		return
	}
	source := filepath.Join(realHome, ".config", "git")
	if !isDirectory(source) {
		return
	}
	mirrorDirectoryTree(source, filepath.Join(sessionHome, ".config", "git"))
}

// mirrorDirectoryTree recursively copies a real directory into a mirrored
// one, overwriting existing files so each launch refreshes the session's
// copy. Nothing is ever deleted: session-side additions survive. Best-effort
// per entry, like every other mirror input; a single unreadable file must not
// stop the session from launching.
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

// mirrorProfileFiles copies the user's Command Code profile into the session
// home. Each file is independent: one missing or unreadable file must not stop
// the session from launching.
func mirrorProfileFiles(source, destination string) {
	for _, name := range mirroredProfileFiles {
		contents, err := os.ReadFile(filepath.Join(source, name)) //nolint:gosec // name is a fixed allowlist
		if err != nil {
			continue
		}
		if name == "settings.json" {
			contents = rebaseSettingsHomeRefs(contents, filepath.Dir(source))
		}
		_ = writePrivateFile(filepath.Join(destination, name), contents)
	}
}

// rebaseSettingsHomeRefs rewrites home-relative asset paths in a mirrored
// settings.json to absolute real-home paths.
//
// Copying the JSON alone leaves home-relative references broken: a skill entry
// like "~/.commandcode/custom-skills", a hook running
// `bash "$HOME/.commandcode/hooks/guard.sh"`, or a configured mod path all
// resolve inside the isolated home, where none of them exists, so configured
// skills disappear, user hooks cannot execute, and the user's mods stop
// loading. Command Code expands `~/` in configured skill and mod paths, so
// these references unambiguously mean the real home. Rewriting happens on AO's
// copy only; the user's file is never touched.
//
// Only `~/` and `$HOME` prefixes are rewritten, and only inside the `skills`
// list, the `mods.paths` list, and hook `command` strings -- permission rule
// patterns and other free text keep their literal form. Anything that fails to
// parse as JSON is returned unchanged rather than dropping the user's
// settings.
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

// rebaseHomeRef resolves a single configured path against the real home,
// reporting whether it changed. Absolute paths and non-home references pass
// through untouched.
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

// homeVarEnd reports the position just past a $HOME reference at index, or -1
// when the reference is not followed by a real variable boundary: $HOMEBREW
// and similar identifiers share the $HOME prefix and must stay untouched.
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

// rebaseHookCommand rewrites home-relative references in a shell command so
// they keep resolving to the real home while it runs under the session home.
// Shell semantics are preserved rather than guessed at:
//
//   - $HOME expands, and is rewritten, outside single quotes only; inside
//     single quotes it is intentionally literal. $HOMEBREW-style identifiers
//     that merely share the $HOME prefix are left alone.
//   - A leading ~ expands only when unquoted at the start of a word; inside
//     quotes it stays a literal character.
//   - A home whose path contains whitespace would split into two arguments
//     after naive substitution, so the rebuilt word is double-quoted to stay
//     one word, exactly like the tilde expansion it replaces. Words that
//     would break that quoting are left untouched.
//
// Anything ambiguous is left as written; a hook command that cannot be
// rewritten safely keeps running with its original source.
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
				if !needsQuote {
					out.WriteString(realHome)
					if strings.HasPrefix(rest, "/") {
						// Copy the remainder verbatim but do not consume it:
						// it may itself contain references to rewrite.
						changed = true
						continue
					}
					changed = true
					continue
				}
				if endsWord {
					out.WriteByte('"')
					out.WriteString(realHome)
					out.WriteByte('"')
					changed = true
					continue
				}
				end := tokenWordEnd(command, index+1)
				tail := command[index+1 : end]
				if strings.Contains(tail, "\"") {
					// The tail would end the inserted quoting; leave the
					// expansion to the runtime and keep scanning.
					out.WriteByte(char)
					continue
				}
				out.WriteByte('"')
				out.WriteString(realHome)
				out.WriteString(tail)
				out.WriteByte('"')
				changed = true
				index = end - 1
				continue
			}
		}
		if char == '$' {
			if end := homeVarEnd(command, index); end >= 0 {
				if !needsQuote {
					out.WriteString(realHome)
					changed = true
					index = end - 1
					continue
				}
				endsWord := end >= len(command) || isShellDelimiter(command[end])
				if endsWord {
					out.WriteByte('"')
					out.WriteString(realHome)
					out.WriteByte('"')
					changed = true
					index = end - 1
					continue
				}
				word := tokenWordEnd(command, end)
				tail := command[end:word]
				if strings.Contains(tail, "\"") {
					// Leave the reference as written; the tail would break
					// the inserted quoting. Emit it verbatim and resume
					// scanning after it.
					out.WriteString(command[index:end])
					index = end - 1
				} else {
					out.WriteByte('"')
					out.WriteString(realHome)
					out.WriteString(tail)
					out.WriteByte('"')
					changed = true
					index = word - 1
				}
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

// tokenWordEnd returns the index of the next shell delimiter at or after
// from, or the length of command. Escaped characters are not special here:
// a token whose quoting must be rebuilt around them is left alone.
func tokenWordEnd(command string, from int) int {
	end := from
	for end < len(command) && !isShellDelimiter(command[end]) {
		end++
	}
	return end
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
