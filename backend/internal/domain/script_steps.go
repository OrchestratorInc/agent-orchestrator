package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	DefaultScriptStepTimeoutMinutes = 30
	MinScriptStepTimeoutMinutes     = 1
	MaxScriptStepTimeoutMinutes     = 120
)

const scriptTimeoutMarker = "# ao-timeout: "

var scriptTimeoutPattern = regexp.MustCompile(`^# ao-timeout: (\d{1,4})m\r?\n`)

func ParseTimedScriptCommand(step string) (minutes int, command string, ok bool) {
	match := scriptTimeoutPattern.FindStringSubmatch(step)
	if match == nil {
		return 0, step, false
	}
	minutes, _ = strconv.Atoi(match[1])
	return minutes, step[len(match[0]):], true
}

func TimedScriptCommand(minutes int, command string) string {
	if minutes <= 0 {
		return command
	}
	return scriptTimeoutMarker + strconv.Itoa(minutes) + "m\n" + command
}

func ScriptStepMinutes(step string) (string, int) {
	minutes, command, ok := ParseTimedScriptCommand(step)
	if !ok || minutes < MinScriptStepTimeoutMinutes {
		return command, 0
	}
	return command, min(minutes, MaxScriptStepTimeoutMinutes)
}

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
