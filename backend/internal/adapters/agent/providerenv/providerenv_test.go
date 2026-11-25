package providerenv

import (
	"reflect"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/agentlaunch"
)

func TestProviderValuesStayInLaunchEnvironment(t *testing.T) {
	setCommandGOOS(t, "linux")
	secret := "synthetic-'quoted'-$(do-not-evaluate)-credential"
	env := map[string]string{"ANTHROPIC_API_KEY": secret, "DEEPSEEK_BASE_URL": "https://user:synthetic-password@gateway.example/v1", "OPENAI_BASE_URL": "", "UNRELATED": "keep"}
	Capture(env)
	argv := Command([]string{"native-cli", "--resume", "saved-id"}, env)
	text := strings.Join(argv, " ")
	for _, value := range []string{secret, env["DEEPSEEK_BASE_URL"]} {
		if strings.Contains(text, value) {
			t.Fatal("provider value exposed in process arguments")
		}
	}
	if env[bridgePrefix+"ANTHROPIC_API_KEY"] != secret || env[bridgePrefix+"DEEPSEEK_BASE_URL"] != env["DEEPSEEK_BASE_URL"] {
		t.Fatal("explicit launch values lost")
	}
	if !strings.Contains(text, "unset OPENAI_BASE_URL;") {
		t.Fatal("explicit clear not preserved")
	}
	if strings.Contains(text, "UNRELATED") {
		t.Fatal("unrelated environment rewritten")
	}
	if !reflect.DeepEqual(argv[len(argv)-3:], []string{"native-cli", "--resume", "saved-id"}) {
		t.Fatal("native identity changed")
	}
}

func setCommandGOOS(t *testing.T, goos string) {
	t.Helper()
	previous := commandGOOS
	commandGOOS = goos
	t.Cleanup(func() { commandGOOS = previous })
}

// The wrapper must not hide the agent from binary validation or PATH
// augmentation, and Windows (ConPTY, no sh) keeps argv unchanged.
func TestProviderWrapperKeepsAgentBinaryVisible(t *testing.T) {
	argv := []string{"env", "CODEWHALE_MODE=x", "/opt/agents/codewhale", "--resume", "id"}
	env := map[string]string{"OPENAI_API_KEY": "synthetic-key"}
	Capture(env)
	setCommandGOOS(t, "linux")
	wrapped := Command(argv, env)
	if wrapped[0] != "sh" || !reflect.DeepEqual(agentlaunch.UnwrapProviderEnv(wrapped), argv) {
		t.Fatalf("wrapper did not unwrap to the agent argv: %q", wrapped)
	}
	setCommandGOOS(t, "windows")
	if got := Command(argv, env); !reflect.DeepEqual(got, argv) {
		t.Fatalf("windows argv changed: %q", got)
	}
}

func TestNoExplicitProviderEnvironmentLeavesNativeCommandUntouched(t *testing.T) {
	argv := []string{"native-cli", "--resume", "saved-id"}
	for _, env := range []map[string]string{nil, {}, {"UNRELATED": "keep"}} {
		Capture(env)
		if got := Command(argv, env); !reflect.DeepEqual(got, argv) {
			t.Fatalf("native routing changed: %v", got)
		}
	}
}
