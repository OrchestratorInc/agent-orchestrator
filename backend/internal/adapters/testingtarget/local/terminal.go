package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	processutil "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type terminalWindow struct {
	PID       int       `json:"pid"`
	ID        string    `json:"id"`
	TTY       string    `json:"tty"`
	Text      string    `json:"text"`
	Closed    bool      `json:"closed,omitempty"`
	StartedAt time.Time `json:"-"`
}

func (a *Adapter) terminalControl(ctx context.Context, s *launch, action string) (ports.TestingControlResult, error) {
	result := ports.TestingControlResult{}
	if action == "select_app" {
		result.Detail = "Selected owned app window"
		window := s.target
		result.Window = &window
		return result, nil
	}
	if a.ops.terminals == nil {
		return result, errors.New("terminal admission is unavailable")
	}
	windows, err := a.ops.terminals(ctx)
	if err != nil {
		return result, err
	}
	for i := range windows {
		windows[i].StartedAt, err = a.ops.startTime(windows[i].PID)
		if err != nil {
			return result, err
		}
	}
	switch action {
	case "arm_terminal":
		if s.terminal != nil {
			return result, errors.New("this attempt already owns a terminal")
		}
		s.terminalArm = windows
		s.terminalArmedAt = time.Now()
		result.Detail = "Armed terminal admission; existing windows are excluded for two minutes"
	case "admit_terminal":
		if s.terminal != nil {
			return result, errors.New("this attempt already owns a terminal")
		}
		if s.terminalArmedAt.IsZero() || time.Since(s.terminalArmedAt) > 2*time.Minute {
			return result, errors.New("arm terminal admission immediately before the target trigger")
		}
		var candidates []terminalWindow
		for _, w := range windows {
			if w.Closed {
				continue
			}
			existed := false
			for _, before := range s.terminalArm {
				if w.PID == before.PID && w.ID == before.ID && w.StartedAt.Equal(before.StartedAt) {
					existed = true
				}
			}
			if !existed {
				candidates = append(candidates, w)
			}
		}
		if len(candidates) != 1 {
			return result, errors.New("expected exactly one new Terminal.app window after the trigger")
		}
		w := candidates[0]
		w.Text, err = a.ops.terminalText(ctx, w)
		if err != nil {
			return result, err
		}
		// The target's own visible command must name both private state paths.
		// A new unrelated Terminal window is never enough for admission.
		command := strings.NewReplacer("\r", "", "\n", "").Replace(w.Text)
		if !strings.Contains(command, "AO_DATA_DIR="+s.target.DataDir) || !strings.Contains(command, "AO_RUN_FILE="+s.root+"/running.json") {
			return result, errors.New("new terminal does not show this target's private state command")
		}
		if err := a.captureTerminal(ctx, s, w); err != nil {
			return result, err
		}
		s.terminal = &w // retain cleanup ownership before desktop binding can fail
		window := s.target
		window.ID += "-terminal"
		window.ElectronPID, window.ElectronStartedAt, window.WindowID = w.PID, w.StartedAt, w.ID
		result.Window = &window
		result.Detail = "Admitted new target terminal by PID birth, exact window, tty and private state command"
	case "read_terminal":
		if s.terminal == nil {
			return result, errors.New("no owned terminal")
		}
		for _, w := range windows {
			if w.PID == s.terminal.PID && w.ID == s.terminal.ID && w.TTY == s.terminal.TTY && w.StartedAt.Equal(s.terminal.StartedAt) {
				if err := a.captureTerminal(ctx, s, w); err != nil {
					return result, err
				}
				result.Detail = "Read owned terminal text"
				result.Text, err = a.ops.terminalText(ctx, w)
				if err != nil {
					return result, err
				}
				if len(result.Text) > 65536 {
					result.Text = result.Text[len(result.Text)-65536:]
				}
				return result, nil
			}
		}
		return result, errors.New("owned terminal identity changed")
	default:
		return result, fmt.Errorf("unsupported target control: %s", action)
	}
	return result, nil
}

func (a *Adapter) captureTerminal(ctx context.Context, s *launch, w terminalWindow) error {
	if w.PID <= 0 || w.ID == "" || !strings.HasPrefix(w.TTY, "/dev/ttys") {
		return errors.New("invalid target terminal identity")
	}
	ids, err := a.ops.windows(ctx, w.PID)
	if err != nil {
		return err
	}
	found := false
	for _, id := range ids {
		if id == w.ID {
			found = true
		}
	}
	if !found {
		return errors.New("terminal window is absent from native PID inventory")
	}
	pids, err := a.ops.terminalProcesses(ctx, w.TTY)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		return errors.New("target terminal has no observable tty processes")
	}
	for _, pid := range pids {
		started, err := a.ops.startTime(pid)
		if err != nil {
			return err
		}
		if before, ok := s.owned[pid]; ok && !before.Equal(started) {
			return errors.New("terminal child PID was reused")
		}
		s.owned[pid] = started
	}
	started, err := a.ops.startTime(w.PID)
	if err != nil || !started.Equal(w.StartedAt) {
		return errors.Join(errors.New("Terminal.app PID changed"), err)
	}
	return nil
}

// Native terminal operations name only Terminal.app. Querying does not launch it.
const terminalSnapshotScript = `ObjC.import('AppKit');
var apps=$.NSRunningApplication.runningApplicationsWithBundleIdentifier('com.apple.Terminal');
var result=[];
if(Number(apps.count)===1){var pid=Number(apps.objectAtIndex(0).processIdentifier),app=Application('Terminal');
for(var w of app.windows()){var tabs=w.tabs();
if(tabs===null || tabs.length===0){
if(w.visible()!==false)throw Error('Terminal tab inventory unavailable');
result.push({pid:pid,id:String(w.id()),closed:true});continue;}
if(tabs.length!==1)continue;
result.push({pid:pid,id:String(w.id()),tty:tabs[0].tty()});}}
JSON.stringify(result)`

func nativeTerminals(ctx context.Context) ([]terminalWindow, error) {
	cmd := processutil.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", terminalSnapshotScript)
	cmd.Env = strippedEnv(os.Environ())
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var windows []terminalWindow
	if err := json.Unmarshal(data, &windows); err != nil {
		return nil, err
	}
	return windows, nil
}

func nativeTerminalProcesses(ctx context.Context, tty string) ([]int, error) {
	if !strings.HasPrefix(tty, "/dev/ttys") {
		return nil, errors.New("invalid terminal tty")
	}
	cmd := processutil.CommandContext(ctx, "/bin/ps", "-t", strings.TrimPrefix(tty, "/dev/"), "-o", "pid=")
	cmd.Env = strippedEnv(os.Environ())
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, field := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 {
			return nil, errors.New("invalid tty PID")
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func nativeCloseTerminal(ctx context.Context, w terminalWindow) error {
	id, err := strconv.Atoi(w.ID)
	if err != nil || id <= 0 {
		return errors.New("invalid owned terminal window")
	}
	// Closing one window never quits Terminal.app or closes another tab/window.
	script := fmt.Sprintf(`tell application "Terminal" to close (first window whose id is %d) saving no`, id)
	cmd := processutil.CommandContext(ctx, "/usr/bin/osascript", "-e", script)
	cmd.Env = strippedEnv(os.Environ())
	return cmd.Run()
}

func (a *Adapter) closeOwnedTerminal(ctx context.Context, s *launch) error {
	if s.terminal == nil {
		return nil
	}
	w := *s.terminal
	windows, err := a.ops.terminals(ctx)
	if err != nil {
		return err
	}
	for _, current := range windows {
		if current.PID != w.PID || current.ID != w.ID {
			continue
		}
		started, err := a.ops.startTime(w.PID)
		if err != nil || !started.Equal(w.StartedAt) {
			return errors.New("refusing to close changed terminal identity")
		}
		// Terminal can retain a hidden WindowServer window after its last tab
		// closes. Only positive tabless/hidden metadata proves that state.
		if current.Closed {
			return nil
		}
		if current.TTY != w.TTY {
			return errors.New("refusing to close changed terminal identity")
		}
		if err := a.ops.closeTerminal(ctx, w); err != nil {
			return err
		}
	}
	return waitControl(ctx, func() error {
		ids, err := a.ops.windows(ctx, w.PID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id == w.ID {
				windows, err := a.ops.terminals(ctx)
				if err != nil {
					return err
				}
				for _, current := range windows {
					if current.PID == w.PID && current.ID == w.ID && current.Closed {
						started, err := a.ops.startTime(w.PID)
						if err != nil || !started.Equal(w.StartedAt) {
							return errors.New("closed terminal PID identity changed")
						}
						return nil
					}
				}
				return errors.New("owned terminal window remains")
			}
		}
		return nil
	})
}

func nativeTerminalText(ctx context.Context, w terminalWindow) (string, error) {
	id, err := strconv.Atoi(w.ID)
	if err != nil || id <= 0 {
		return "", errors.New("invalid owned terminal window")
	}
	script := fmt.Sprintf(`var app=Application('Terminal'),w=app.windows.byId(%d),tabs=w.tabs(); if(tabs===null || tabs.length!==1 || tabs[0].tty()!==%q)throw Error('Terminal identity changed'); JSON.stringify(tabs[0].contents())`, id, w.TTY)
	cmd := processutil.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script)
	cmd.Env = strippedEnv(os.Environ())
	data, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return "", err
	}
	return text, nil
}
