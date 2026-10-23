package freebuff

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const modelPickerHeading = "↑↓ choose model · Tab reasoning · Enter select · Esc cancel"

// InitializeTerminal pins the startup seed through the native picker's
// process-local selection. BeginStartup keeps the canonical seed unchanged
// until this process has resolved it against its own server catalog. The
// native label can differ from the compiled name (e.g. a dated model release),
// so we reselect the row Freebuff itself resolved, then confirm its footer.
func (p *Plugin) InitializeTerminal(ctx context.Context, cfg ports.LaunchConfig, terminal ports.AgentStartupTerminal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	modelID, effort := strings.TrimSpace(cfg.Config.Model), strings.TrimSpace(cfg.Config.Effort)
	var model ports.AgentModelInfo
	if modelID != "" || effort != "" {
		var ok bool
		model, ok = startupModel(modelID)
		if !ok {
			return fmt.Errorf("freebuff: cannot select unknown native model %q", modelID)
		}
		if effort != "" && !containsString(model.Efforts, effort) {
			return fmt.Errorf("freebuff: model %q does not offer reasoning effort %q", modelID, effort)
		}
		if !p.ownsStartup(cfg) {
			return errors.New("freebuff: native model selection requires its active startup guard")
		}
	}
	readyCtx, cancelReady := context.WithTimeout(ctx, 60*time.Second)
	_, err := waitStartupOutput(readyCtx, terminal, p.ComposerIsEmpty)
	cancelReady()
	if err != nil {
		return fmt.Errorf("freebuff: wait for an empty native composer: %w", err)
	}
	if modelID == "" {
		return nil
	}
	selectionCtx, cancelSelection := context.WithTimeout(ctx, 15*time.Second)
	defer cancelSelection()
	output, err := openNativeModelPicker(selectionCtx, terminal)
	if err != nil {
		return err
	}
	nativeLabel, _ := focusedNativeModel(output)
	if !compatibleNativeModelLabel(model.Label, nativeLabel) {
		return fmt.Errorf("freebuff: canonical model %q resolved to unexpected native model %q", modelID, nativeLabel)
	}
	if effort != "" {
		if err := selectNativeEffort(selectionCtx, terminal, nativeLabel, effort); err != nil {
			return err
		}
	}
	// Enter only selects the model. Freebuff waits for the user's first task
	// before admission and any native spending confirmation.
	if err := terminal.SendInput(selectionCtx, "\r"); err != nil {
		return fmt.Errorf("freebuff: select native model: %w", err)
	}
	if _, err := waitStartupOutput(selectionCtx, terminal, func(output string) bool {
		return p.ComposerIsEmpty(output) && nativeModelFooter(output, nativeLabel, effort)
	}); err != nil {
		return fmt.Errorf("freebuff: model %q selection was not confirmed: %w", nativeLabel, err)
	}
	return nil
}

// openNativeModelPicker separates typed-command acknowledgment from submission.
// An early Enter can be consumed while the slash menu renders. Retry only when
// the complete current composer still proves that our exact command is pending;
// once the picker opens, no Enter in this helper can select a model.
func openNativeModelPicker(ctx context.Context, terminal ports.AgentStartupTerminal) (string, error) {
	if err := terminal.SendInput(ctx, "/model"); err != nil {
		return "", fmt.Errorf("freebuff: open model picker: %w", err)
	}
	output, err := waitStartupOutput(ctx, terminal, func(output string) bool {
		_, opened := focusedNativeModel(output)
		return opened || pendingModelCommand(output)
	})
	if err != nil {
		return "", fmt.Errorf("freebuff: native model command was not rendered: %w", err)
	}
	if _, opened := focusedNativeModel(output); opened {
		return output, nil
	}
	if err := terminal.SendInput(ctx, "\r"); err != nil {
		return "", fmt.Errorf("freebuff: open model picker: %w", err)
	}
	var pendingSince time.Time
	retries := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("freebuff: native model picker was not confirmed: %w", err)
		}
		output, err = terminal.Output(ctx)
		if err != nil {
			return "", fmt.Errorf("freebuff: read native model command: %w", err)
		}
		if _, opened := focusedNativeModel(output); opened {
			return output, nil
		}
		if pendingModelCommand(output) {
			if pendingSince.IsZero() {
				pendingSince = time.Now()
			}
			if retries < 2 && time.Since(pendingSince) >= 750*time.Millisecond {
				if err := terminal.SendInput(ctx, "\r"); err != nil {
					return "", fmt.Errorf("freebuff: submit pending native model command: %w", err)
				}
				retries++
				pendingSince = time.Time{}
			}
		} else {
			pendingSince = time.Time{}
		}
		if err := startupPause(ctx, 100*time.Millisecond); err != nil {
			return "", fmt.Errorf("freebuff: native model picker was not confirmed: %w", err)
		}
	}
}

func pendingModelCommand(output string) bool {
	composer, ok := currentComposer(output)
	if !ok || composer.placeholder != "" {
		return false
	}
	rows := strings.Split(strings.TrimSpace(composer.content), "\n")
	if len(rows) == 0 || strings.TrimSpace(rows[len(rows)-1]) != "/model" {
		return false
	}
	if len(rows) == 1 {
		return true
	}
	// Freebuff draws these slash suggestions above the draft, separated by a
	// blank row. They are native menu chrome, not additional command content.
	if strings.TrimSpace(rows[len(rows)-2]) != "" {
		return false
	}
	menu := strings.Join(strings.Fields(strings.Join(rows[:len(rows)-2], " ")), " ")
	return menu == "/model Choose the model for your next message" ||
		menu == "/model Choose the model for your next message /reasoning Choose reasoning effort for your next message model"
}

// The online picker may append a release date to the same compiled model's
// name (Freebuff 0.2.32: DeepSeek V4.1 Flash 10/17). Only that bounded suffix is
// accepted after seeding the canonical id; another model or a Fast variant is
// a native fallback, not confirmation of the requested model.
func compatibleNativeModelLabel(compiled, native string) bool {
	if native == compiled {
		return true
	}
	return regexp.MustCompile("^" + regexp.QuoteMeta(compiled) + ` (?:0?[1-9]|1[0-2])/(?:0?[1-9]|[12][0-9]|3[01])$`).MatchString(native)
}

func startupModel(id string) (ports.AgentModelInfo, bool) {
	for _, model := range modelcatalog.Base(adapterID).Models {
		if model.ID == id {
			return model, true
		}
	}
	return ports.AgentModelInfo{}, false
}

func selectNativeEffort(ctx context.Context, terminal ports.AgentStartupTerminal, model, effort string) error {
	if err := terminal.SendInput(ctx, "\t"); err != nil {
		return fmt.Errorf("freebuff: open reasoning picker: %w", err)
	}
	output, err := waitStartupOutput(ctx, terminal, func(output string) bool {
		_, ok := focusedNativeEffort(output, model)
		return ok
	})
	if err != nil {
		return fmt.Errorf("freebuff: native reasoning picker was not confirmed: %w", err)
	}
	seen := map[string]bool{}
	for steps := 0; steps < 8; steps++ {
		focused, ok := focusedNativeEffort(output, model)
		if !ok {
			return errors.New("freebuff: native reasoning picker changed while selecting effort")
		}
		if focused == effort {
			break
		}
		if seen[focused] {
			return fmt.Errorf("freebuff: native picker does not offer reasoning effort %q", effort)
		}
		seen[focused] = true
		if err := terminal.SendInput(ctx, "\x1b[B"); err != nil {
			return fmt.Errorf("freebuff: navigate reasoning picker: %w", err)
		}
		output, err = waitStartupOutput(ctx, terminal, func(output string) bool {
			next, ok := focusedNativeEffort(output, model)
			return ok && next != focused
		})
		if err != nil {
			return fmt.Errorf("freebuff: native reasoning navigation was not confirmed: %w", err)
		}
	}
	if focused, ok := focusedNativeEffort(output, model); !ok || focused != effort {
		return fmt.Errorf("freebuff: native picker did not reach reasoning effort %q", effort)
	}
	if err := terminal.SendInput(ctx, "\r"); err != nil {
		return fmt.Errorf("freebuff: save native reasoning effort: %w", err)
	}
	if _, err := waitStartupOutput(ctx, terminal, func(output string) bool {
		focused, ok := focusedNativeModel(output)
		return ok && focused == model
	}); err != nil {
		return fmt.Errorf("freebuff: reasoning picker did not return to model selection: %w", err)
	}
	return nil
}

func focusedNativeModel(output string) (string, bool) {
	lines, raw, ok := nativePickerBody(output)
	if !ok {
		return "", false
	}
	focused := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "↑↓ choose · Enter save · Esc back") {
			return "", false
		}
		if !strings.HasPrefix(line, "│") || !strings.HasSuffix(line, "│") {
			continue
		}
		inside := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "│"), "│"))
		if !strings.HasPrefix(inside, "› ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(inside, "› "))
		for _, separator := range []string{" • ", " · ", "  "} {
			name, _, _ = strings.Cut(name, separator)
		}
		marker := strings.Index(raw[i], "›")
		if name == "" || marker < 0 || !terminalui.LastPromptHasBoldMarker(raw[i][marker+len("›"):], name) || focused != "" {
			return "", false
		}
		focused = name
	}
	return focused, focused != ""
}

func focusedNativeEffort(output, model string) (string, bool) {
	lines, _, ok := nativePickerBody(output)
	if !ok {
		return "", false
	}
	heading, hint := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case model + " • Reasoning":
			heading = i
		case "↑↓ choose · Enter save · Esc back":
			hint = i
		}
	}
	if heading < 0 || hint != heading+1 {
		return "", false
	}
	focused := ""
	for _, line := range lines[hint+1:] {
		line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│"))
		if !strings.HasPrefix(line, "› ") {
			continue
		}
		value := strings.TrimSuffix(strings.TrimPrefix(line, "› "), " (default)")
		if !containsString([]string{"low", "medium", "high", "xhigh", "max"}, value) || focused != "" {
			return "", false
		}
		focused = value
	}
	return focused, focused != ""
}

// Native controls replace the input dock and retain its model/history footer.
// Old picker rows above a current composer are transcript, not live controls.
func nativePickerBody(output string) ([]string, []string, bool) {
	if _, ok := currentComposer(output); ok {
		return nil, nil, false
	}
	lines := terminalui.PlainTerminalLines(output)
	raw := strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n")
	if len(lines) != len(raw) {
		return nil, nil, false
	}
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 2 || strings.TrimSpace(lines[last]) != "← for history · ? for help" {
		return nil, nil, false
	}
	heading := -1
	for i := last - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "❯") || composerBorderWidth(line, '╰', '╯') != 0 {
			return nil, nil, false
		}
		if line == modelPickerHeading {
			heading = i
			break
		}
	}
	if heading < 0 || !strings.Contains(strings.Join(lines[heading+1:last], " "), " · /model to change · Chat: ") {
		return nil, nil, false
	}
	return lines[heading+1 : last], raw[heading+1 : last], true
}

func nativeModelFooter(output, model, effort string) bool {
	lines := terminalui.PlainTerminalLines(output)
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 2 || strings.TrimSpace(lines[last]) != "← for history · ? for help" {
		return false
	}
	for i := last - 1; i >= 0 && i >= last-12; i-- {
		if composerBorderWidth(lines[i], '╰', '╯') == 0 && !strings.HasPrefix(strings.TrimSpace(lines[i]), "❯") {
			continue
		}
		rows := make([]string, 0, last-i-1)
		for _, line := range lines[i+1 : last] {
			rows = append(rows, strings.TrimSpace(line))
		}
		footer := strings.Join(rows, " ")
		if !strings.Contains(footer, " · /model to change · Chat: ") {
			return false
		}
		selection, _, ok := strings.Cut(footer, " · ")
		if !ok {
			return false
		}
		if effort != "" {
			return selection == model+" • "+effort
		}
		return selection == model || strings.HasPrefix(selection, model+" • ")
	}
	return false
}

func waitStartupOutput(ctx context.Context, terminal ports.AgentStartupTerminal, matches func(string) bool) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		output, err := terminal.Output(ctx)
		if err != nil {
			return "", err
		}
		if matches(output) {
			return output, nil
		}
		if err := startupPause(ctx, 100*time.Millisecond); err != nil {
			return "", err
		}
	}
}

func startupPause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
