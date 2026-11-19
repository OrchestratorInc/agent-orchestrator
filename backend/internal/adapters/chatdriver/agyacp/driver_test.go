package agyacp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSessionModeMapsApprovalTiersOntoAntigravityModes(t *testing.T) {
	for mode, want := range map[ports.PermissionMode]string{
		ports.PermissionModeDefault:           "default",
		ports.PermissionModeAcceptEdits:       "auto_edit",
		ports.PermissionModeAuto:              "yolo", // no classifier mode: auto runs without asking
		ports.PermissionModeBypassPermissions: "yolo",
	} {
		if got := sessionMode(mode); got != want {
			t.Errorf("sessionMode(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestPermissionPolicyApprovesOnceOnlyInAOsAutoModes(t *testing.T) {
	cfg := buildConfig(nil, testResolver(t, nil), nil)
	if cfg.ValidateTurnSettings != nil {
		t.Fatal("approval modes switch live; no restart validator is needed")
	}
	request := acpsdk.RequestPermissionRequest{Options: []acpsdk.PermissionOption{
		{OptionId: "always", Kind: acpsdk.PermissionOptionKindAllowAlways},
		{OptionId: "once", Kind: acpsdk.PermissionOptionKindAllowOnce},
		{OptionId: "no", Kind: acpsdk.PermissionOptionKindRejectOnce},
	}}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if got, ok := cfg.PermissionPolicy(mode, request); !ok || got != "once" {
			t.Errorf("policy(%q) = %q, %v; want allow_once, never allow_always", mode, got, ok)
		}
	}
	for _, mode := range []ports.PermissionMode{ports.PermissionModeDefault, ports.PermissionModeAcceptEdits} {
		if got, ok := cfg.PermissionPolicy(mode, request); ok {
			t.Errorf("policy(%q) answered %q; the user decides", mode, got)
		}
	}
	noOnce := acpsdk.RequestPermissionRequest{Options: []acpsdk.PermissionOption{
		{OptionId: "always", Kind: acpsdk.PermissionOptionKindAllowAlways},
	}}
	if got, ok := cfg.PermissionPolicy(ports.PermissionModeAuto, noOnce); ok {
		t.Errorf("policy chose %q without an allow_once option", got)
	}
}

func TestSessionModeForAgentMapsOntoAgyACPModes(t *testing.T) {
	legacy := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "antigravity-acp", Version: "1.3.0"}}
	if sessionModeForAgent(legacy) != nil {
		t.Fatal("the separate server must keep its own mode ids")
	}
	cli := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: "Antigravity ACP Server", Version: "1.5.6"}}
	modeFor := sessionModeForAgent(cli)
	if modeFor == nil {
		t.Fatal("no mode mapping for agy acp")
	}
	for mode, want := range map[ports.PermissionMode]string{
		ports.PermissionModeDefault:           "default",
		ports.PermissionModeAcceptEdits:       "accept-edits",
		ports.PermissionModeAuto:              "accept-edits", // commands are approved by permissionPolicy
		ports.PermissionModeBypassPermissions: "accept-edits",
	} {
		if got := modeFor(mode); got != want {
			t.Errorf("agy acp mode(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestSteeringIsOfferedOnlyForAgyACP(t *testing.T) {
	cfg := buildConfig(nil, testResolver(t, nil), nil)
	if cfg.SteerPrompt == nil || cfg.SteerPrompt.Supported == nil {
		t.Fatal("no prompt steering binding")
	}
	for _, tc := range []struct {
		name, version string
		want          bool
	}{
		{"Antigravity ACP Server", "1.5.4", true},
		{"Antigravity ACP Server", "1.5.6", true},
		{"Antigravity ACP Server", "1.5.3", false},
		{"Antigravity ACP Server", "1.4.9", false},
		{"antigravity-acp", "1.4.5", false},
	} {
		init := acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: tc.name, Version: tc.version}}
		if got := cfg.SteerPrompt.Supported(init); got != tc.want {
			t.Errorf("steering for %s %s = %v, want %v", tc.name, tc.version, got, tc.want)
		}
	}
}

func TestAgyACPSkipsTheSeparateServersSignInBootstrap(t *testing.T) {
	called := false
	bootstrap := authBootstrap{geminiHome: func(map[string]string) (string, error) {
		called = true
		return "", errors.New("unexpected")
	}}
	cli := acpsdk.InitializeResponse{
		AgentInfo:   &acpsdk.Implementation{Name: "Antigravity ACP Server", Version: "1.5.6"},
		AuthMethods: []acpsdk.AuthMethod{{Agent: &acpsdk.AuthMethodAgent{Id: "oauth-personal", Name: "Google"}}},
	}
	if err := afterInitialize(bootstrap)(context.Background(), nil, cli, acpdriver.LaunchConfig{}); err != nil || called {
		t.Fatalf("agy acp ran the sign-in bootstrap: err=%v called=%v", err, called)
	}
}

func TestLaunchArgsMirrorTheRegistryEntry(t *testing.T) {
	if got := launchArgs("linux"); !reflect.DeepEqual(got, []string{"--uid="}) {
		t.Errorf("linux args = %v", got)
	}
	for _, goos := range []string{"darwin", "windows"} {
		if got := launchArgs(goos); got != nil {
			t.Errorf("%s args = %v, want none", goos, got)
		}
	}
}

func TestSessionOptionsSelectOnlyAChosenModel(t *testing.T) {
	if got := sessionOptions(ports.ChatTurnSettings{}); got != nil {
		t.Errorf("empty model options = %v", got)
	}
	want := []acpdriver.SessionOption{{ID: "model", Value: "gemini-3.8-flash-high"}}
	if got := sessionOptions(ports.ChatTurnSettings{Model: " gemini-3.8-flash-high "}); !reflect.DeepEqual(got, want) {
		t.Errorf("options = %v, want %v", got, want)
	}
}

func TestSessionPreambleCarriesStandingInstructionsAsText(t *testing.T) {
	if got := sessionPreamble(acpdriver.LaunchConfig{SystemPrompt: "  "}); got != nil {
		t.Fatalf("blank prompt preamble = %v", got)
	}
	blocks := sessionPreamble(acpdriver.LaunchConfig{SystemPrompt: "Follow AO worker rules."})
	if len(blocks) != 1 || blocks[0].Text == nil {
		t.Fatalf("preamble = %#v, want one text block", blocks)
	}
	if !strings.Contains(blocks[0].Text.Text, "<ao_standing_instructions>\nFollow AO worker rules.\n</ao_standing_instructions>") {
		t.Fatalf("preamble text = %q", blocks[0].Text.Text)
	}
}

func TestValidateInitializeAdmitsOnlyTestedAntigravityServers(t *testing.T) {
	info := func(name, version string) acpsdk.InitializeResponse {
		return acpsdk.InitializeResponse{AgentInfo: &acpsdk.Implementation{Name: name, Version: version}}
	}
	for _, ok := range []acpsdk.InitializeResponse{
		info("antigravity-acp", "1.3.0"),
		info("antigravity-acp", "1.10.2"),
		info("antigravity-acp", "2.0.0-beta"),
		info("Antigravity ACP Server", "1.5.4"),
		info("Antigravity ACP Server", "1.5.6"),
	} {
		if err := validateInitialize(ok); err != nil {
			t.Errorf("validateInitialize(%s %s) = %v", ok.AgentInfo.Name, ok.AgentInfo.Version, err)
		}
	}
	for _, bad := range []acpsdk.InitializeResponse{
		{},
		info("cline", "3.0.0"),
		info("antigravity-acp", "1.2.1"),
		info("antigravity-acp", "dev"),
		info("Antigravity ACP Server", "1.4.0"),
		info("Antigravity ACP Server", "1.5.3"),
		info("Antigravity ACP Server", "dev"),
	} {
		if err := validateInitialize(bad); err == nil {
			t.Errorf("validateInitialize(%+v) admitted an untested server", bad.AgentInfo)
		}
	}
}

func TestResolverPrefersAnExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	server := writeExecutable(t, filepath.Join(dir, "custom-server"))
	r := testResolver(t, map[string]string{serverPathEnvVar: server})
	if got, err := r.serverPath(context.Background()); err != nil || got != server {
		t.Fatalf("serverPath = %q, %v", got, err)
	}

	r = testResolver(t, map[string]string{serverPathEnvVar: filepath.Join(dir, "missing")})
	if _, err := r.serverPath(context.Background()); err == nil || !strings.Contains(err.Error(), serverPathEnvVar) {
		t.Fatalf("missing override error = %v, want it named", err)
	}
}

func TestResolverFindsTheServerBesideTheAgyCLI(t *testing.T) {
	bin := t.TempDir()
	cli := writeExecutable(t, filepath.Join(bin, "agy"))
	server := writeExecutable(t, filepath.Join(bin, "agy_acp_server"))
	r := testResolver(t, nil)
	r.agyCLI = func(context.Context) (string, error) { return cli, nil }
	r.resolveSpec = func(context.Context, binaryutil.BinarySpec) (string, error) {
		t.Fatal("PATH lookup ran although the server sits beside the CLI")
		return "", nil
	}
	if got, err := r.serverPath(context.Background()); err != nil || got != server {
		t.Fatalf("serverPath = %q, %v, want %q", got, err, server)
	}
}

func TestResolverFallsBackToPATHAndExplainsAMissingServer(t *testing.T) {
	r := testResolver(t, nil)
	r.agyCLI = func(context.Context) (string, error) { return "", errors.New("agy not installed") }
	r.resolveSpec = func(_ context.Context, spec binaryutil.BinarySpec) (string, error) {
		if spec.Label != "agy_acp_server" {
			t.Fatalf("resolved spec %q", spec.Label)
		}
		return "/usr/local/bin/agy_acp_server", nil
	}
	if got, err := r.serverPath(context.Background()); err != nil || got != "/usr/local/bin/agy_acp_server" {
		t.Fatalf("serverPath = %q, %v", got, err)
	}

	r.resolveSpec = func(context.Context, binaryutil.BinarySpec) (string, error) {
		return "", ports.ErrAgentBinaryNotFound
	}
	if _, err := r.serverPath(context.Background()); err == nil || !strings.Contains(err.Error(), "antigravity-acp") {
		t.Fatalf("missing server error = %v, want the registry entry named", err)
	}
}

func TestHarnessPathFollowsASymlinkedServer(t *testing.T) {
	install := t.TempDir()
	server := writeExecutable(t, filepath.Join(install, "agy_acp_server.par"))
	harness := writeExecutable(t, filepath.Join(install, "localharness_external"))
	bin := t.TempDir()
	link := filepath.Join(bin, "agy_acp_server")
	if err := os.Symlink(server, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r := testResolver(t, nil)
	if got := r.harnessPath(link); !samePath(t, got, harness) {
		t.Fatalf("harnessPath(symlink) = %q, want %q", got, harness)
	}
	if got := r.harnessPath(filepath.Join(bin, "missing")); got != "" {
		t.Fatalf("harnessPath(missing) = %q, want empty", got)
	}
}

func TestLaunchPointsTheServerAtItsHarness(t *testing.T) {
	install := t.TempDir()
	server := writeExecutable(t, filepath.Join(install, "agy_acp_server.par"))
	harness := writeExecutable(t, filepath.Join(install, "localharness_external"))
	cfg := buildConfig(nil, testResolver(t, map[string]string{serverPathEnvVar: server}), nil)
	launch, err := cfg.Launch(context.Background(), acpdriver.LaunchConfig{Env: map[string]string{"PATH": "/usr/bin"}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if launch.Command != server || !samePath(t, launch.Env[harnessPathEnvVar], harness) || launch.Env["PATH"] != "/usr/bin" {
		t.Fatalf("launch = %+v", launch)
	}
	if launch.Env[disableWorkspaceTrustEnvVar] != "1" {
		t.Fatalf("expected %s=1 in launch env, got %q", disableWorkspaceTrustEnvVar, launch.Env[disableWorkspaceTrustEnvVar])
	}

	launch, err = cfg.Launch(context.Background(), acpdriver.LaunchConfig{Env: map[string]string{
		harnessPathEnvVar:           "/custom",
		disableWorkspaceTrustEnvVar: "0",
	}})
	if err != nil || launch.Env[harnessPathEnvVar] != "/custom" || launch.Env[disableWorkspaceTrustEnvVar] != "0" {
		t.Fatalf("an explicit %s or %s was overridden: %+v, %v", harnessPathEnvVar, disableWorkspaceTrustEnvVar, launch.Env, err)
	}
}

func TestResolverLaunchesAgyACPFromANewEnoughCLI(t *testing.T) {
	bin := t.TempDir()
	cli := writeExecutable(t, filepath.Join(bin, "agy"))
	server := writeExecutable(t, filepath.Join(bin, "agy_acp_server"))
	for _, tc := range []struct {
		version string
		err     error
		wantCLI bool
	}{
		{version: "1.5.4", wantCLI: true},
		{version: "1.5.6\n", wantCLI: true},
		{version: "2.0.0", wantCLI: true},
		{version: "1.5.3"},
		{version: "1.4.9"},
		{version: "unknown"},
		{err: errors.New("agy --version failed")},
	} {
		r := testResolver(t, nil)
		r.agyCLI = func(context.Context) (string, error) { return cli, nil }
		r.cliVersion = func(context.Context, string) (string, error) { return tc.version, tc.err }
		got, err := r.target(context.Background())
		if err != nil {
			t.Fatalf("target(%q, %v): %v", tc.version, tc.err, err)
		}
		want := launchTarget{path: server}
		if tc.wantCLI {
			want = launchTarget{path: cli, cli: true}
		}
		if got != want {
			t.Errorf("target(%q, %v) = %+v, want %+v", tc.version, tc.err, got, want)
		}
	}

	// An explicit server path keeps the separate server even on a new CLI.
	r := testResolver(t, map[string]string{serverPathEnvVar: server})
	r.agyCLI = func(context.Context) (string, error) { return cli, nil }
	r.cliVersion = func(context.Context, string) (string, error) {
		t.Fatal("version probed despite an explicit server path")
		return "", nil
	}
	if got, err := r.target(context.Background()); err != nil || got != (launchTarget{path: server}) {
		t.Fatalf("target with override = %+v, %v", got, err)
	}
}

func TestLaunchStartsAgyACPWithTheChatDataFolder(t *testing.T) {
	cli := writeExecutable(t, filepath.Join(t.TempDir(), "agy"))
	r := testResolver(t, nil)
	r.agyCLI = func(context.Context) (string, error) { return cli, nil }
	r.cliVersion = func(context.Context, string) (string, error) { return "1.5.6", nil }
	cfg := buildConfig(nil, r, nil)

	launch, err := cfg.Launch(context.Background(), acpdriver.LaunchConfig{Env: map[string]string{"PATH": "/usr/bin"}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if launch.Command != cli || !reflect.DeepEqual(launch.Args, []string{"acp", "--app-data-dir", "antigravity-acp"}) {
		t.Fatalf("launch = %q %v", launch.Command, launch.Args)
	}
	if _, set := launch.Env[harnessPathEnvVar]; set || launch.Env[disableWorkspaceTrustEnvVar] != "1" || launch.Env["PATH"] != "/usr/bin" {
		t.Fatalf("launch env = %v", launch.Env)
	}

	launch, err = cfg.Launch(context.Background(), acpdriver.LaunchConfig{Env: map[string]string{"GEMINI_HOME": "/srv/gemini"}})
	want := []string{"acp", "--app-data-dir", "antigravity-acp", "--gemini-dir", "/srv/gemini"}
	if err != nil || !reflect.DeepEqual(launch.Args, want) {
		t.Fatalf("launch with GEMINI_HOME = %v, %v; want %v", launch.Args, err, want)
	}

	// The daemon's own GEMINI_HOME reaches agy as the flag too: `agy acp`
	// ignores the variable, so without it Chat would write ~/.gemini.
	inherited := testResolver(t, map[string]string{"GEMINI_HOME": "/srv/daemon-gemini"})
	inherited.agyCLI, inherited.cliVersion = r.agyCLI, r.cliVersion
	launch, err = buildConfig(nil, inherited, nil).Launch(context.Background(), acpdriver.LaunchConfig{})
	want = []string{"acp", "--app-data-dir", "antigravity-acp", "--gemini-dir", "/srv/daemon-gemini"}
	if err != nil || !reflect.DeepEqual(launch.Args, want) {
		t.Fatalf("launch with an inherited GEMINI_HOME = %v, %v; want %v", launch.Args, err, want)
	}
}

func TestProbeReportsAMissingServerAsUnavailable(t *testing.T) {
	r := testResolver(t, nil)
	r.resolveSpec = func(context.Context, binaryutil.BinarySpec) (string, error) { return "", ports.ErrAgentBinaryNotFound }
	cfg := buildConfig(nil, r, nil)
	if err := cfg.Probe(context.Background()); !errors.Is(err, ports.ErrChatDriverUnavailable) {
		t.Fatalf("Probe = %v, want ErrChatDriverUnavailable", err)
	}
}

func TestProbeRespectsAnUnauthorizedPlugin(t *testing.T) {
	server := writeExecutable(t, filepath.Join(t.TempDir(), "agy_acp_server"))
	r := testResolver(t, map[string]string{serverPathEnvVar: server})
	cfg := buildConfig(fakePlugin{status: ports.AgentAuthStatusUnauthorized}, r, nil)
	if err := cfg.Probe(context.Background()); !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("Probe = %v, want ErrChatAuthRequired", err)
	}
	cfg = buildConfig(fakePlugin{status: ports.AgentAuthStatusAuthorized}, r, nil)
	if err := cfg.Probe(context.Background()); err != nil {
		t.Fatalf("Probe = %v", err)
	}
}

func TestAuthBootstrapAuthenticatesOnlyAnAlreadySignedInUser(t *testing.T) {
	advertised := acpsdk.InitializeResponse{AuthMethods: []acpsdk.AuthMethod{
		{Agent: &acpsdk.AuthMethodAgent{Id: "oauth-personal", Name: "Log in with Google"}},
	}}
	tests := []struct {
		name     string
		init     acpsdk.InitializeResponse
		accounts string
		settings string
		wantCall bool
	}{
		{name: "signed in, not configured", init: advertised, accounts: `{"active":"me@example.com","old":[]}`, wantCall: true},
		{name: "not signed in: never risk a browser flow", init: advertised, accounts: `{"active":"","old":[]}`},
		{name: "no accounts file", init: advertised},
		{name: "already configured", init: advertised, accounts: `{"active":"me@example.com"}`, settings: `{"auth":{"type":"oauth-personal"}}`},
		{name: "method not advertised", init: acpsdk.InitializeResponse{}, accounts: `{"active":"me@example.com"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.accounts != "" {
				writeFile(t, filepath.Join(home, "google_accounts.json"), tt.accounts)
			}
			if tt.settings != "" {
				writeFile(t, filepath.Join(home, "antigravity-acp", "settings.json"), tt.settings)
			}
			var calls []string
			b := newAuthBootstrap(func(map[string]string) (string, error) { return home, nil })
			err := b.bootstrap(context.Background(), tt.init, nil, func(_ context.Context, method string) error {
				calls = append(calls, method)
				return nil
			})
			if err != nil {
				t.Fatalf("bootstrap = %v", err)
			}
			if tt.wantCall != (len(calls) == 1 && calls[0] == "oauth-personal") || (!tt.wantCall && len(calls) != 0) {
				t.Fatalf("authenticate calls = %v, want call=%v", calls, tt.wantCall)
			}
		})
	}
}

func TestAuthBootstrapChecksTheSessionGeminiHome(t *testing.T) {
	daemonHome := t.TempDir()
	sessionHome := t.TempDir()
	writeFile(t, filepath.Join(sessionHome, "google_accounts.json"), `{"active":"session@example.com"}`)
	t.Setenv("GEMINI_HOME", daemonHome)
	init := acpsdk.InitializeResponse{AuthMethods: []acpsdk.AuthMethod{
		{Agent: &acpsdk.AuthMethodAgent{Id: "oauth-personal", Name: "Log in with Google"}},
	}}
	calls := 0
	b := newAuthBootstrap(agy.GeminiHome)
	if err := b.bootstrap(context.Background(), init, map[string]string{"GEMINI_HOME": sessionHome}, func(_ context.Context, _ string) error {
		calls++
		return nil
	}); err != nil {
		t.Fatalf("bootstrap = %v", err)
	}
	if calls != 1 {
		t.Fatalf("authenticate calls = %d, want session sign-in to be detected", calls)
	}
}

func TestAuthBootstrapBoundsAndReportsAFailedAuthenticate(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "google_accounts.json"), `{"active":"me@example.com"}`)
	init := acpsdk.InitializeResponse{AuthMethods: []acpsdk.AuthMethod{
		{Agent: &acpsdk.AuthMethodAgent{Id: "oauth-personal", Name: "Log in with Google"}},
	}}
	b := newAuthBootstrap(func(map[string]string) (string, error) { return home, nil })
	b.timeout = 50 * time.Millisecond
	err := b.bootstrap(context.Background(), init, nil, func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bootstrap = %v, want a bounded deadline error", err)
	}
}

func TestAuthMethodIDReadsTheAdvertisedID(t *testing.T) {
	var method acpsdk.AuthMethod
	if err := json.Unmarshal([]byte(`{"id":"oauth-personal","name":"Log in with Google"}`), &method); err != nil {
		t.Fatalf("decode auth method: %v", err)
	}
	if got := authMethodID(method); got != "oauth-personal" {
		t.Fatalf("authMethodID = %q", got)
	}
}

type fakePlugin struct{ status ports.AgentAuthStatus }

func (f fakePlugin) ResolveBinary(context.Context) (string, error) { return "", errors.New("not used") }
func (f fakePlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return f.status, nil
}

func testResolver(t *testing.T, env map[string]string) resolver {
	t.Helper()
	return resolver{
		getenv: func(key string) string { return env[key] },
		isFile: isExecutableFile,
		resolveSpec: func(context.Context, binaryutil.BinarySpec) (string, error) {
			return "", ports.ErrAgentBinaryNotFound
		},
	}
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	writeFile(t, path, "#!/bin/sh\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
