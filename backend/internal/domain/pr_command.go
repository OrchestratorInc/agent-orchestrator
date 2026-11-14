package domain

import "regexp"

var prChangingCommand = regexp.MustCompile(`\b(?:gh\s+pr\s+(?:merge|close|ready|review|create|edit)|git\s+push)\b`)

func CommandMayChangePR(command string) bool {
	return prChangingCommand.MatchString(command)
}
