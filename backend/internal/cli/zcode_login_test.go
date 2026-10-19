package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const zcodeTestKey = "zai-test-key-0123456789"

func TestZcodeLoginBrowserOptionRunsNativeLogin(t *testing.T) {
	var stdout bytes.Buffer
	var gotName string
	var gotArgs []string
	deps := Deps{
		In:       strings.NewReader("1\n"),
		Out:      &stdout,
		Err:      &stdout,
		LookPath: func(string) (string, error) { return "/usr/local/bin/zcode", nil },
		ReadSecret: func(io.Reader) ([]byte, error) {
			t.Fatal("browser login must not prompt for a secret")
			return nil, nil
		},
		RunInteractiveCommand: func(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return nil
		},
	}

	cmd := newZcodeLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed/bin/zcode", "--provider-config", filepath.Join(t.TempDir(), "provider_config.json")})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotName != "/managed/bin/zcode" {
		t.Fatalf("executable = %q, want adapter-resolved ZCode", gotName)
	}
	if want := []string{"login", "zai"}; !slices.Equal(gotArgs, want) {
		t.Fatalf("args = %q, want %q", gotArgs, want)
	}
	if !strings.Contains(stdout.String(), "ZCode sign-in complete.") {
		t.Fatalf("output missing completion line:\n%s", stdout.String())
	}
}

func TestZcodeLoginAPIKeyOptionWritesProviderConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "harnesses", "zcode", "provider_config.json")
	deps := Deps{
		In:       strings.NewReader("2\n"),
		Out:      &stdout,
		Err:      &stderr,
		LookPath: func(string) (string, error) { return "/usr/local/bin/zcode", nil },
		ReadSecret: func(io.Reader) ([]byte, error) {
			return []byte("  " + zcodeTestKey + "\n"), nil
		},
		RunInteractiveCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
			t.Fatal("API key option must not spawn the ZCode CLI")
			return nil
		},
	}

	cmd := newZcodeLoginCommand(&commandContext{deps: deps.withDefaults()})
	cmd.SetArgs([]string{"--executable", "/managed/bin/zcode", "--provider-config", path})
	cmd.SetIn(deps.In)
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.Contains(stdout.String(), zcodeTestKey) || strings.Contains(stderr.String(), zcodeTestKey) {
		t.Fatalf("API key leaked into terminal output:\n%s\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "API key saved to "+path) {
		t.Fatalf("output missing saved-to line:\n%s", stdout.String())
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat provider config: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("provider config mode = %o, want 0600", got)
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat provider config dir: %v", err)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("provider config dir mode = %o, want 0700", got)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provider config: %v", err)
	}
	want := `{
  "schemaVersion": 1,
  "config": {
    "providerConfigRules": {
      "providerRules": [
        {
          "providerId": "zai-api-ao",
          "templateId": "zai-api",
          "providerName": "Z.ai Coding Plan (AO)",
          "config": {
            "group": "standard-personal",
            "access": {
              "type": "zhipu-coding-plan-api-key",
              "apiKey": "` + zcodeTestKey + `"
            },
            "personalModelIds": [
              "GLM-5.3",
              "GLM-5.3-Flash"
            ]
          }
        }
      ]
    },
    "modelConfigRules": {
      "providerModelRules": [],
      "manualProviderModelRules": []
    },
    "defaultModelSelection": {
      "providerId": "zai-api-ao",
      "modelId": "GLM-5.3"
    }
  }
}
`
	if string(data) != want {
		t.Fatalf("provider config = %s\nwant %s", data, want)
	}
}

func TestZcodeLoginMenuListsBothMethods(t *testing.T) {
	var stdout bytes.Buffer
	if err := writeZcodeLoginMenu(&stdout, codexLoginStyle{}); err != nil {
		t.Fatalf("writeZcodeLoginMenu: %v", err)
	}
	for _, fragment := range []string{"Sign in to ZCode", "Z.AI account in browser", "Z.ai Coding Plan API key", "Selection [1-2]"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("menu missing %q:\n%s", fragment, stdout.String())
		}
	}
}

func TestZcodeLoginRejectsBadInput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		deps     Deps
		want     string
		wantExit int
	}{
		{
			name:     "missing binary",
			deps:     Deps{LookPath: func(string) (string, error) { return "", errors.New("missing") }},
			want:     "zcode CLI is not installed",
			wantExit: 1,
		},
		{
			name: "native failure",
			deps: Deps{
				In:       strings.NewReader("1\n"),
				LookPath: func(string) (string, error) { return "/zcode", nil },
				RunInteractiveCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
					return errors.New("exit status 1")
				},
			},
			want:     "zcode login failed",
			wantExit: 1,
		},
		{
			name:     "unknown selection",
			deps:     Deps{In: strings.NewReader("9\n"), LookPath: func(string) (string, error) { return "/zcode", nil }},
			want:     "login method must be 1 or 2",
			wantExit: 2,
		},
		{
			name:     "API key without provider config path",
			deps:     Deps{In: strings.NewReader("2\n"), LookPath: func(string) (string, error) { return "/zcode", nil }},
			want:     "--provider-config is required",
			wantExit: 2,
		},
		{
			name: "empty API key",
			args: []string{"--provider-config", filepath.Join(t.TempDir(), "provider_config.json")},
			deps: Deps{
				In:         strings.NewReader("2\n"),
				LookPath:   func(string) (string, error) { return "/zcode", nil },
				ReadSecret: func(io.Reader) ([]byte, error) { return []byte("  \n"), nil },
			},
			want:     "API key is required",
			wantExit: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newZcodeLoginCommand(&commandContext{deps: tt.deps.withDefaults()})
			cmd.SetArgs(tt.args)
			if tt.deps.In == nil {
				cmd.SetIn(strings.NewReader("1\n"))
			} else {
				cmd.SetIn(tt.deps.In)
			}
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute error = %v, want %q", err, tt.want)
			}
			if got := ExitCode(err); got != tt.wantExit {
				t.Fatalf("exit code = %d, want %d", got, tt.wantExit)
			}
		})
	}
}
