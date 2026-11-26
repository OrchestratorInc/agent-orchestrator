package agentaccount

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// agy keeps the computer's own sign-in in the OS keyring (the Keychain,
// Credential Manager, the Secret Service): one login per computer, whatever
// folder agy runs with. AGY_CLI_FORCE_FILE_TOKEN_STORAGE makes it keep the
// sign-in in a file of its --gemini-dir instead, the store agy already uses
// where no keyring exists, so each account folder holds a sign-in of its own.
const (
	agyFileTokenEnv = "AGY_CLI_FORCE_FILE_TOKEN_STORAGE" //nolint:gosec // a variable name, not a credential
	agyTokenFile    = "antigravity-oauth-token"          //nolint:gosec // a file name, not a credential
)

// agyHomeEnv is AO's name for agy's --gemini-dir. agy itself reads only the
// flag; AO's agy launches pass this variable as it.
const agyHomeEnv = "GEMINI_HOME"

// agyStorageDirs are where the terminal CLI and the ACP server keep their data.
// Each is a real folder of the account, because the sign-in file sits inside
// it; its shareable entries link to the computer's own.
var agyStorageDirs = []string{agy.CLIStorageDir, agy.ACPStorageDir}

// agyKnownShared are shared even before the computer's own folder has them, so
// a conversation started under one account resumes under every other.
var agyKnownShared = []string{"conversations", "brain", "annotations", "knowledge", "skills"}

// agySharedFiles are the storage folders' files that follow the computer: the
// user's settings, key bindings and prompt history. Any other file, such as a
// SQLite index whose journal could split between two folders, stays private.
var agySharedFiles = map[string]bool{"settings.json": true, "keybindings.json": true, "history.jsonl": true}

// agyPrivate stay inside the account's storage folders: its sign-in, caches
// (its plan's models and its first-run state), logs, crash reports, the
// presence of running instances and the updater's state.
var agyPrivate = map[string]bool{
	agyTokenFile: true, "cache": true, "log": true, "logs": true, "crashes": true,
	"presence": true, "updater": true, "tmp": true,
}

// agyTopPrivate are entries at the top of the computer's folder that name a
// login: the Google accounts Antigravity and the Gemini CLI signed in with.
var agyTopPrivate = map[string]bool{"google_accounts.json": true, "oauth_creds.json": true}

// agyCredentialName reports an entry whose name says it holds a credential,
// such as an MCP server's OAuth tokens. It is never shared.
func agyCredentialName(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "oauth") || strings.Contains(name, "token") || strings.Contains(name, "cred")
}

// agySignInNeeded matches agy's answer when it has no sign-in.
var agySignInNeeded = regexp.MustCompile(`(?i)authentication required|please visit the url to log in|please sign in|not signed in`)

// agyProbeTimeout bounds the `agy models` check of the computer's own login.
// Listing models spends no quota and starts no sign-in.
const agyProbeTimeout = 20 * time.Second

// Antigravity runs the `agy` CLI under an account folder passed as its
// --gemini-dir. A saved account's identity is the email agy records beside
// its file sign-in; AO reads only that label and whether a token is present.
type Antigravity struct {
	// Binary resolves the agy executable.
	Binary func(context.Context) (string, error)
	// DeviceHome is the computer's own agy folder (~/.gemini).
	DeviceHome string
}

var _ ports.AgentAccountProviderCLI = (*Antigravity)(nil)

// Provider names Antigravity.
func (*Antigravity) Provider() domain.AgentAccountProvider {
	return domain.AgentAccountProviderAntigravity
}

// HasLimits is false: AO reads no usage limits for Antigravity accounts, so a
// session never moves between them at a limit.
func (*Antigravity) HasLimits() bool { return false }

// LaunchEnv points agy at home and keeps its sign-in in home's file.
func (*Antigravity) LaunchEnv(home string) map[string]string {
	if home == "" {
		return nil
	}
	return map[string]string{agyHomeEnv: home, agyFileTokenEnv: "true"}
}

// LoginArgv is agy itself: it asks for a Google sign-in when it has none.
func (a *Antigravity) LoginArgv(ctx context.Context) ([]string, error) {
	bin, err := a.Binary(ctx)
	if err != nil {
		return nil, err
	}
	return []string{bin}, nil
}

// HomeLoginArgv is agy run in home. agy reads its folder from its command
// line only, so the sign-in names it there as well as in LaunchEnv.
func (a *Antigravity) HomeLoginArgv(ctx context.Context, home string) ([]string, error) {
	argv, err := a.LoginArgv(ctx)
	if err != nil || home == "" {
		return argv, err
	}
	return append(argv, "--gemini-dir", home), nil
}

// Logout removes home's file sign-in. agy signs out only from inside its own
// screen; the folder is AO's, and without the file agy is signed out there.
// The computer's own login is the user's to sign out in agy.
func (*Antigravity) Logout(_ context.Context, home string) error {
	if home == "" {
		return errors.New("sign out of agy's own login in agy")
	}
	var errs []error
	for _, dir := range agyStorageDirs {
		if err := os.Remove(filepath.Join(home, dir, agyTokenFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Identity reports who home is signed in to: the email beside its file
// sign-in. The computer's own login is in the keyring, where AO does not read;
// `agy models` shows whether it works, without an email.
func (a *Antigravity) Identity(ctx context.Context, home string) (domain.AgentAccountIdentityCheck, error) {
	folder := home
	if folder == "" {
		folder = a.DeviceHome
	}
	if identity, found, err := readAgySignIn(folder); found || err != nil {
		return identity, err
	}
	if home != "" {
		return domain.AgentAccountIdentityCheck{}, nil // no sign-in of its own yet
	}
	return a.deviceIdentity(ctx)
}

func (a *Antigravity) deviceIdentity(ctx context.Context) (domain.AgentAccountIdentityCheck, error) {
	bin, err := a.Binary(ctx)
	if err != nil {
		return domain.AgentAccountIdentityCheck{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, agyProbeTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, bin, "models")
	// A timeout or kill stops the CLI's whole process tree, not only its leader.
	aoprocess.ConfigureTreeCancellation(cmd)
	// A throwaway cwd keeps a project's own agy settings out of the probe.
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	switch {
	case err == nil:
		return domain.AgentAccountIdentityCheck{SignedIn: true, LoggedIn: true}, nil
	case ctx.Err() != nil:
		// A killed CLI reports "signal: killed"; the reason is the deadline.
		return domain.AgentAccountIdentityCheck{}, ctx.Err()
	case agySignInNeeded.Match(out):
		return domain.AgentAccountIdentityCheck{}, nil
	}
	return domain.AgentAccountIdentityCheck{}, fmt.Errorf("agy models: %w", err)
}

// readAgySignIn reads the email agy records with a file sign-in in folder.
// found is false when folder has none. The token is only checked for presence.
func readAgySignIn(folder string) (identity domain.AgentAccountIdentityCheck, found bool, err error) {
	for _, dir := range agyStorageDirs {
		data, err := os.ReadFile(filepath.Join(folder, dir, agyTokenFile))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return identity, true, fmt.Errorf("read agy sign-in: %w", err)
		}
		var login struct {
			Token struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
			} `json:"token"`
			IDToken      string `json:"id_token"`
			IDTokenEmail string `json:"id_token_email"`
		}
		// The parse error is not wrapped: its text could quote the file.
		if json.Unmarshal(data, &login) != nil {
			return identity, true, errors.New("read agy sign-in: the file is not agy's sign-in")
		}
		signedIn := login.Token.RefreshToken != "" || login.Token.AccessToken != ""
		email := login.IDTokenEmail
		if email == "" {
			email = jwtEmail(login.IDToken)
		}
		return domain.AgentAccountIdentityCheck{SignedIn: signedIn, LoggedIn: signedIn, Email: email}, true, nil
	}
	return identity, false, nil
}

// jwtEmail is the email claim of an ID token, unverified: it labels the
// account and is never trusted for anything else. Older agy versions record
// the ID token in place of id_token_email.
func jwtEmail(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Email
}

// Limits is always ErrAgentAccountNoLimits; see HasLimits.
func (*Antigravity) Limits(context.Context, string) (domain.AgentAccountLimits, error) {
	return domain.AgentAccountLimits{}, ports.ErrAgentAccountNoLimits
}

// Prepare shares the computer's own agy folder with home, except what names a
// login and the private parts of agy's storage folders. Conversations, brain
// files, skills, settings and MCP config follow every account, so a
// conversation resumes under any of them.
func (a *Antigravity) Prepare(home string) error {
	if home == "" {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if a.DeviceHome == "" {
		return fmt.Errorf("%w: the computer's own agy folder is unknown", ports.ErrAgentAccountUnshared)
	}
	errs := []error{shareAgyEntries(home, a.DeviceHome, nil, func(name string, _ bool) bool {
		return !slices.Contains(agyStorageDirs, name) && !agyTopPrivate[name] && !agyCredentialName(name) && !strings.HasSuffix(name, ".tmp")
	})}
	for _, dir := range agyStorageDirs {
		account, device := filepath.Join(home, dir), filepath.Join(a.DeviceHome, dir)
		if err := os.MkdirAll(account, 0o700); err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, shareAgyEntries(account, device, agyKnownShared, func(name string, dir bool) bool {
			return !agyPrivate[name] && !agyCredentialName(name) && (dir || agySharedFiles[name])
		}))
		carryOnboarding(device, account)
	}
	shareSignInWithACP(home)
	return errors.Join(errs...)
}

// shareAgyEntries links into account each known directory and each entry of
// device that share allows.
func shareAgyEntries(account, device string, known []string, share func(name string, dir bool) bool) error {
	names := map[string]bool{} // name -> whether it is a file
	for _, name := range known {
		names[name] = false
	}
	entries, err := os.ReadDir(device)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if _, ok := names[entry.Name()]; !ok && share(entry.Name(), entry.IsDir()) {
			names[entry.Name()] = !entry.IsDir()
		}
	}
	pruneDangling(account, device, names)
	errs := make([]error, 0, len(names))
	for name, file := range names {
		errs = append(errs, linkShared(account, device, name, file, false))
	}
	return errors.Join(errs...)
}

// carryOnboarding marks agy's first-run screens done in account when they are
// done on the computer. The choices made there live with the Google account;
// the file only records that they were shown. Without it, agy opens a moved
// conversation on its theme picker.
func carryOnboarding(device, account string) {
	done := func(path string) ([]byte, bool) {
		var state struct {
			OnboardingComplete bool `json:"onboardingComplete"`
		}
		data, err := os.ReadFile(path)
		return data, err == nil && json.Unmarshal(data, &state) == nil && state.OnboardingComplete
	}
	data, ok := done(filepath.Join(device, "cache", "onboarding.json"))
	dst := filepath.Join(account, "cache", "onboarding.json")
	if _, already := done(dst); !ok || already {
		return
	}
	if os.MkdirAll(filepath.Dir(dst), 0o700) == nil {
		_ = writeIfChanged(dst, data)
	}
}

// shareSignInWithACP links the ACP server's sign-in file to the terminal
// CLI's within the account, so a Chat session on the account finds the
// sign-in made in its terminal whichever storage folder agy reads it from.
func shareSignInWithACP(home string) {
	cli := filepath.Join(home, agy.CLIStorageDir, agyTokenFile)
	acp := filepath.Join(home, agy.ACPStorageDir, agyTokenFile)
	if _, err := os.Stat(cli); err != nil {
		return
	}
	if _, err := os.Lstat(acp); errors.Is(err, fs.ErrNotExist) {
		_ = os.Symlink(cli, acp)
	}
}
