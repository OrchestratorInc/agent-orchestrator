package agyacp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// oauthPersonal is Antigravity's "Log in with Google" ACP auth method.
const oauthPersonal = "oauth-personal"

// authTimeout bounds the one-time authenticate call. With an existing Google
// sign-in it completes in a few seconds; Zed users report the interactive flow
// hanging, so AO never waits on it indefinitely.
const authTimeout = 20 * time.Second

// authBootstrap selects Antigravity's Google sign-in for the ACP server when
// the user is already signed in but the server has no auth method configured.
//
// Without a configured method, session/new answers "Authentication required".
// ACP authenticate can start a browser OAuth flow, so AO calls it only when
// GEMINI_HOME already records an active Google account; the agent then reuses
// that sign-in and persists the choice to its own settings. In every other
// case AO leaves auth to the user and surfaces the ordinary sign-in error.
type authBootstrap struct {
	geminiHome func(map[string]string) (string, error)
	timeout    time.Duration
}

func newAuthBootstrap(geminiHome func(map[string]string) (string, error)) authBootstrap {
	return authBootstrap{geminiHome: geminiHome, timeout: authTimeout}
}

func (b authBootstrap) run(ctx context.Context, conn *acpsdk.ClientSideConnection, init acpsdk.InitializeResponse, cfg acpdriver.LaunchConfig) error {
	return b.bootstrap(ctx, init, cfg.Env, func(ctx context.Context, methodID string) error {
		_, err := conn.Authenticate(ctx, acpsdk.AuthenticateRequest{MethodId: methodID})
		return err
	})
}

// bootstrap decides whether to authenticate and, if so, calls authenticate
// with a bounded context.
func (b authBootstrap) bootstrap(
	ctx context.Context,
	init acpsdk.InitializeResponse,
	env map[string]string,
	authenticate func(context.Context, string) error,
) error {
	if !advertises(init, oauthPersonal) {
		return nil
	}
	home, err := b.geminiHome(env)
	if err != nil {
		return nil
	}
	if authConfigured(home) || !googleSignedIn(home) {
		return nil
	}
	authCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	if err := authenticate(authCtx, oauthPersonal); err != nil {
		return fmt.Errorf("authenticate %s: %w", oauthPersonal, err)
	}
	return nil
}

func advertises(init acpsdk.InitializeResponse, methodID string) bool {
	for _, method := range init.AuthMethods {
		if authMethodID(method) == methodID {
			return true
		}
	}
	return false
}

// authMethodID reads the id of any auth-method variant the SDK decodes.
func authMethodID(method acpsdk.AuthMethod) string {
	raw, err := json.Marshal(method)
	if err != nil {
		return ""
	}
	var shape struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &shape) != nil {
		return ""
	}
	return shape.ID
}

// authConfigured reports whether Antigravity's ACP settings already name an
// auth method, in which case the agent authenticates on its own.
func authConfigured(geminiHome string) bool {
	var settings struct {
		Auth struct {
			Type string `json:"type"`
		} `json:"auth"`
	}
	if !readJSON(filepath.Join(geminiHome, agy.ACPStorageDir, "settings.json"), &settings) {
		return false
	}
	return strings.TrimSpace(settings.Auth.Type) != ""
}

// googleSignedIn reports whether GEMINI_HOME records an active Google account,
// which the Antigravity CLI and IDE write after a completed sign-in.
func googleSignedIn(geminiHome string) bool {
	var accounts struct {
		Active string `json:"active"`
	}
	if !readJSON(filepath.Join(geminiHome, "google_accounts.json"), &accounts) {
		return false
	}
	return strings.TrimSpace(accounts.Active) != ""
}

func readJSON(path string, out any) bool {
	data, err := os.ReadFile(path) //nolint:gosec // path is under the user's GEMINI_HOME.
	if err != nil {
		return false
	}
	return json.Unmarshal(data, out) == nil
}
