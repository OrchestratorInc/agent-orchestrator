package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// renderCuaCall writes a call's result as text the way Cua Driver's MCP
// server presents it; the screenshots go alongside as images. A window read
// is a header line, the tree (or the diff since the last read), and the
// snapshots it made stale; an action is its summary followed by AO's read of
// the window; run_steps is one line per step and its observation. Tools
// without such a layout report false and are sent as JSON.
func renderCuaCall(response cuaCallResponse) (string, bool) {
	result, ok := response.Result.(map[string]any)
	if !ok {
		return "", false
	}
	var out cuaText
	switch {
	case response.Tool == "get_window_state":
		out.window(result)
	case response.Tool == "run_steps":
		out.runSteps(result)
	case result["observation"] != nil:
		// The daemon attaches its read of the window to every action it
		// observed (or says why it could not), so the layout follows that.
		out.action(result)
	default:
		return "", false
	}
	if note := cuaString(result["ao_note"]); note != "" {
		out.line("note: " + note)
	}
	return out.String(), true
}

type cuaText struct {
	strings.Builder
}

func (t *cuaText) line(text string) {
	t.WriteString(text)
	t.WriteByte('\n')
}

func (t *cuaText) action(result map[string]any) {
	if summary := cuaString(result["summary"]); summary != "" {
		t.line(summary)
	} else {
		t.line(compactJSON(cuaWithout(result, "observation")))
	}
	observation, _ := result["observation"].(map[string]any)
	switch {
	case observation["ok"] == true:
		state, _ := observation["state"].(map[string]any)
		t.line("")
		t.window(state)
	case cuaString(observation["skipped"]) != "":
		t.line("observation skipped: " + cuaString(observation["skipped"]))
	case cuaString(observation["error"]) != "":
		t.line("observation failed: " + cuaString(observation["error"]))
	}
	if observation["ok"] != true {
		if hint := cuaString(result["hint"]); hint != "" {
			t.line(hint)
		}
	}
}

func (t *cuaText) runSteps(result map[string]any) {
	steps, _ := result["steps"].([]any)
	passed := 0
	for _, raw := range steps {
		if step, _ := raw.(map[string]any); step["ok"] == true {
			passed++
		}
	}
	total := cuaNumber(result["total"])
	if total == "" {
		total = strconv.Itoa(len(steps))
	}
	t.line(fmt.Sprintf("run_steps: %d/%s steps ok", passed, total))
	for position, raw := range steps {
		step, _ := raw.(map[string]any)
		status := "ok"
		if step["ok"] != true {
			status = "failed"
			if phase := cuaString(step["phase"]); phase != "" {
				status += " (" + phase + ")"
			}
		}
		message := cuaString(step["message"])
		if message == "" {
			message = cuaString(step["error"])
		}
		t.line(fmt.Sprintf("%d. %s %s: %s", position+1, cuaString(step["tool"]), status, message))
	}
	observation, _ := result["observation"].(map[string]any)
	if observation["ok"] == false {
		// The steps ran even though the read after them failed; the result is
		// not an error, which an agent would answer by running them again.
		reason := cuaString(observation["message"])
		if reason == "" {
			reason = cuaString(observation["error"])
		}
		t.line("observation failed: " + reason)
		t.line("The steps above ran, but AO could not read the window after them. Read it with get_window_state before acting again.")
		return
	}
	if summary := cuaString(observation["summary"]); summary != "" {
		t.line("observation: " + summary)
	}
	if state, ok := observation["state"].(map[string]any); ok {
		t.line("")
		t.window(state)
	}
}

// window prints a get_window_state result.
func (t *cuaText) window(state map[string]any) {
	header := []string{}
	for _, key := range []string{"window_id", "pid"} {
		if value := cuaNumber(state[key]); value != "" {
			header = append(header, key+"="+value)
		}
	}
	if width, height := cuaNumber(state["screenshot_width"]), cuaNumber(state["screenshot_height"]); width != "" && height != "" {
		header = append(header, "size="+width+"x"+height)
	}
	if elements := cuaNumber(state["element_count"]); elements != "" {
		header = append(header, "elements="+elements)
	}
	snapshot := cuaString(state["snapshot_id"])
	if snapshot != "" {
		header = append(header, "snapshot_id="+snapshot)
	}
	if len(header) > 0 {
		t.line(strings.Join(header, " "))
	}
	if reason := cuaString(state["degraded_reason"]); reason != "" {
		t.line("degraded: " + reason)
	}
	if warning := cuaString(state["render_warning"]); warning != "" {
		t.line("warning: " + warning)
	}
	if diff := cuaString(state["tree_diff"]); diff != "" {
		t.line("")
		t.line(diff)
	} else if tree := cuaString(state["tree_markdown"]); tree != "" {
		if snapshot != "" {
			t.line(fmt.Sprintf("element_token for row [N] = %s:N (row [1] is %q)", snapshot, snapshot+":1"))
		}
		t.line("")
		t.line(tree)
	} else if elements, _ := state["elements"].([]any); len(elements) > 0 {
		// tree_format "elements": each element carries its own token.
		t.line("")
		for _, element := range elements {
			t.line(compactJSON(element))
		}
	}
	if state["truncated"] == true {
		t.line(fmt.Sprintf("Tree truncated: %s of %s elements shown; pass query, or a higher max_elements.", cuaNumber(state["returned_element_count"]), cuaNumber(state["total_element_count"])))
	}
	if stale := cuaStrings(state["invalidated_snapshot_ids"]); len(stale) > 0 {
		t.line(fmt.Sprintf("Invalidated snapshots %s: their element_tokens are stale.", strings.Join(stale, ", ")))
	}
}

func cuaString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

// cuaNumber prints a JSON number without an exponent: window ids and pids
// are large enough that %v would print 1.234567e+06.
func cuaNumber(value any) string {
	number, ok := value.(float64)
	if !ok {
		return ""
	}
	return strconv.FormatFloat(number, 'f', -1, 64)
}

func cuaStrings(value any) []string {
	items, _ := value.([]any)
	texts := make([]string, 0, len(items))
	for _, item := range items {
		if text := cuaString(item); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

func cuaWithout(result map[string]any, key string) map[string]any {
	rest := make(map[string]any, len(result))
	for name, value := range result {
		if name != key {
			rest[name] = value
		}
	}
	return rest
}

func compactJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
