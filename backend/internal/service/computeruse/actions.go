package computeruse

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Cua Driver will act on whatever happens to be in front when an action names
// no window: screen-coordinate clicks, keys sent to the frontmost app, text
// typed into whichever field has focus. On a machine the user is working on,
// that lands in the wrong window or chat. AO refuses those calls before they
// reach the driver and says what to pass instead.
//
// These checks catch agent mistakes; they are not a sandbox. run_script's
// JavaScript is not inspected, and the driver itself stays on the machine.

// actionTools are the tools run_steps accepts as steps: every tool that sends
// input to an app.
var actionTools = map[string]bool{
	"click": true, "double_click": true, "right_click": true, "set_value": true,
	"type_text": true, "press_key": true, "hotkey": true, "scroll": true,
	"drag": true, "move_cursor": true,
}

// pixelArguments are the coordinate arguments of the pixel action forms.
// move_cursor is left out: in window scope it moves only the agent cursor
// overlay, never the user's pointer.
var pixelArguments = []string{"x", "y", "from_x", "from_y", "to_x", "to_y"}

// stepKeys are run_steps step fields that are not the step's tool.
var stepKeys = map[string]bool{"tool": true, "args": true, "wait_for": true, "expect": true, "timeout_ms": true}

// checkActions refuses an action call, or any step of a run_steps batch, that
// would act outside a named window or type into an unnamed field.
func checkActions(tool string, arguments map[string]any) error {
	if tool != "run_steps" {
		return checkAction(tool, arguments, nil, 0)
	}
	steps, _ := arguments["steps"].([]any)
	for index, raw := range steps {
		step, _ := raw.(map[string]any)
		stepTool, stepArguments := stepAction(step)
		if err := checkAction(stepTool, stepArguments, arguments, index+1); err != nil {
			return err
		}
	}
	return nil
}

// stepAction reads a step written {tool, args} or {<tool>: args}.
func stepAction(step map[string]any) (string, map[string]any) {
	if tool, ok := step["tool"].(string); ok {
		arguments, _ := step["args"].(map[string]any)
		return tool, arguments
	}
	for key, value := range step {
		if stepKeys[key] {
			continue
		}
		if arguments, ok := value.(map[string]any); ok {
			return key, arguments
		}
	}
	return "", nil
}

// checkAction checks one action. batch holds run_steps' own arguments, whose
// pid and window_id are the default window of steps that name none; step is
// the 1-based step number, or 0 for a direct call.
func checkAction(tool string, arguments, batch map[string]any, step int) error {
	if !actionTools[tool] {
		return nil
	}
	if arguments["scope"] == "desktop" || targetKind(arguments) == "desktop" {
		return actionRefused("COMPUTER_USE_DESKTOP_SCOPE_DISABLED", tool, step,
			"AO does not act on the whole screen, which may be in use. Pass the pid and window_id of the window you read with get_window_state instead of scope \"desktop\".")
	}
	if has(arguments, "element_token") {
		return nil
	}
	if tool != "move_cursor" && hasAny(arguments, pixelArguments...) && !namesWindow(arguments, batch) {
		return actionRefused("COMPUTER_USE_WINDOW_REQUIRED", tool, step,
			"Pixel coordinates need the pid and window_id of the window whose get_window_state screenshot you measured them on. Without them the driver would aim at raw screen coordinates.")
	}
	if tool == "type_text" && !hasAny(arguments, "x", "y", "role", "name") {
		return actionRefused("COMPUTER_USE_TEXT_TARGET_REQUIRED", tool, step,
			"type_text needs the element_token of the field to type into. Without one it types into whatever field has focus, which may be a different field or chat.")
	}
	return nil
}

// namesWindow reports whether a pixel action names the window it measured.
func namesWindow(arguments, batch map[string]any) bool {
	if targetKind(arguments) == "window" {
		target, _ := arguments["target"].(map[string]any)
		return has(target, "pid") && has(target, "window_id")
	}
	if has(arguments, "pid") || has(arguments, "window_id") {
		return has(arguments, "pid") && has(arguments, "window_id")
	}
	return has(batch, "pid") && has(batch, "window_id")
}

func targetKind(arguments map[string]any) string {
	target, _ := arguments["target"].(map[string]any)
	kind, _ := target["kind"].(string)
	return kind
}

func has(arguments map[string]any, key string) bool {
	value, ok := arguments[key]
	return ok && value != nil
}

func hasAny(arguments map[string]any, keys ...string) bool {
	for _, key := range keys {
		if has(arguments, key) {
			return true
		}
	}
	return false
}

func actionRefused(code, tool string, step int, message string) error {
	details := map[string]any{"tool": tool}
	if step > 0 {
		details["step"] = step
		message = fmt.Sprintf("run_steps step %d (%s): %s", step, tool, message)
	}
	return apierr.Invalid(code, message, details)
}

// driverRefusalHints add AO's advice to driver refusals whose own suggestion
// would be unsafe or misleading for an agent.
var driverRefusalHints = map[string]string{
	"desktop_scope_disabled": "AO does not act on the whole screen: pass the pid and window_id of the window you read with get_window_state, not scope \"desktop\".",
}

// missingArgument is the driver's plain-text answer to arguments its schema
// rejects before running, such as "Missing required integer field: pid".
var missingArgument = regexp.MustCompile(`(?m)^Missing required \w+ field: `)

// driverFailureKind is the API error kind for a failed driver call. A
// refusal or rejected arguments is the caller's request (400); an error with
// its own code, such as window_id_not_found or stale_element_token, is the
// screen not being in the state the call expected (409). Anything else, a
// crash or a timeout, means the driver is not working (503).
func driverFailureKind(stdout, stderr []byte) apierr.Kind {
	var answer struct {
		Status  string          `json:"status"`
		Code    string          `json:"code"`
		Refusal json.RawMessage `json:"refusal"`
	}
	if json.Unmarshal(stdout, &answer) == nil {
		switch {
		case answer.Status == "refused" || len(answer.Refusal) > 0:
			return apierr.KindInvalid
		case answer.Code != "":
			return apierr.KindConflict
		}
	}
	if missingArgument.MatchString(cleanText(string(stdout))) || missingArgument.MatchString(cleanText(string(stderr))) {
		return apierr.KindInvalid
	}
	return apierr.KindUnavailable
}

// refusalCode reads the code of a driver refusal:
// {"status":"refused","refusal":{"code":"...","message":"..."}}.
func refusalCode(stdout []byte) (code, message string) {
	var refused struct {
		Refusal struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"refusal"`
	}
	if json.Unmarshal(stdout, &refused) != nil {
		return "", ""
	}
	return refused.Refusal.Code, refused.Refusal.Message
}
