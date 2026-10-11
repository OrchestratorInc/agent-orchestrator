package poolacp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// BuildStandingHook returns private YAML and a POSIX helper for an unnamed,
// additive SessionStart hook. It does not install or execute either artifact.
// Caller must use exclusively owned private AO files and qualify fail-open
// behavior before admission. Windows cmd quoting is intentionally unsupported.
func BuildStandingHook(prompt, helperPath string) (settings, helper []byte, err error) {
	if !utf8.ValidString(prompt) || strings.ContainsRune(prompt, '\x00') {
		return nil, nil, fmt.Errorf("pool standing instructions must be valid UTF-8 without NUL")
	}
	// Reject the cap boundary too: native wrapper/marker accounting is unproved.
	if len(prompt) >= 32*1024 {
		return nil, nil, fmt.Errorf("pool standing instructions reach the native 32 KiB hook cap")
	}
	if !filepath.IsAbs(helperPath) || !strings.HasPrefix(helperPath, "/") || !utf8.ValidString(helperPath) || strings.ContainsAny(helperPath, "\x00\r\n") {
		return nil, nil, fmt.Errorf("pool hook requires an absolute POSIX helper path")
	}
	payload, err := json.Marshal(map[string]any{"hook_specific_output": map[string]string{"additional_context": prompt}})
	if err != nil { return nil, nil, err }
	if len(payload) >= 1024*1024 { return nil, nil, fmt.Errorf("pool hook output reaches native stdout cap") }
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	command, err := json.Marshal("/bin/sh " + quote(helperPath))
	if err != nil { return nil, nil, err }
	settings = []byte("hooks:\n  SessionStart:\n    - matcher: \"*\"\n      command: " + string(command) + "\n")
	helper = []byte("#!/bin/sh\nprintf '%s' " + quote(string(payload)) + "\n")
	return settings, helper, nil
}
