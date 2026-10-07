package systeminstall

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type managerCommandResponse struct {
	argv   []string
	output string
	err    error
}

type managerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f managerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func managerCommandRunner(t *testing.T, responses ...managerCommandResponse) commandRunnerFunc {
	t.Helper()
	index := 0
	t.Cleanup(func() {
		if index != len(responses) {
			t.Errorf("manager commands consumed = %d, want %d", index, len(responses))
		}
	})
	return func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if index >= len(responses) {
			t.Fatalf("unexpected manager command %v", argv)
		}
		response := responses[index]
		index++
		if !reflect.DeepEqual(argv, response.argv) {
			t.Fatalf("command %d = %v, want %v", index, argv, response.argv)
		}
		_, _ = io.WriteString(stdout, response.output)
		return response.err
	}
}

func TestManagedVersionCheckerUsesNativeCommandsWhenRegistryIsUnreachable(t *testing.T) {
	exitOne := errors.New("exit status 1")
	tests := []struct {
		name     string
		plan     Plan
		response managerCommandResponse
	}{
		{
			name: "npm accepts outdated exit status",
			plan: Plan{Method: "npm", Package: "@openai/codex", PackagePrefix: "/prefix"},
			response: managerCommandResponse{
				argv:   []string{"npm", "outdated", "-g", "--json", "--prefix", "/prefix", "@openai/codex"},
				output: `{"@openai/codex":{"current":"1.2.0","wanted":"1.2.0","latest":"1.3.0","location":"lib/node_modules/@openai/codex"}}`,
				err:    exitOne,
			},
		},
		{
			name: "homebrew cask",
			plan: Plan{Method: "homebrew", Package: "codex", PackageCask: true},
			response: managerCommandResponse{
				argv:   []string{"brew", "outdated", "--json=v2", "--cask", "codex"},
				output: `{"formulae":[],"casks":[{"name":"codex","installed_versions":["1.2.0"],"current_version":"1.3.0"}]}`,
			},
		},
		{
			name: "winget exact id",
			plan: Plan{Method: "winget", Package: "GitHub.Copilot"},
			response: managerCommandResponse{
				argv:   []string{"winget", "upgrade", "--disable-interactivity"},
				output: "GitHub Copilot  GitHub.Copilot  1.2.0  1.3.0  winget\n",
			},
		},
		{
			name: "bun registry view",
			plan: Plan{Method: "bun", Package: "@oh-my-pi/pi-coding-agent"},
			response: managerCommandResponse{
				argv:   []string{"bun", "pm", "view", "@oh-my-pi/pi-coding-agent", "version"},
				output: "1.3.0\n",
			},
		},
		{
			name: "pnpm global",
			plan: Plan{Method: "pnpm", Package: "@openai/codex"},
			response: managerCommandResponse{
				argv:   []string{"pnpm", "outdated", "--global", "--format", "json", "@openai/codex"},
				output: `{"@openai/codex":{"current":"1.2.0","wanted":"1.2.0","latest":"1.3.0"}}`,
				err:    exitOne,
			},
		},
		{
			name: "yarn classic global",
			plan: Plan{Method: "yarn", Package: "@openai/codex"},
			response: managerCommandResponse{
				argv:   []string{"yarn", "global", "outdated", "--json", "@openai/codex"},
				output: `{"type":"table","data":{"head":["Package","Current","Wanted","Latest","Package Type","URL"],"body":[["@openai/codex","1.2.0","1.2.0","1.3.0","dependencies",""]]}}`,
				err:    exitOne,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, _ := parseUpdateVersion("1.2.0")
			checker := newManagedVersionChecker(managerCommandRunner(t, tt.response), offlineManagerClient())
			got, err := checker(context.Background(), tt.plan, current)
			if err != nil {
				t.Fatal(err)
			}
			if got.Latest != "1.3.0" || got.Channel != "latest" {
				t.Fatalf("result = %+v", got)
			}
		})
	}
}

func TestManagedVersionCheckerFallsBackWhenOutdatedResultIsEmpty(t *testing.T) {
	tests := []struct {
		name      string
		plan      Plan
		responses []managerCommandResponse
	}{
		{
			name: "npm view",
			plan: Plan{Method: "npm", Package: "@openai/codex", PackagePrefix: "/prefix"},
			responses: []managerCommandResponse{
				{argv: []string{"npm", "outdated", "-g", "--json", "--prefix", "/prefix", "@openai/codex"}, output: `{}`},
				{argv: []string{"npm", "view", "@openai/codex@latest", "version", "--json"}, output: `"1.3.0"`},
			},
		},
		{
			name: "homebrew info",
			plan: Plan{Method: "homebrew", Package: "codex", PackageCask: true},
			responses: []managerCommandResponse{
				{argv: []string{"brew", "outdated", "--json=v2", "--cask", "codex"}, output: `{"formulae":[],"casks":[]}`},
				{argv: []string{"brew", "info", "--json=v2", "--cask", "codex"}, output: `{"formulae":[],"casks":[{"token":"codex","name":["Codex"],"version":"1.3.0","installed":"1.3.0","outdated":false}]}`},
			},
		},
		{
			name: "pnpm view",
			plan: Plan{Method: "pnpm", Package: "@openai/codex"},
			responses: []managerCommandResponse{
				{argv: []string{"pnpm", "outdated", "--global", "--format", "json", "@openai/codex"}, output: `{}`},
				{argv: []string{"pnpm", "view", "@openai/codex@latest", "version", "--json"}, output: `"1.3.0"`},
			},
		},
		{
			name: "yarn info",
			plan: Plan{Method: "yarn", Package: "@openai/codex"},
			responses: []managerCommandResponse{
				{argv: []string{"yarn", "global", "outdated", "--json", "@openai/codex"}, output: ""},
				{argv: []string{"yarn", "info", "@openai/codex@latest", "version", "--json"}, output: `{"type":"inspect","data":"1.3.0"}`},
			},
		},
		{
			name: "winget show versions",
			plan: Plan{Method: "winget", Package: "GitHub.Copilot"},
			responses: []managerCommandResponse{
				{argv: []string{"winget", "upgrade", "--disable-interactivity"}, output: "No applicable upgrade found.\n"},
				{argv: []string{"winget", "show", "--id", "GitHub.Copilot", "--exact", "--source", "winget", "--versions", "--accept-source-agreements", "--disable-interactivity"}, output: "Found GitHub Copilot [GitHub.Copilot]\nVersion\n-------\n1.3.0\n1.2.0\n"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, _ := parseUpdateVersion("1.3.0")
			checker := newManagedVersionChecker(managerCommandRunner(t, tt.responses...), offlineManagerClient())
			got, err := checker(context.Background(), tt.plan, current)
			if err != nil || got.Latest != "1.3.0" {
				t.Fatalf("result=%+v err=%v", got, err)
			}
		})
	}
}

func TestManagedVersionCheckerReadsRegistryFirstForInstalledPrereleaseChannel(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != "https://registry.npmjs.org/@openai%2Fcodex" {
			t.Fatalf("URL = %s", request.URL.String())
		}
		return managerHTTPResponse(http.StatusOK, `{"dist-tags":{"latest":"1.3.0","beta":"2.0.0-beta.3"}}`), nil
	})}
	commands := managerCommandRunner(t)
	current, _ := parseUpdateVersion("2.0.0-beta.1")
	got, err := newManagedVersionChecker(commands, client)(context.Background(), Plan{Method: "npm", Package: "@openai/codex"}, current)
	if err != nil {
		t.Fatal(err)
	}
	if got.Latest != "2.0.0-beta.3" || got.Channel != "beta" || requests != 1 {
		t.Fatalf("result=%+v requests=%d", got, requests)
	}
}

func TestManagedVersionCheckerRejectsMissingPrereleaseChannel(t *testing.T) {
	client := &http.Client{Transport: managerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return managerHTTPResponse(http.StatusOK, `{"dist-tags":{"latest":"1.3.0","next":"2.0.0-next.2"}}`), nil
	})}
	// A definitive registry answer is final; the package manager is not asked again.
	commands := managerCommandRunner(t)
	current, _ := parseUpdateVersion("2.0.0-beta.1")
	if _, err := newManagedVersionChecker(commands, client)(context.Background(), Plan{Method: "npm", Package: "@openai/codex"}, current); err == nil {
		t.Fatal("missing beta dist-tag unexpectedly produced an advisory")
	}
}

func TestManagedVersionCheckerQueriesPyPIForOwnedPythonTools(t *testing.T) {
	for _, method := range []string{"uv", "pipx"} {
		t.Run(method, func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != "https://pypi.org/pypi/mistral-vibe/json" {
					t.Fatalf("URL = %s", request.URL.String())
				}
				return managerHTTPResponse(http.StatusOK, `{"info":{"version":"2.0.0"}}`), nil
			})}
			commandsCalled := false
			commands := commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
				commandsCalled = true
				return nil
			})
			current, _ := parseUpdateVersion("1.0.0")
			got, err := newManagedVersionChecker(commands, client)(context.Background(), Plan{Method: method, Package: "mistral-vibe"}, current)
			if err != nil || got.Latest != "2.0.0" {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if commandsCalled {
				t.Fatal("version checker repeated the ownership inventory command")
			}
		})
	}
}

func TestManagedVersionCheckerRejectsBadRegistryResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non ok", status: http.StatusServiceUnavailable, body: `{}`},
		{name: "malformed", status: http.StatusOK, body: `{`},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", (1<<20)+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return managerHTTPResponse(tt.status, tt.body), nil
			})}
			current, _ := parseUpdateVersion("1.0.0")
			if _, err := newManagedVersionChecker(nil, client)(context.Background(), Plan{Method: "uv", Package: "mistral-vibe"}, current); err == nil {
				t.Fatal("bad registry response unexpectedly succeeded")
			}
		})
	}
}

func TestManagedVersionCheckerRejectsAmbiguousWingetRows(t *testing.T) {
	commands := managerCommandRunner(t, managerCommandResponse{
		argv:   []string{"winget", "upgrade", "--disable-interactivity"},
		output: "GitHub Copilot  GitHub.Copilot  1.2.0  1.3.0  winget\nGitHub Copilot Preview  GitHub.Copilot  2.0.0  2.1.0  winget\n",
	})
	current, _ := parseUpdateVersion("1.2.0")
	if _, err := newManagedVersionChecker(commands, &http.Client{})(context.Background(), Plan{Method: "winget", Package: "GitHub.Copilot"}, current); err == nil {
		t.Fatal("ambiguous Winget output unexpectedly succeeded")
	}
}

func managerHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestHomebrewVersionMatchesCaskTokenNotDisplayName(t *testing.T) {
	raw := `{"formulae":[],"casks":[{"token":"other","name":["codex"],"version":"9.9.9"},{"token":"codex","name":["Codex"],"version":"0.160.1"}]}`
	if got := parseHomebrewVersion(raw, "codex", true, false); got != "0.160.1" {
		t.Fatalf("version = %q", got)
	}
}

// offlineManagerClient fails every request, so tests exercise the package
// manager's local commands without reaching a live registry.
func offlineManagerClient() *http.Client {
	return &http.Client{Transport: managerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}
}

func TestHomebrewCoreVersionReadsLiveAPIBeforeLocalMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, wantURL, body string
		plan                Plan
	}{
		{name: "cask", plan: Plan{Method: "homebrew", Package: "claude-code", PackageCask: true}, wantURL: "https://formulae.brew.sh/api/cask/claude-code.json", body: `{"token":"claude-code","version":"2.1.285"}`},
		{name: "cask with download token", plan: Plan{Method: "homebrew", Package: "claude-code", PackageCask: true}, wantURL: "https://formulae.brew.sh/api/cask/claude-code.json", body: `{"token":"claude-code","version":"2.1.285,8f3a2c"}`},
		{name: "formula", plan: Plan{Method: "homebrew", Package: "qwen-code"}, wantURL: "https://formulae.brew.sh/api/formula/qwen-code.json", body: `{"name":"qwen-code","versions":{"stable":"2.1.285"}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != tt.wantURL {
					t.Fatalf("URL = %s, want %s", request.URL, tt.wantURL)
				}
				return managerHTTPResponse(http.StatusOK, tt.body), nil
			})}
			// Local brew metadata would still report the stale release; it must not be consulted.
			current, _ := parseUpdateVersion("2.1.200")
			got, err := newManagedVersionChecker(managerCommandRunner(t), client)(context.Background(), tt.plan, current)
			if err != nil || got.Latest != "2.1.285" {
				t.Fatalf("result=%+v err=%v", got, err)
			}
		})
	}
}

func TestHomebrewTapVersionUsesLocalMetadataOnly(t *testing.T) {
	client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("tap package queried the Homebrew API: %s", request.URL)
		return nil, nil
	})}
	commands := managerCommandRunner(t, managerCommandResponse{
		argv:   []string{"brew", "outdated", "--json=v2", "--formula", "anomalyco/tap/opencode"},
		output: `{"formulae":[{"name":"anomalyco/tap/opencode","installed_versions":["1.18.34"],"current_version":"1.18.35"}],"casks":[]}`,
	})
	current, _ := parseUpdateVersion("1.18.34")
	got, err := newManagedVersionChecker(commands, client)(context.Background(), Plan{Method: "homebrew", Package: "anomalyco/tap/opencode"}, current)
	if err != nil || got.Latest != "1.18.35" {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestNodeManagersReadRegistryWithoutStartingManager(t *testing.T) {
	for _, method := range []string{"npm", "pnpm", "yarn", "bun"} {
		t.Run(method, func(t *testing.T) {
			client := &http.Client{Transport: managerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != "https://registry.npmjs.org/@openai%2Fcodex" {
					t.Fatalf("URL = %s", request.URL)
				}
				return managerHTTPResponse(http.StatusOK, `{"dist-tags":{"latest":"1.3.0"}}`), nil
			})}
			current, _ := parseUpdateVersion("1.2.0")
			got, err := newManagedVersionChecker(managerCommandRunner(t), client)(context.Background(), Plan{Method: method, Package: "@openai/codex"}, current)
			if err != nil || got.Latest != "1.3.0" {
				t.Fatalf("result=%+v err=%v", got, err)
			}
		})
	}
}
