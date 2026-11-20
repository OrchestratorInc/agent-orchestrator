package kimi

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var kimiOAuthSlot = regexp.MustCompile(`^oauth/[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var errKimiSignIn = errors.New("kimi: sign in to Kimi with `kimi login`, then import or open this conversation again")

// CheckImportedOAuth keeps valid OAuth histories discoverable, but refuses
// registration/preparation with an actionable reason when native login is absent.
// It performs no login, refresh, config write or credential seeding.
func CheckImportedOAuth(ctx context.Context, home, model string) error {
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	data, err := readKimiFile(ctx, root, "config.toml", 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil // Non-OAuth history parsing does not require a provider config.
	}
	if err != nil {
		return err
	}
	var config kimiImportConfig
	if toml.Unmarshal(data, &config) != nil {
		return errors.New("kimi: cannot read native provider configuration")
	}
	if model == "" {
		model = config.DefaultModel
	}
	providerID, _ := config.Models[model]["provider"].(string)
	provider := config.Providers[providerID]
	if len(provider.OAuth) == 0 && providerID != "managed:kimi-code" {
		return nil
	}
	ref, err := kimiImportOAuthRef(provider.OAuth)
	if err != nil {
		return err
	}
	return kimiImportedOAuthReady(ctx, home, ref)
}

func kimiImportOAuthRef(ref map[string]string) (map[string]string, error) {
	if len(ref) == 0 {
		ref = map[string]string{"storage": "file", "key": "oauth/kimi-code"}
	}
	if ref["storage"] != "file" || !kimiOAuthSlot.MatchString(ref["key"]) {
		return nil, errors.New("kimi: imported OAuth requires Kimi's native file credential reference")
	}
	for key, value := range ref {
		switch key {
		case "storage", "key":
		case "oauthHost", "oauth_host":
			endpoint, err := url.Parse(value)
			if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
				return nil, errors.New("kimi: unsupported native OAuth host")
			}
		default:
			return nil, errors.New("kimi: unsupported native OAuth configuration")
		}
	}
	return ref, nil
}

func kimiImportedOAuthReady(ctx context.Context, home string, ref map[string]string) error {
	root, err := os.OpenRoot(filepath.Join(home, "credentials"))
	if err != nil {
		return errKimiSignIn
	}
	defer func() { _ = root.Close() }()
	data, err := readKimiFile(ctx, root, strings.TrimPrefix(ref["key"], "oauth/")+".json", 1<<20)
	if err != nil {
		return errKimiSignIn
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_at"`
	}
	if json.Unmarshal(data, &token) != nil || token.Access == "" || (token.Expires != 0 && token.Expires <= time.Now().Unix() && token.Refresh == "") {
		return errKimiSignIn
	}
	// An expired access token with a refresh token is deliberately admitted:
	// Kimi performs the supported refresh against the shared store and locks.
	return nil
}

// CleanupWorkspace removes only the private home's native-auth symlinks. Native
// tokens, refresh locks and the user's profile remain owned by Kimi.
func (p *Plugin) CleanupWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	home := cfg.ImportConfigDir
	if !IsImportedHome(home, cfg.DataDir) {
		return nil
	}
	root, err := os.OpenRoot(home)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	data, err := readKimiFile(ctx, root, ".ao-import.json", 1<<20)
	if err != nil {
		return err
	}
	var proof kimiImportProof
	if json.Unmarshal(data, &proof) != nil || proof.Home != filepath.Base(home) || proof.ID != cfg.NativeSessionID || !filepath.IsAbs(proof.Source) {
		return errors.New("kimi: cannot clean an unverified imported binding")
	}
	for _, name := range []string{"credentials", "oauth"} {
		link, err := root.Readlink(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !sameKimiConfigPath(link, filepath.Join(proof.Source, name)) {
			return errors.New("kimi: imported credential link does not match its source")
		}
		if err := root.Remove(name); err != nil {
			return err
		}
	}
	return nil
}
