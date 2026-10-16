package workerexec

import (
	"fmt"
	"path/filepath"
	"slices"
)

// secretFileDenyRules keep Claude Code's file tools out of secret environment
// files. Claude Code evaluates deny rules in every permission mode, including
// bypassPermissions, and applies Read and Edit deny rules to its built-in
// file tools and to the file commands it recognizes in Bash (cat, head, tail,
// sed, tee and redirections). A Read deny also blocks Edit and Write on the
// same path; the Edit rules cover the remaining editing tools.
//
// "//**/.env" is anchored at the filesystem root, so a .env anywhere is
// covered, including the template's own checkouts beside the session's.
// ".env.*" is a bare filename and matches at any depth under the working
// directory; the "!" rules after it are gitignore negations that keep the
// committed examples readable. Negations only carve out of cwd-relative rules
// listed before them in the same file, which is why .env.* is not anchored at
// the root.
var secretFileDenyRules = []string{
	"Read(//**/.env)",
	"Edit(//**/.env)",
	"Read(.env.*)",
	"Edit(.env.*)",
	"Read(!.env.example)",
	"Edit(!.env.example)",
	"Read(!.env.sample)",
	"Edit(!.env.sample)",
	"Read(!.env.template)",
	"Edit(!.env.template)",
}

// applySecretFileProtection adds secretFileDenyRules to a Claude settings
// object's permissions.deny when protect is set, after any rules already
// there, and removes exactly those rules when it is not. Other deny rules are
// left as they are.
func applySecretFileProtection(settings map[string]any, protect bool) error {
	permissions, _ := settings["permissions"].(map[string]any)
	if permissions == nil {
		if !protect {
			return nil
		}
		permissions = map[string]any{}
		settings["permissions"] = permissions
	}
	var existing []string
	switch value := permissions["deny"].(type) {
	case nil:
	case []any:
		for _, entry := range value {
			rule, ok := entry.(string)
			if !ok {
				return fmt.Errorf("permissions.deny holds a non-string entry %v", entry)
			}
			existing = append(existing, rule)
		}
	default:
		return fmt.Errorf("permissions.deny is %T, not a list", value)
	}
	deny := make([]any, 0, len(existing)+len(secretFileDenyRules))
	for _, rule := range existing {
		if !slices.Contains(secretFileDenyRules, rule) {
			deny = append(deny, rule)
		}
	}
	if protect {
		for _, rule := range secretFileDenyRules {
			deny = append(deny, rule)
		}
	}
	if len(deny) == 0 {
		delete(permissions, "deny")
		return nil
	}
	permissions["deny"] = deny
	return nil
}

// writeClaudeSecretFileProtection applies the protection (deny rules and the
// shell guard hook) to the settings.json in Claude's config directory, which
// both the terminal CLI and the Chat ACP adapter load as user settings.
func (b HarnessBuilder) writeClaudeSecretFileProtection(configDir string) error {
	var applyErr error
	err := updateJSONFile(filepath.Join(configDir, "settings.json"), func(settings map[string]any) {
		applyErr = applySecretFileProtection(settings, b.ProtectSecretFiles)
		applySecretFileGuardHook(settings, b.secretFileGuard())
	})
	if err != nil {
		return err
	}
	return applyErr
}
