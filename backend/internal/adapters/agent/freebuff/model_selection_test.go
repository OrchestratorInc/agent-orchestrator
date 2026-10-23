package freebuff

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type startupTerminalStep struct {
	input  string
	output string
}

type startupTerminalFixture struct {
	t      *testing.T
	output string
	steps  []startupTerminalStep
	inputs []string
	err    error
}

func initializeStartupFixture(ctx context.Context, t *testing.T, cfg ports.LaunchConfig, terminal ports.AgentStartupTerminal) error {
	t.Helper()
	// Every test, including parallel fixtures, owns a separate native config.
	// Never inherit the daemon's or developer's real Freebuff preferences.
	cfg.SessionID = t.Name()
	cfg.Env = map[string]string{configDirEnv: t.TempDir()}
	p := New()
	release, err := p.BeginStartup(ctx, cfg)
	if err != nil {
		return err
	}
	defer release()
	return p.InitializeTerminal(ctx, cfg, terminal)
}

func (f *startupTerminalFixture) Output(context.Context) (string, error) {
	return f.output, f.err
}

func (f *startupTerminalFixture) SendInput(_ context.Context, input string) error {
	f.t.Helper()
	f.inputs = append(f.inputs, input)
	if len(f.steps) == 0 {
		f.t.Fatalf("unexpected native input %q", input)
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	if input != step.input {
		f.t.Fatalf("native input = %q, want %q", input, step.input)
	}
	f.output = step.output
	return nil
}

func startupComposer(model, effort string) string {
	selection := model
	if effort != "" {
		selection += " • " + effort
	}
	return strings.Replace(freebuffTerminalScreen("unlimited", freebuffIdleInput), "GLM 5.3 Flash • high", selection, 1)
}

func startupModelCommand() string {
	return freebuffTerminalScreen("unlimited", "/model\x1b[1m▍\x1b[0m")
}

func startupModelPicker(model, effort string) string {
	name := model
	if effort != "" {
		name += " • " + effort
	}
	return modelPickerHeading + "\nUNLIMITED\n┌──────────────────────────────────────────────────────┐\n│ › \x1b[1m" + name + "\x1b[0m  Model tagline                       │\n│                   No usage limit                     │\n└──────────────────────────────────────────────────────┘\n↑  Show fewer\n" + startupPickerFooter
}

const startupPickerFooter = "GLM 5.3 Flash • high · /workspace · /model to change · Chat: New chat\n← for history · ? for help\n"

func startupEffortPicker(model, focused string) string {
	var out strings.Builder
	out.WriteString(modelPickerHeading + "\n" + model + " • Reasoning\n↑↓ choose · Enter save · Esc back\n")
	for _, effort := range []string{"low", "high"} {
		if effort == focused {
			out.WriteString("› ")
		} else {
			out.WriteString("  ")
		}
		out.WriteString(effort)
		if effort == "high" {
			out.WriteString(" (default)")
		}
		out.WriteByte('\n')
	}
	out.WriteString(startupPickerFooter)
	return out.String()
}

func TestInitializeTerminalSelectsNativeModelAndEffort(t *testing.T) {
	initial := startupComposer("GLM 5.3 Flash", "high")
	terminal := &startupTerminalFixture{t: t, output: initial, steps: []startupTerminalStep{
		{"/model", startupModelCommand()},
		{"\r", startupModelPicker("GLM 5.3 Flash", "high")},
		{"\t", startupEffortPicker("GLM 5.3 Flash", "high")},
		{"\x1b[B", startupEffortPicker("GLM 5.3 Flash", "low")},
		{"\r", startupModelPicker("GLM 5.3 Flash", "low")},
		{"\r", startupComposer("GLM 5.3 Flash", "low")},
	}}
	err := initializeStartupFixture(context.Background(), t, ports.LaunchConfig{Config: ports.AgentConfig{Model: "z-ai/glm-5.3-flash", Effort: "low"}}, terminal)
	if err != nil {
		t.Fatal(err)
	}
	if len(terminal.steps) != 0 {
		t.Fatalf("%d native input steps were not performed", len(terminal.steps))
	}
}

func TestInitializeTerminalReselectsNativeModelEvenWhenFooterAlreadyMatches(t *testing.T) {
	// Native nextModel is process-local. Explicitly picking the model protects
	// this process from another launch changing the shared saved preference
	// before the initial server catalog has arrived.
	initial := startupComposer("DeepSeek V4.1 Flash 10/17", "high")
	terminal := &startupTerminalFixture{t: t, output: initial, steps: []startupTerminalStep{
		{"/model", startupModelCommand()},
		{"\r", startupModelPicker("DeepSeek V4.1 Flash 10/17", "high")},
		{"\r", initial},
	}}
	if err := initializeStartupFixture(context.Background(), t, ports.LaunchConfig{Config: ports.AgentConfig{Model: "deepseek/deepseek-v4-flash"}}, terminal); err != nil {
		t.Fatal(err)
	}
	if len(terminal.steps) != 0 {
		t.Fatalf("%d native input steps were not performed", len(terminal.steps))
	}
}

func TestInitializeTerminalSelectionsAreIndependent(t *testing.T) {
	for _, tc := range []struct{ id, label string }{
		{"z-ai/glm-5.3-flash", "GLM 5.3 Flash"},
		{"openai/gpt-6-luna", "GPT-6 Luna"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			initial := startupComposer("MiMo v2.6 Flash Fast", "")
			terminal := &startupTerminalFixture{t: t, output: initial, steps: []startupTerminalStep{
				{"/model", startupModelCommand()}, {"\r", startupModelPicker(tc.label, "high")}, {"\r", startupComposer(tc.label, "high")},
			}}
			if err := initializeStartupFixture(context.Background(), t, ports.LaunchConfig{Config: ports.AgentConfig{Model: tc.id}}, terminal); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInitializeTerminalRejectsUnsupportedSelectionsWithoutInput(t *testing.T) {
	for _, cfg := range []ports.AgentConfig{
		{Model: "arbitrary/server-only-model"},
		{Model: "mimo/mimo-v2.5", Effort: "high"},
		{Model: "z-ai/glm-5.3-flash", Effort: "max"},
		{Effort: "high"},
	} {
		terminal := &startupTerminalFixture{t: t}
		if err := New().InitializeTerminal(context.Background(), ports.LaunchConfig{Config: cfg}, terminal); err == nil {
			t.Fatalf("config %+v should fail", cfg)
		}
		if len(terminal.inputs) != 0 {
			t.Fatalf("invalid config sent input: %q", terminal.inputs)
		}
	}
}

func TestInitializeTerminalDoesNotTypeIntoDraftOrUnknownSurface(t *testing.T) {
	for name, screen := range map[string]string{
		"draft":    freebuffTerminalScreen("unlimited", "existing human draft"),
		"plain":    "Enter a coding task or / for commands\n",
		"sign in":  "Log in to Freebuff\nPress Enter to continue\n",
		"decision": "Confirm payment\nEnter confirm · Esc cancel\n",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			terminal := &startupTerminalFixture{t: t, output: screen}
			err := initializeStartupFixture(ctx, t, ports.LaunchConfig{Config: ports.AgentConfig{Model: "z-ai/glm-5.3-flash"}}, terminal)
			if !errors.Is(err, context.DeadlineExceeded) || len(terminal.inputs) != 0 {
				t.Fatalf("err=%v inputs=%q", err, terminal.inputs)
			}
		})
	}
}

func TestInitializeTerminalRequiresNativeSelectionConfirmation(t *testing.T) {
	initial := startupComposer("GLM 5.3 Flash", "high")
	terminal := &startupTerminalFixture{t: t, output: initial, steps: []startupTerminalStep{
		{"/model", startupModelCommand()}, {"\r", startupModelPicker("GLM 5.3 Flash", "high")},
		// A native rejection or changed layout must not report a successful
		// startup or press Enter a second time into an arbitrary dialog.
		{"\r", "Model unavailable\nEnter to confirm a different model\n"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := initializeStartupFixture(ctx, t, ports.LaunchConfig{Config: ports.AgentConfig{Model: "z-ai/glm-5.3-flash"}}, terminal)
	if err == nil || !strings.Contains(err.Error(), "selection was not confirmed") || len(terminal.inputs) != 3 {
		t.Fatalf("err=%v inputs=%q", err, terminal.inputs)
	}
}

func TestInitializeTerminalRejectsNativeFallbackModel(t *testing.T) {
	for _, native := range []string{"MiMo v2.6 Flash Fast", "DeepSeek V4.1 Flash Fast", "DeepSeek V4.1 Flash Fast 10/17"} {
		t.Run(native, func(t *testing.T) {
			initial := startupComposer(native, "high")
			terminal := &startupTerminalFixture{t: t, output: initial, steps: []startupTerminalStep{
				{"/model", startupModelCommand()}, {"\r", startupModelPicker(native, "high")},
			}}
			err := initializeStartupFixture(context.Background(), t, ports.LaunchConfig{Config: ports.AgentConfig{Model: "deepseek/deepseek-v4-flash"}}, terminal)
			if err == nil || !strings.Contains(err.Error(), "unexpected native model") || len(terminal.inputs) != 2 {
				t.Fatalf("err=%v inputs=%q", err, terminal.inputs)
			}
		})
	}
}

func TestNativeModelLabelCompatibilityDoesNotUseBroadPrefixes(t *testing.T) {
	for label, want := range map[string]bool{
		"DeepSeek V4.1 Flash":            true,
		"DeepSeek V4.1 Flash 10/17":      true,
		"DeepSeek V4.1 Flash 1/9":        true,
		"DeepSeek V4.1 Flash Fast":       false,
		"DeepSeek V4.1 Flash Fast 10/17": false,
		"DeepSeek V4.1 Flash 99/99":      false,
		"DeepSeek V4.1 Flash unknown":    false,
		"MiMo v2.6 Flash Fast":           false,
	} {
		if got := compatibleNativeModelLabel("DeepSeek V4.1 Flash", label); got != want {
			t.Fatalf("label %q compatible=%v want=%v", label, got, want)
		}
	}
}

func TestInitializeTerminalRequiresMatchingStartupOwner(t *testing.T) {
	cfg := ports.LaunchConfig{SessionID: "owner", Env: map[string]string{configDirEnv: t.TempDir()}, Config: ports.AgentConfig{Model: "z-ai/glm-5.3-flash"}}
	p := New()
	terminal := &startupTerminalFixture{t: t, output: startupComposer("GLM 5.3 Flash", "high")}
	if err := p.InitializeTerminal(context.Background(), cfg, terminal); err == nil {
		t.Fatal("unguarded selection was accepted")
	}
	release, err := p.BeginStartup(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	other := cfg
	other.SessionID = "different-session"
	if err := p.InitializeTerminal(context.Background(), other, terminal); err == nil || len(terminal.inputs) != 0 {
		t.Fatalf("mismatched owner err=%v inputs=%q", err, terminal.inputs)
	}
}

func TestInitializeTerminalWithoutModelStillRequiresEmptyNativeComposer(t *testing.T) {
	for _, screen := range []string{freebuffTerminalScreen("unlimited", "human draft"), "Log in to Freebuff"} {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		terminal := &startupTerminalFixture{t: t, output: screen}
		err := initializeStartupFixture(ctx, t, ports.LaunchConfig{}, terminal)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || len(terminal.inputs) != 0 {
			t.Fatalf("err=%v inputs=%q", err, terminal.inputs)
		}
	}
}

func TestFocusedNativeModelRejectsTranscriptAndAmbiguousMarkers(t *testing.T) {
	valid := startupModelPicker("GLM 5.3 Flash", "high")
	if got, ok := focusedNativeModel(valid); !ok || got != "GLM 5.3 Flash" {
		t.Fatalf("model=%q ok=%v", got, ok)
	}
	for name, screen := range map[string]string{
		"no picker heading":                      strings.TrimPrefix(valid, modelPickerHeading+"\n"),
		"plain transcript":                       strings.ReplaceAll(strings.ReplaceAll(valid, "\x1b[1m", ""), "\x1b[0m", ""),
		"multiple focuses":                       strings.Replace(valid, startupPickerFooter, "│ › \x1b[1mGPT-6 Luna\x1b[0m  tagline │\n"+startupPickerFooter, 1),
		"reasoning submenu":                      startupEffortPicker("GLM 5.3 Flash", "high"),
		"older live rows above current composer": valid + startupComposer("GLM 5.3 Flash", "high"),
		"partial without native footer":          strings.TrimSuffix(valid, startupPickerFooter),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := focusedNativeModel(screen); ok {
				t.Fatal("unconfirmed native picker accepted")
			}
		})
	}
}

func TestNativeModelFooterUsesCurrentChrome(t *testing.T) {
	valid := startupComposer("GLM 5.3 Flash", "low")
	if !nativeModelFooter(valid, "GLM 5.3 Flash", "low") {
		t.Fatal("current footer was not recognized")
	}
	if nativeModelFooter("GLM 5.3 Flash • low · /workspace · /model to change · Chat: New chat\n"+startupComposer("GPT-6 Luna", "high"), "GLM 5.3 Flash", "low") {
		t.Fatal("transcript model must not override current native footer")
	}
	if nativeModelFooter(valid, "GLM 5.3 Flash", "high") || nativeModelFooter(valid, "GLM 5.3", "low") {
		t.Fatal("model and effort must match exactly")
	}
}
