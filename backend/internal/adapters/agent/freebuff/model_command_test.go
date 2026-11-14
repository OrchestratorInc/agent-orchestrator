package freebuff

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func startupModelAutocomplete() string {
	// Captured native Freebuff0.2.32 menu: both suggestions are inside the
	// composer's border, followed by a blank row and the exact input draft.
	return freebuffTerminalScreen("unlimited",
		"/model      Choose the model for your next message",
		"/reasoning  Choose reasoning effort for your next message model",
		"", "/model\x1b[38;2;101;168;62;1m▍\x1b[0m")
}

func TestPendingModelCommandRequiresExactNativeDraft(t *testing.T) {
	for name, tc := range map[string]struct {
		output string
		want   bool
	}{
		"command":        {startupModelCommand(), true},
		"autocomplete":   {startupModelAutocomplete(), true},
		"idle":           {startupComposer("GLM 5.3 Flash", "high"), false},
		"different":      {freebuffTerminalScreen("", "/model other\x1b[1m▍\x1b[0m"), false},
		"human prefix":   {freebuffTerminalScreen("", "keep this draft", "", "/model\x1b[1m▍\x1b[0m"), false},
		"human suffix":   {freebuffTerminalScreen("", "/model", "keep this draft\x1b[1m▍\x1b[0m"), false},
		"literal cursor": {freebuffTerminalScreen("", "/mo▍del\x1b[1m▍\x1b[0m"), false},
		"partial":        {"│ /model\x1b[1m▍\x1b[0m │\n", false},
		"dialog":         {"Confirm a different model\nEnter accept · Esc cancel\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := pendingModelCommand(tc.output); got != tc.want {
				t.Fatalf("pendingModelCommand()=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestOpenNativeModelPickerRetriesOnlyRenderedCommand(t *testing.T) {
	picker := startupModelPicker("GLM 5.3 Flash", "high")
	terminal := &startupTerminalFixture{t: t, steps: []startupTerminalStep{
		{"/model", startupModelAutocomplete()},
		{"\r", startupModelAutocomplete()}, // First Enter is consumed by the slash menu.
		{"\r", picker},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := openNativeModelPicker(ctx, terminal)
	if err != nil || output != picker || len(terminal.inputs) != 3 || len(terminal.steps) != 0 {
		t.Fatalf("err=%v inputs=%q remaining=%d", err, terminal.inputs, len(terminal.steps))
	}
}

func TestOpenNativeModelPickerNeverRetriesIntoChangedSurface(t *testing.T) {
	for name, output := range map[string]string{
		"dialog":    "Confirm spending\nEnter accept · Esc cancel\n",
		"draft":     freebuffTerminalScreen("", "different human draft\x1b[1m▍\x1b[0m"),
		"unknown":   "loading model picker\n",
		"empty":     startupComposer("GLM 5.3 Flash", "high"),
		"reasoning": startupEffortPicker("GLM 5.3 Flash", "high"),
	} {
		t.Run(name, func(t *testing.T) {
			terminal := &startupTerminalFixture{t: t, steps: []startupTerminalStep{
				{"/model", startupModelCommand()}, {"\r", output},
			}}
			timeout := 30 * time.Millisecond
			if name == "dialog" {
				// Observe beyond the retry interval: a delayed retry into the
				// dialog would select an unrelated action.
				timeout = time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			_, err := openNativeModelPicker(ctx, terminal)
			if !errors.Is(err, context.DeadlineExceeded) || len(terminal.inputs) != 2 {
				t.Fatalf("err=%v inputs=%q", err, terminal.inputs)
			}
		})
	}
}

func TestOpenNativeModelPickerBoundsEnterRetries(t *testing.T) {
	command := startupModelCommand()
	terminal := &startupTerminalFixture{t: t, steps: []startupTerminalStep{
		{"/model", command}, {"\r", command}, {"\r", command}, {"\r", command},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := openNativeModelPicker(ctx, terminal)
	if !errors.Is(err, context.DeadlineExceeded) || len(terminal.inputs) != 4 || len(terminal.steps) != 0 {
		t.Fatalf("err=%v inputs=%q remaining=%d", err, terminal.inputs, len(terminal.steps))
	}
}

type delayedModelCommandTerminal struct {
	startupTerminalFixture
	readsBeforeCommand int
}

func (f *delayedModelCommandTerminal) Output(ctx context.Context) (string, error) {
	if f.readsBeforeCommand > 0 {
		f.readsBeforeCommand--
		return startupComposer("GLM 5.3 Flash", "high"), nil
	}
	return f.startupTerminalFixture.Output(ctx)
}

func (f *delayedModelCommandTerminal) SendInput(ctx context.Context, input string) error {
	if input == "\r" && f.readsBeforeCommand > 0 {
		f.t.Fatal("Enter sent before native command rendering was acknowledged")
	}
	return f.startupTerminalFixture.SendInput(ctx, input)
}

func TestOpenNativeModelPickerWaitsForRenderedCommandBeforeEnter(t *testing.T) {
	terminal := &delayedModelCommandTerminal{
		startupTerminalFixture: startupTerminalFixture{t: t, steps: []startupTerminalStep{
			{"/model", startupModelCommand()}, {"\r", startupModelPicker("GLM 5.3 Flash", "high")},
		}},
		readsBeforeCommand: 2,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := openNativeModelPicker(ctx, terminal)
	if err != nil || terminal.readsBeforeCommand != 0 || len(terminal.inputs) != 2 {
		t.Fatalf("err=%v reads=%d inputs=%q", err, terminal.readsBeforeCommand, terminal.inputs)
	}
}

func TestOpenNativeModelPickerAcceptsAlreadyOpenedPickerWithoutEnter(t *testing.T) {
	terminal := &startupTerminalFixture{t: t, steps: []startupTerminalStep{{"/model", startupModelPicker("GLM 5.3 Flash", "high")}}}
	if _, err := openNativeModelPicker(context.Background(), terminal); err != nil || strings.Join(terminal.inputs, "") != "/model" {
		t.Fatalf("err=%v inputs=%q", err, terminal.inputs)
	}
}
