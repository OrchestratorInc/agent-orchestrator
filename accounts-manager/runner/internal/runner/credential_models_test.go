package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type catalogExecutor struct {
	coreauth.ProviderExecutor
	provider string
}

func (e *catalogExecutor) Identifier() string { return e.provider }

func TestCredentialExecutorOwnership(t *testing.T) {
	for _, mode := range []string{"nil-source", "nil-destination", "same-manager", "missing", "partial", "changed-provider", "complete"} {
		t.Run(mode, func(t *testing.T) {
			source := coreauth.NewManager(nil, nil, nil)
			runtime := &credentialRuntime{manager: coreauth.NewManager(nil, nil, nil)}
			if _, err := runtime.manager.Register(t.Context(), &coreauth.Auth{ID: "managed-account", Provider: "codex"}); err != nil {
				t.Fatal(err)
			}
			executors := make(map[string]*catalogExecutor)
			for provider := range oauthProviders {
				executors[provider] = &catalogExecutor{provider: provider}
				source.RegisterExecutor(executors[provider])
			}
			switch mode {
			case "nil-source":
				source = nil
			case "nil-destination":
				runtime.manager = nil
			case "same-manager":
				source = runtime.manager
			case "missing":
				source = coreauth.NewManager(nil, nil, nil)
			case "partial":
				source.UnregisterExecutor("codex")
			case "changed-provider":
				executors["codex"].provider = "foreign-provider"
			}
			err := runtime.attachExecutors(source)
			if mode != "complete" {
				if err == nil {
					t.Fatal("incomplete executor setup was accepted")
				}
				if runtime.manager != nil {
					for provider := range oauthProviders {
						if _, exists := runtime.manager.Executor(provider); exists {
							t.Fatal("rejected setup published a partial executor set")
						}
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for provider, executor := range executors {
				if got, exists := runtime.manager.Executor(provider); !exists || got != executor {
					t.Fatal("supported executor reference was not attached")
				}
			}
			if len(source.List()) != 0 || len(runtime.manager.List()) != 1 {
				t.Fatal("executor setup transferred account ownership")
			}
		})
	}
}

func TestRunnerRejectsMissingExecutor(t *testing.T) {
	if testing.Short() {
		t.Skip("runner subprocess integration")
	}
	stateDir, _ := validStateFixture(t)
	replaceInFile(t, filepath.Join(stateDir, configFileName), "43127", strconv.Itoa(reservePort(t)))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunnerHelperProcess$")
	command.Env = append(os.Environ(), runnerHelperEnvironment+"=1", "AO_ACCOUNTS_MANAGER_TEST_STATE="+stateDir, "AO_ACCOUNTS_MANAGER_TEST_MODEL_BARRIER=missing-executor", "GIN_MODE=release", "GORACE=atexit_sleep_ms=0")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil || err == nil || !strings.Contains(string(output), "credential executor is unavailable") {
		t.Fatalf("missing executor did not reject startup: err=%v timeout=%v output=%s", err, ctx.Err(), output)
	}
}
