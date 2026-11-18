package openhands

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// ConversationFiles lists only one native conversation, in SDK event order.
// Neither discovery refresh nor private preparation walks other conversations.
func ConversationFiles(ctx context.Context, root, nativeID string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(nativeID)
	if err != nil {
		return nil, err
	}
	name := strings.ReplaceAll(id.String(), "-", "")
	provider, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = provider.Close() }()
	for _, path := range []string{name, filepath.Join(name, "events")} {
		info, err := provider.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("openhands conversation must use real directories")
		}
	}
	state := filepath.Join(name, "base_state.json")
	info, err := provider.Lstat(state)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("openhands state must be a regular file")
	}
	dir, err := provider.Open(filepath.Join(name, "events"))
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return nil, err
	}
	var events []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "event-") && strings.HasSuffix(entry.Name(), ".json") {
			events = append(events, filepath.Join(root, name, "events", entry.Name()))
		}
	}
	slices.SortFunc(events, func(a, b string) int {
		aParts := strings.SplitN(filepath.Base(a), "-", 3)
		bParts := strings.SplitN(filepath.Base(b), "-", 3)
		aIndex, aErr := strconv.ParseUint(aParts[1], 10, 64)
		bIndex, bErr := strconv.ParseUint(bParts[1], 10, 64)
		if aErr == nil && bErr == nil && aIndex != bIndex {
			return cmp.Compare(aIndex, bIndex)
		}
		return strings.Compare(a, b)
	})
	if len(events) == 0 {
		return nil, errors.New("no openhands conversation events")
	}
	return append([]string{filepath.Join(root, state)}, events...), nil
}

// ValidateImportedState refuses credential-bearing SDK configuration before
// admission or copying. Default CLI persistence masks SecretStr values, and
// native resume replaces agent/workspace with the normal profile's values
// (SDK 1.21 state.py:336-363, utils/pydantic_secrets.py:25-65).
// Cipher/plaintext dumps, stored tool secrets and custom headers are not copied.
// Event/tool history is outside this configuration check and remains unchanged.
func ValidateImportedState(raw []byte, nativeID string) error {
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	id, err := uuid.Parse(nativeID)
	if err != nil {
		return err
	}
	stored, _ := state["id"].(string)
	actual, err := uuid.Parse(stored)
	if err != nil || actual != id {
		return errors.New("openhands native identity mismatch")
	}
	workspace, _ := state["workspace"].(map[string]any)
	cwd, _ := workspace["working_dir"].(string)
	if !filepath.IsAbs(cwd) || state["head_is_empty"] == true {
		return errors.New("invalid openhands conversation state")
	}
	for _, key := range []string{"agent", "secret_registry"} {
		if hasImportCredential(state[key]) {
			return errors.New("openhands stored credentials or secret configuration cannot be copied")
		}
	}
	return nil
}

func importCredentialKey(key string) bool {
	switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
	case "apikey", "awsaccesskeyid", "awssecretaccesskey", "awssessiontoken", "clientsecret", "accesstoken", "refreshtoken", "password", "token", "authorization":
		return true
	}
	return false
}

func hasImportCredential(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if importCredentialKey(key) && item != nil {
				text, ok := item.(string)
				if !ok || (strings.TrimSpace(text) != "" && text != "**********") {
					return true
				}
			}
			switch strings.ToLower(key) {
			case "extra_headers", "headers", "secret_sources", "env", "environment", "credentials", "secrets":
				if item != nil {
					fields, ok := item.(map[string]any)
					if !ok || len(fields) != 0 {
						return true
					}
				}
			}
			if strings.HasSuffix(strings.ToLower(key), "url") {
				if text, ok := item.(string); ok {
					parsed, err := url.Parse(text)
					if err == nil {
						if parsed.User != nil {
							return true
						}
						for name := range parsed.Query() {
							if importCredentialKey(name) {
								return true
							}
						}
					}
				}
			}
			if hasImportCredential(item) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if hasImportCredential(item) {
				return true
			}
		}
	}
	return false
}
