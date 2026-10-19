// The unregistered Hermes ACP candidate uses supported native configuration.
// Native hook errors, replay races and AO lifecycle conformance remain gated.
package hermesacp

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	// Register the SQLite driver used for read-only native history validation.
	_ "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

//go:embed context_plugin.py
var contextPlugin []byte

const pluginName = "ao-standing-context"
const maxContextBytes = 8192

// PrepareProfile adds a supported per-turn context plugin to an exclusively
// owned isolated profile. Callers must never pass the user's native profile.
// Native defaults, user plugins and cached system prompts are preserved. This
// is candidate configuration, not proof of execution or a fail-closed boundary.
func PrepareProfile(ctx context.Context, home, instructions string) error {
	return prepareProfile(ctx, home, instructions, false)
}

func prepareLaunchProfile(ctx context.Context, home, instructions string) error {
	return prepareProfile(ctx, home, instructions, true)
}

func prepareProfile(ctx context.Context, home, instructions string, refresh bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(home) {
		return fmt.Errorf("hermes ACP: isolated home must be absolute")
	}
	if !utf8.ValidString(instructions) || len(instructions) > maxContextBytes {
		return fmt.Errorf("hermes ACP: standing context exceeds 8192 UTF-8 bytes")
	}
	// Refuse symlinked profile components before writing any managed files.
	for path := filepath.Clean(home); ; path = filepath.Dir(path) {
		st, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("hermes ACP: symlinked profile")
		}
		if path == filepath.Dir(path) {
			break
		}
	}
	configPath := filepath.Join(home, "config.yaml")
	if st, err := os.Lstat(configPath); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("hermes ACP: regular isolated config required")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	config := map[string]any{}
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return err
	}
	if config == nil {
		return fmt.Errorf("hermes ACP: empty isolated config")
	}
	plugins, ok := config["plugins"].(map[string]any)
	if config["plugins"] != nil && !ok {
		return fmt.Errorf("hermes ACP: malformed plugin configuration")
	}
	if plugins == nil {
		plugins = map[string]any{}
		config["plugins"] = plugins
	}
	// A missing allow-list may have native migration semantics. Do not implicitly
	// disable existing user plugins by inventing an empty list.
	enabled, ok := plugins["enabled"].([]any)
	if !ok {
		return fmt.Errorf("hermes ACP: explicit native plugin allow-list required")
	}
	disabled, _ := plugins["disabled"].([]any)
	for _, name := range disabled {
		if text, ok := name.(string); ok && text == pluginName {
			return fmt.Errorf("hermes ACP: managed plugin explicitly disabled")
		}
	}
	found := false
	for _, name := range enabled {
		if text, ok := name.(string); ok && text == pluginName {
			found = true
		}
	}
	if !found {
		plugins["enabled"] = append(enabled, pluginName)
	}
	// Native per-hook spilling is lossy. Reject a cap that could truncate this
	// bounded context instead of weakening the user's configured limit.
	if hooks, ok := config["hooks"].(map[string]any); ok {
		if spill, ok := hooks["output_spill"].(map[string]any); ok {
			enabled, valid := spill["enabled"].(bool)
			if spill["enabled"] != nil && !valid {
				return fmt.Errorf("hermes ACP: malformed hook spill policy")
			}
			explicitlyDisabled := valid && !enabled
			if !explicitlyDisabled {
				limit, valid := spill["max_chars"].(int)
				if spill["max_chars"] != nil && !valid {
					return fmt.Errorf("hermes ACP: integer hook spill limit required")
				}
				if limit > 0 && limit < len([]rune(instructions)) {
					return fmt.Errorf("hermes ACP: native hook spill limit would truncate context")
				}
			}
		}
	}
	pluginRoot := filepath.Join(home, "plugins")
	if err := os.Mkdir(pluginRoot, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	if st, err := os.Lstat(pluginRoot); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("hermes ACP: unsafe plugin directory")
	}
	dir := filepath.Join(pluginRoot, pluginName)
	manifest := []byte("name: ao-standing-context\nversion: \"0.1.0\"\ndescription: Isolated AO ACP standing context candidate\nprovides_hooks: [pre_llm_call]\n")
	created := false
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !refresh || !os.IsExist(err) {
			return fmt.Errorf("hermes ACP: managed plugin collision: %w", err)
		}
		st, err := os.Lstat(dir)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("hermes ACP: unsafe managed plugin")
		}
		// Never overwrite user changes or follow symlinks while refreshing a
		// generation. Only our exact embedded implementation may own this name.
		for name, expected := range map[string][]byte{"__init__.py": contextPlugin, "plugin.yaml": manifest} {
			path := filepath.Join(dir, name)
			st, err := os.Lstat(path)
			if err != nil || !st.Mode().IsRegular() {
				return fmt.Errorf("hermes ACP: unsafe managed plugin file")
			}
			raw, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, expected) {
				return fmt.Errorf("hermes ACP: managed plugin modified")
			}
		}
		st, err = os.Lstat(filepath.Join(dir, "instructions.txt"))
		if err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("hermes ACP: unsafe managed instructions")
		}
	} else {
		created = true
	}
	if created {
		for name, contents := range map[string][]byte{"__init__.py": contextPlugin, "plugin.yaml": manifest} {
			if err := os.WriteFile(filepath.Join(dir, name), contents, 0600); err != nil {
				return err
			}
		}
	}
	// Replace, never truncate, so a native hook sees one complete bounded value
	// and existing hard links cannot redirect writes to source-profile files.
	private, err := os.CreateTemp(dir, ".instructions-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(private.Name()) }()
	if _, err := private.WriteString(instructions); err != nil {
		_ = private.Close()
		return err
	}
	if err := private.Close(); err != nil {
		return err
	}
	if err := os.Rename(private.Name(), filepath.Join(dir, "instructions.txt")); err != nil {
		return err
	}

	raw, err = yaml.Marshal(config)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(home, ".ao-config-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), configPath)
}

// ValidateHistory rejects ordinary missing, empty, foreign-source, compressed
// or wrong-workspace history before a candidate can issue session/load. It does
// not write native state and never calls session/resume (which can create fresh
// history). A successful check alone does not prove native replay completeness
// or close the validation/load race; production registration remains gated.
func ValidateHistory(ctx context.Context, home, id, workspace string) error {
	fail := func() error {
		return fmt.Errorf("%w: Hermes ACP history unavailable or workspace mismatch", ports.ErrChatResumeFailed)
	}
	if id == "" || id == "latest" || strings.ContainsAny(id, " /\\\r\n\t") || !filepath.IsAbs(home) || !filepath.IsAbs(workspace) {
		return fail()
	}
	expected, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fail()
	}
	for p := filepath.Clean(home); ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fail()
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	st, err := os.Lstat(filepath.Join(home, "state.db"))
	if err != nil || !st.Mode().IsRegular() {
		return fail()
	}
	path := filepath.ToSlash(filepath.Join(home, "state.db"))
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(1000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fail()
	}
	defer func() { _ = db.Close() }()
	var source, cwd, meta string
	var messages, children int
	err = db.QueryRowContext(ctx, `SELECT source, COALESCE(cwd,''), COALESCE(model_config,''),
 (SELECT count(*) FROM messages WHERE session_id=sessions.id AND active=1),
 (SELECT count(*) FROM sessions child WHERE child.parent_session_id=sessions.id)
 FROM sessions WHERE id=? AND parent_session_id IS NULL`, id).Scan(&source, &cwd, &meta, &messages, &children)
	if err != nil || source != "acp" || messages == 0 || children != 0 {
		return fail()
	}
	var modelConfig struct {
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal([]byte(meta), &modelConfig) != nil {
		return fail()
	}
	for _, stored := range []string{cwd, modelConfig.Cwd} {
		if !filepath.IsAbs(stored) {
			return fail()
		}
		canonical, err := filepath.EvalSymlinks(stored)
		if err != nil || canonical != expected {
			return fail()
		}
	}
	return ctx.Err()
}
