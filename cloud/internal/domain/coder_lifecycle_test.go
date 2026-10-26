package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOrgCoderConfigLifecycleDefaults(t *testing.T) {
	t.Parallel()
	encoded, err := EncodeOrgCoderConfig(OrgCoderConfig{
		BaseURL: "https://coder.example.com", WorkerUser: CoderWorkerUserWorkspace,
		WorkingDirectory: "  ~/work/{repo} ",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DecodeOrgCoderConfig(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkingDirectory != "~/work/{repo}" || !cfg.ProtectsSecretFiles() || cfg.ProtectSecretFiles == nil {
		t.Fatalf("normalized = %+v", cfg)
	}
	if err := cfg.ValidateDevEnvironment(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	// A connection saved before the fields existed protects secret files and
	// has no working directory.
	legacy, err := DecodeOrgCoderConfig(json.RawMessage(`{"baseUrl":"https://coder.example.com","owner":"ao","templateId":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if !legacy.ProtectsSecretFiles() || legacy.WorkingDirectory != "" {
		t.Fatalf("legacy = %+v", legacy)
	}
	off := false
	optedOut, _ := EncodeOrgCoderConfig(OrgCoderConfig{BaseURL: "https://coder.example.com", ProtectSecretFiles: &off})
	if decoded, _ := DecodeOrgCoderConfig(optedOut); decoded.ProtectsSecretFiles() {
		t.Fatal("an explicit opt-out was lost")
	}
}

func TestOrgCoderConfigLifecycleNeedsWorkspaceUser(t *testing.T) {
	t.Parallel()
	cfg := OrgCoderConfig{WorkerUser: CoderWorkerUserAOWorker, WorkingDirectory: "~/work/{repo}"}
	if err := cfg.ValidateDevEnvironment(); !errors.Is(err, ErrCoderSettingRequiresWorkspaceUser) {
		t.Errorf("working directory with ao-worker: err = %v", err)
	}
}

func TestOrgCoderConfigLifecycleValidation(t *testing.T) {
	t.Parallel()
	bad := OrgCoderConfig{WorkerUser: CoderWorkerUserWorkspace, WorkingDirectory: "work/{repo}"}
	if err := bad.ValidateDevEnvironment(); err == nil {
		t.Error("bad working directory accepted")
	}
	good := OrgCoderConfig{WorkerUser: CoderWorkerUserWorkspace, WorkingDirectory: "~/work/{repo}"}
	if err := good.ValidateDevEnvironment(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateCoderWorkingDirectory(t *testing.T) {
	t.Parallel()
	// A hidden folder is refused only for the ao-worker checkout
	// (ValidateCoderCheckoutDirectory): a workspace-user session may work in
	// the template's ~/.config/app.
	for _, value := range []string{"", "~/work/{repo}", "$HOME/work/{repo}", "/srv/app", "/home/ubuntu/work/alice", "~/{repo}-main",
		"~/.config/app", "/srv/.cache/{repo}", "~/work/.github"} {
		if err := ValidateCoderWorkingDirectory(value); err != nil {
			t.Errorf("%q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		"work/{repo}", "~", "~/", "/", "$HOME", "~/../etc", "/srv/./app", "~/work/{name}", "~/work/$USER",
		"~/work/`id`", "~/.ao/{repo}", "~/work/.ao", "/srv/app\n", strings.Repeat("a", MaxCoderWorkingDirectoryLength+1), "${HOME}/work",
	} {
		if err := ValidateCoderWorkingDirectory(value); err == nil {
			t.Errorf("%q accepted", value)
		}
	}
}

func TestCoderWorkspaceNamePrefixFor(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"alice":                         "alice",
		"Alice Backend":                 "alice-backend",
		"  11x / Alice__API  ":          "x-alice-api",
		"2024":                          "ao",
		"":                              "ao",
		"---":                           "ao",
		"a-very-long-project-name-here": "a-very-long-project",
		"Ünïcode Project":               "n-code-project",
	} {
		got := CoderWorkspaceNamePrefixFor(name)
		if got != want {
			t.Errorf("CoderWorkspaceNamePrefixFor(%q) = %q, want %q", name, got, want)
		}
		if err := ValidateCoderWorkspaceNamePrefix(got); err != nil {
			t.Errorf("CoderWorkspaceNamePrefixFor(%q) = %q is invalid: %v", name, got, err)
		}
	}
}
