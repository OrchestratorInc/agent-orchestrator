package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Each Setup (PostCreate) or Cleanup (PreRemove) step may set its own time
// limit, in whole minutes, from MinScriptStepTimeoutMinutes to
// MaxScriptStepTimeoutMinutes. A Setup step without one runs for at most
// DefaultScriptStepTimeoutMinutes; a Cleanup step without one keeps running
// without a limit, as Cleanup always has.
//
// The limit is stored with the step, as the Cloud control plane stores it
// (the private control plane's internal/domain.TimedScriptCommand): the step's first line is the
// shell comment "# ao-timeout: <N>m". So a step keeps its limit through any
// reordering, and the Settings editor reads and writes local and Cloud steps
// alike.
const (
	DefaultScriptStepTimeoutMinutes = 30
	MinScriptStepTimeoutMinutes     = 1
	MaxScriptStepTimeoutMinutes     = 120
)

const scriptTimeoutMarker = "# ao-timeout: "

var scriptTimeoutPattern = regexp.MustCompile(`^# ao-timeout: (\d{1,4})m\r?\n`)

// ParseTimedScriptCommand splits a step into its time limit in minutes (as
// written, so possibly out of range) and its command without the marker line;
// ok is false for a step without one.
func ParseTimedScriptCommand(step string) (minutes int, command string, ok bool) {
	match := scriptTimeoutPattern.FindStringSubmatch(step)
	if match == nil {
		return 0, step, false
	}
	minutes, _ = strconv.Atoi(match[1])
	return minutes, step[len(match[0]):], true
}

// TimedScriptCommand is a step with its own time limit: command after the
// marker line. A step with no limit (0) is command unchanged.
func TimedScriptCommand(minutes int, command string) string {
	if minutes <= 0 {
		return command
	}
	return scriptTimeoutMarker + strconv.Itoa(minutes) + "m\n" + command
}

// ScriptStepMinutes is the command a step runs and the time limit it sets in
// minutes, capped at MaxScriptStepTimeoutMinutes; 0 when it sets none.
func ScriptStepMinutes(step string) (string, int) {
	minutes, command, ok := ParseTimedScriptCommand(step)
	if !ok || minutes < MinScriptStepTimeoutMinutes {
		return command, 0
	}
	return command, min(minutes, MaxScriptStepTimeoutMinutes)
}

// validateScriptSteps checks each step's time limit, when it sets one.
func validateScriptSteps(name string, steps []string) error {
	for index, step := range steps {
		minutes, command, ok := ParseTimedScriptCommand(step)
		if !ok {
			continue
		}
		if minutes < MinScriptStepTimeoutMinutes || minutes > MaxScriptStepTimeoutMinutes {
			return fmt.Errorf("%s step %d: the time limit must be a whole number of minutes from %d to %d",
				name, index+1, MinScriptStepTimeoutMinutes, MaxScriptStepTimeoutMinutes)
		}
		if strings.TrimSpace(command) == "" {
			return fmt.Errorf("%s step %d: the command is empty", name, index+1)
		}
	}
	return nil
}
