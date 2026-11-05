package computeruse

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

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

// stepAliases are the step tool names run_steps runs as another tool, as Cua
// Driver's batch planner rewrites them. AO checks a step as the tool it runs.
var stepAliases = map[string]string{
	"press": "press_key", "key": "press_key", "keypress": "press_key",
	"type": "type_text", "set": "set_value",
	"hover": "move_cursor", "mouse_move": "move_cursor",
}

// keyStepNames are key names run_steps runs as a press_key of that key when
// a step uses one as its tool ({"tool": "down"}).
var keyStepNames = map[string]bool{
	"return": true, "enter": true, "tab": true, "escape": true, "esc": true,
	"space": true, "backspace": true, "up": true, "down": true, "left": true,
	"right": true, "home": true, "end": true, "pageup": true, "pagedown": true,
}

// readSteps are the window reads run_steps skips between actions or takes as
// its end-of-batch read; the driver also reads any step named for a screenshot.
var readSteps = map[string]bool{
	"get_window_state": true, "get_desktop_state": true, "get_accessibility_tree": true,
	"get_browser_state": true, "list_windows": true, "verify_state": true, "zoom": true,
}

// pressTargets are the arguments that make a press step, with no key, a
// click of that element.
var pressTargets = []string{"element_token", "element_index", "role", "name", "label", "x"}

// checkActions refuses an action call, or any step of a run_steps batch, that
// would act outside a named window or type into an unnamed field.
func checkActions(tool string, arguments map[string]any) error {
	if tool != "run_steps" {
		return checkAction(tool, arguments, nil, 0)
	}
	if foreground(arguments) || foregroundFallback(arguments) {
		return actionRefused("COMPUTER_USE_FOREGROUND_DISABLED", tool, 0, foregroundRefusal)
	}
	steps, _ := arguments["steps"].([]any)
	for index, raw := range steps {
		step, _ := raw.(map[string]any)
		name, stepArguments := stepAction(step)
		stepTool, known := batchTool(name, stepArguments)
		if !known {
			return actionRefused("COMPUTER_USE_STEP_UNSUPPORTED", name, index+1,
				"AO checks every step before the driver runs it and does not know this tool. Use one of: "+batchableList+".")
		}
		if err := checkAction(stepTool, stepArguments, arguments, index+1); err != nil {
			return err
		}
	}
	return nil
}

// batchableList names the action tools a run_steps step may use.
const batchableList = "click, double_click, right_click, set_value, type_text, press_key, hotkey, scroll, drag, move_cursor"

// batchTool is the tool a run_steps step runs as, after the driver's
// rewrites, and whether AO knows it. A step with no tool only waits or checks.
func batchTool(name string, arguments map[string]any) (string, bool) {
	lower := strings.ToLower(name)
	switch {
	case name == "" || actionTools[name] || readSteps[name]:
		return name, true
	case strings.Contains(lower, "screenshot"):
		return "screenshot", true
	case name == "press" && !has(arguments, "key") && hasAny(arguments, pressTargets...):
		return "click", true
	case stepAliases[name] != "":
		return stepAliases[name], true
	case lower == "triple_click":
		return "click", true
	case keyStepNames[lower]:
		return "press_key", true
	}
	return name, false
}

// foregroundFallback reports whether run_steps may retry a refused step with
// foreground input. Only an absent field or boolean false is let through: the
// driver turns a string such as "true" or " TRUE " into true before running.
func foregroundFallback(arguments map[string]any) bool {
	value, set := arguments["foreground_fallback"]
	return set && value != nil && value != false
}

// foreground reports whether arguments ask for foreground delivery in any
// form the driver accepts: delivery_mode in any letter case, or the older
// dispatch field it still reads as delivery_mode.
func foreground(arguments map[string]any) bool {
	for _, key := range []string{"delivery_mode", "dispatch"} {
		if mode, ok := arguments[key].(string); ok && strings.EqualFold(mode, "foreground") {
			return true
		}
	}
	return false
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
	if foreground(arguments) {
		return actionRefused("COMPUTER_USE_FOREGROUND_DISABLED", tool, step, foregroundRefusal)
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

// foregroundRefusal explains why AO refuses foreground delivery: the driver
// raises the window and sends real input to whatever has focus, which may be
// the window the user is typing in.
const foregroundRefusal = "AO does not send foreground input: the driver would raise the window and send real keys and clicks to whatever has focus, which may be the user's own window. Act in the background; if the window is not rendering, ask the user, then bring it to the front with bring_to_front."

// foregroundAdvice replaces the driver's own suggestion to retry an action
// with delivery_mode "foreground", which AO refuses.
const foregroundAdvice = "AO refuses delivery_mode \"foreground\". If the action did not land because the window is not rendering, ask the user, then bring it to the front with bring_to_front and act again."

// adviceKeys are the result fields where the driver gives advice. The window's
// tree is left out: an app's own labels can mention "foreground".
var adviceKeys = map[string]bool{"hint": true, "suggestion": true, "escalation": true, "message": true, "reason": true, "detail": true, "recommended": true}

// webTextAdvice follows a type_text the driver refused or could not verify. A
// text box in a web page, such as a browser's post composer, takes neither
// background keys nor foreground ones (AO refuses those), and set_value fills
// it without the input events the page listens for.
const webTextAdvice = "If this is a text box in a web page or web-based app, it may not take typed text in the background. Fill it with a run_steps set_value step and read the field to check it. The page may not register text filled this way, so before anything is sent or saved, tell the user and ask them to type and delete one character in the field."

// needsWebTextAdvice reports whether a failed call is a type_text the driver
// refused for the field itself, not for its arguments.
func needsWebTextAdvice(tool string, stdout, stderr []byte) bool {
	if tool != "type_text" || driverFailureKind(stdout, stderr) != apierr.KindInvalid {
		return false
	}
	code, _ := refusalCode(stdout)
	return code != "" && code != "invalid_arguments"
}

// noteWebTextAdvice adds webTextAdvice to a type_text result the driver
// reports as unverifiable: the field shows the text, but a web page may not
// have seen it.
func noteWebTextAdvice(tool string, result any) {
	output, ok := result.(map[string]any)
	if !ok || tool != "type_text" || output["effect"] != "unverifiable" {
		return
	}
	if note, _ := output["ao_note"].(string); note != "" {
		output["ao_note"] = note + "\n" + webTextAdvice
		return
	}
	output["ao_note"] = webTextAdvice
}

// noteForegroundAdvice adds ao_note to a result whose driver advice suggests
// foreground delivery.
func noteForegroundAdvice(result any) {
	output, ok := result.(map[string]any)
	if ok && suggestsForeground(output, false) {
		output["ao_note"] = foregroundAdvice
	}
}

func suggestsForeground(value any, advice bool) bool {
	switch v := value.(type) {
	case string:
		return advice && strings.Contains(strings.ToLower(v), "foreground")
	case map[string]any:
		for key, item := range v {
			if key == "tree_markdown" || key == "tree_diff" || key == "elements" {
				continue
			}
			if suggestsForeground(item, advice || adviceKeys[key]) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if suggestsForeground(item, advice) {
				return true
			}
		}
	}
	return false
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

// windowNotFound is the Windows driver's plain-text answer for a window that
// does not exist, where macOS answers {"code":"window_id_not_found"}.
var windowNotFound = regexp.MustCompile(`(?m)^No window with window_id \d+ exists`)

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
	out, errOut := cleanText(string(stdout)), cleanText(string(stderr))
	switch {
	case missingArgument.MatchString(out) || missingArgument.MatchString(errOut):
		return apierr.KindInvalid
	case windowNotFound.MatchString(out) || windowNotFound.MatchString(errOut):
		return apierr.KindConflict
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
