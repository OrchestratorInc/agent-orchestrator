package workerexec

import (
	"fmt"
	"os"
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

// checkoutSecretFileDenyRules extend the cwd-relative .env.* rules to a
// checkout outside the working directory (an additional repository beside the
// main checkout). The committed-example negations only carve out of
// cwd-relative rules, so an .env.example in such a checkout is denied too: the
// price of keeping its real .env files out of the agent's file tools.
func checkoutSecretFileDenyRules(dir string) []string {
	pattern := "/" + filepath.ToSlash(filepath.Clean(dir)) + "/**/.env.*"
	return []string{"Read(" + pattern + ")", "Edit(" + pattern + ")"}
}

// claudeCheckoutDenyRulesKey records, in Claude's settings, which
// permissions.deny entries checkoutSecretFileDenyRules added, so a later launch
// with other repositories replaces exactly those and never removes a rule of
// the same shape the user wrote.
const claudeCheckoutDenyRulesKey = "aoManagedCheckoutDenyRules"

// managedCheckoutDenyRules reads the rules AO recorded under
// claudeCheckoutDenyRulesKey.
func managedCheckoutDenyRules(settings map[string]any) map[string]bool {
	managed := map[string]bool{}
	if previous, ok := settings[claudeCheckoutDenyRulesKey].([]any); ok {
		for _, entry := range previous {
			if rule, ok := entry.(string); ok {
				managed[rule] = true
			}
		}
	}
	return managed
}

// applySecretFileProtection adds secretFileDenyRules, plus the .env.* rules
// for each checkout in extraDirs, to a Claude settings object's
// permissions.deny when protect is set, after any rules already there, and
// removes exactly those rules when it is not. Other deny rules are left as
// they are.
func applySecretFileProtection(settings map[string]any, protect bool, extraDirs []string) error {
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
	managed := managedCheckoutDenyRules(settings)
	deny := make([]any, 0, len(existing)+len(secretFileDenyRules))
	kept := map[string]bool{}
	for _, rule := range existing {
		if !slices.Contains(secretFileDenyRules, rule) && !managed[rule] {
			deny = append(deny, rule)
			kept[rule] = true
		}
	}
	var added []any
	if protect {
		for _, rule := range secretFileDenyRules {
			deny = append(deny, rule)
		}
		for _, dir := range extraDirs {
			for _, rule := range checkoutSecretFileDenyRules(dir) {
				if kept[rule] {
					continue // The user already denies it; it stays theirs.
				}
				kept[rule] = true
				deny = append(deny, rule)
				added = append(added, rule)
			}
		}
	}
	if len(added) == 0 {
		delete(settings, claudeCheckoutDenyRulesKey)
	} else {
		settings[claudeCheckoutDenyRulesKey] = added
	}
	if len(deny) == 0 {
		delete(permissions, "deny")
		return nil
	}
	permissions["deny"] = deny
	return nil
}

// writeClaudeChatSettings applies the secret-file protection (deny rules and
// the shell guard hook) and the access to the extraDirs checkouts to the
// settings.json in Claude's config directory, which both the terminal CLI and
// the Chat ACP adapter load as user settings. A session with neither and no
// settings yet is left without a settings file.
func (b HarnessBuilder) writeClaudeChatSettings(configDir string, extraDirs []string) error {
	path := filepath.Join(configDir, "settings.json")
	if !b.ProtectSecretFiles && len(extraDirs) == 0 {
		if _, err := os.Stat(path); err != nil {
			return nil
		}
	}
	var applyErr error
	err := updateJSONFile(path, func(settings map[string]any) {
		applyClaudeAdditionalDirectories(settings, extraDirs)
		applyErr = applySecretFileProtection(settings, b.ProtectSecretFiles, extraDirs)
		applySecretFileGuardHook(settings, b.secretFileGuard())
	})
	if err != nil {
		return fmt.Errorf("write Claude settings: %w", err)
	}
	if applyErr != nil {
		return fmt.Errorf("protect secret files in Claude settings: %w", applyErr)
	}
	return nil
}
