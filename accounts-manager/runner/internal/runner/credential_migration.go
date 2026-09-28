package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"gopkg.in/yaml.v3"
)

type legacyCredential struct {
	Fingerprint string `json:"fingerprint"`
	AccountID   string `json:"account_id"`
	Index       string `json:"index"`
}

type legacySource struct {
	Name        string
	Fingerprint string
	Auth        *coreauth.Auth
}

func credentialDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func migrateDraftCredentials(ctx context.Context, state *State, vault *credentialVault) (resultErr error) {
	cfg := state.Config
	if len(cfg.GeminiKey)+len(cfg.InteractionsKey)+len(cfg.OpenAICompatibility)+len(cfg.VertexCompatAPIKey)+len(cfg.XAIKey)+len(cfg.MetaKey) != 0 || cfg.SaveCooldownStatus || cfg.Home.Enabled {
		return errors.New("unsupported legacy credential configuration; recovery requires review")
	}
	sources, document, configRaw, err := draftCredentialSources(state, vault)
	if err != nil {
		return err
	}
	defer clear(configRaw)
	if len(sources) == 0 {
		return nil
	}
	if err := requireDraftRunnerStopped(ctx, vault); err != nil {
		return err
	}
	if err := vault.migrateCredentials(ctx, sources); err != nil {
		return err
	}
	for _, source := range sources {
		if !strings.HasPrefix(source.Name, "auth/") {
			continue
		}
		raw, err := vault.readFile(source.Name, 1<<20)
		if err != nil {
			return errCredentialStorage
		}
		unchanged := credentialDigest(raw) == source.Fingerprint
		clear(raw)
		if !unchanged {
			return errCredentialConflict
		}
		if err := vault.root.Remove(source.Name); err != nil {
			return errCredentialStorage
		}
	}
	authRoot, err := vault.root.OpenRoot(authDirName)
	if err != nil {
		return errCredentialStorage
	}
	err = syncVaultDirectory(authRoot)
	_ = authRoot.Close()
	if err != nil {
		return errCredentialStorage
	}
	if len(cfg.CodexKey)+len(cfg.ClaudeKey) == 0 {
		return nil
	}
	delete(document, "codex-api-key")
	delete(document, "claude-api-key")
	clean, err := yaml.Marshal(document)
	if err != nil {
		return errCredentialStorage
	}
	defer clear(clean)
	parsed, err := sdkconfig.ParseConfigBytes(clean)
	if err != nil || validateConfig(parsed, state.Config.AuthDir) != nil {
		return errCredentialStorage
	}
	current, err := vault.readFile(configFileName, 1<<20)
	if err != nil {
		return errCredentialStorage
	}
	unchanged := credentialDigest(current) == credentialDigest(configRaw)
	clear(current)
	if !unchanged {
		return errCredentialConflict
	}
	id, err := randomVaultID()
	if err != nil {
		return err
	}
	name := "configuration-" + id + ".pending"
	defer func() {
		if err := vault.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errCredentialStorage
		}
	}()
	if err := vault.writeNewFile(name, clean); err != nil {
		return err
	}
	if err := vault.root.Rename(name, configFileName); err != nil {
		return errCredentialStorage
	}
	if err := syncVaultDirectory(vault.root); err != nil {
		return errCredentialStorage
	}
	state.Config = parsed
	return nil
}

func draftCredentialSources(state *State, vault *credentialVault) ([]legacySource, map[string]any, []byte, error) {
	raw, err := vault.readFile(configFileName, 1<<20)
	if err != nil {
		return nil, nil, nil, errCredentialStorage
	}
	var document map[string]any
	if yaml.Unmarshal(raw, &document) != nil || document == nil {
		clear(raw)
		return nil, nil, nil, errCredentialStorage
	}
	sources := make([]legacySource, 0)
	for _, provider := range []string{"codex", "claude"} {
		value := document[provider+"-api-key"]
		if value == nil {
			continue
		}
		entries, ok := value.([]any)
		if !ok || len(entries) > 1000 {
			clear(raw)
			return nil, nil, nil, errCredentialConflict
		}
		for index, value := range entries {
			entry, ok := value.(map[string]any)
			if !ok {
				clear(raw)
				return nil, nil, nil, errCredentialConflict
			}
			for key := range entry {
				if key != "api-key" && key != "base-url" {
					clear(raw)
					return nil, nil, nil, errors.New("unsupported legacy key settings; recovery requires review")
				}
			}
			key, _ := entry["api-key"].(string)
			base, baseIsString := entry["base-url"].(string)
			if entry["base-url"] != nil && !baseIsString {
				clear(raw)
				return nil, nil, nil, errCredentialConflict
			}
			auth, err := parseCredentialCreate(credentialCreate{Provider: provider, Key: key, BaseURL: base}, true)
			if err != nil {
				clear(raw)
				return nil, nil, nil, err
			}
			old := &coreauth.Auth{Provider: provider, Attributes: map[string]string{"api_key": strings.TrimSpace(key), "base_url": strings.TrimSpace(base)}}
			auth.Index = old.EnsureIndex()
			encoded, _ := json.Marshal(entry)
			sources = append(sources, legacySource{Name: fmt.Sprintf("config:%s:%d", provider, index), Fingerprint: credentialDigest(encoded), Auth: auth})
			clear(encoded)
		}
	}
	entries, err := os.ReadDir(state.Config.AuthDir)
	if err != nil || len(entries) > 1000 {
		clear(raw)
		return nil, nil, nil, errCredentialStorage
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			clear(raw)
			return nil, nil, nil, errors.New("unsupported legacy credential file; recovery requires review")
		}
		name := "auth/" + entry.Name()
		credential, err := vault.readFile(name, 1<<20)
		if err != nil {
			clear(raw)
			return nil, nil, nil, errCredentialStorage
		}
		var identity struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(credential, &identity) != nil || !validVaultProvider(identity.Type) {
			clear(credential)
			clear(raw)
			return nil, nil, nil, errCredentialConflict
		}
		auth, err := parseCredentialCreate(credentialCreate{Provider: identity.Type, Credential: credential}, false)
		fingerprint := credentialDigest(credential)
		clear(credential)
		if err != nil {
			clear(raw)
			return nil, nil, nil, err
		}
		old := &coreauth.Auth{Provider: auth.Provider, FileName: filepath.Join(state.Config.AuthDir, entry.Name()), Metadata: auth.Metadata}
		auth.Index = old.EnsureIndex()
		sources = append(sources, legacySource{Name: name, Fingerprint: fingerprint, Auth: auth})
	}
	return sources, document, raw, nil
}

func (v *credentialVault) migrateCredentials(ctx context.Context, sources []legacySource) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.readyLocked(ctx); err != nil {
		return err
	}
	next := v.cloneLocked()
	indexes := make(map[string]string)
	for id, entry := range next.Records {
		if !entry.Deleted {
			auth, err := v.authLocked(id, entry)
			if err != nil {
				return err
			}
			indexes[auth.Index] = id
		}
	}
	for _, source := range sources {
		if prior, exists := next.Legacy[source.Name]; exists {
			if prior.Fingerprint != source.Fingerprint || prior.Index != source.Auth.Index {
				return errCredentialConflict
			}
			if _, exists := next.Records[prior.AccountID]; !exists {
				return errCredentialStorage
			}
			continue
		}
		var duplicate string
		for _, prior := range next.Legacy {
			if prior.Index == source.Auth.Index {
				if prior.Fingerprint != source.Fingerprint {
					return errCredentialConflict
				}
				duplicate = prior.AccountID
				break
			}
		}
		if duplicate != "" {
			next.Legacy[source.Name] = legacyCredential{source.Fingerprint, duplicate, source.Auth.Index}
			continue
		}
		if indexes[source.Auth.Index] != "" {
			return errCredentialConflict
		}
		auth, err := normalizedVaultAuth(source.Auth)
		if err != nil || auth.Index == "" {
			return errCredentialConflict
		}
		id, err := randomVaultID()
		if err != nil {
			return err
		}
		auth.ID, auth.FileName = id, ""
		auth.Attributes[vaultGenerationAttribute] = "1"
		auth.Attributes[coreauth.AttributeAuthIndexSeed] = id
		auth.CreatedAt, auth.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		entry := vaultEntry{Provider: auth.Provider, Generation: 1}
		entry.Sealed, err = v.sealAuth(auth, entry)
		if err != nil {
			return err
		}
		next.Records[id] = entry
		indexes[auth.Index] = id
		next.Legacy[source.Name] = legacyCredential{source.Fingerprint, id, auth.Index}
	}
	return v.persistLocked(next)
}

func requireDraftRunnerStopped(ctx context.Context, vault *credentialVault) error {
	raw, err := vault.readFile("runtime.json", 16<<10)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errCredentialStorage
	}
	var record RuntimeRecord
	if json.Unmarshal(raw, &record) != nil || record.Port < 1 || record.Port > 65535 {
		return errCredentialStorage
	}
	dialer := &net.Dialer{Timeout: time.Second}
	connection, err := dialer.DialContext(ctx, "tcp4", "127.0.0.1:"+strconv.Itoa(record.Port))
	if err == nil {
		_ = connection.Close()
		return errors.New("previous accounts runner must stop before credential recovery")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return errCredentialStorage
	}
	return nil
}
