package domain

import "regexp"

var prChangingCommand = regexp.MustCompile(`\b(?:gh\s+pr\s+(?:merge|close|ready|review|create|edit)|git\s+push)\b`)

// CommandMayChangePR reports whether a shell command an agent ran probably
// changed a pull request on the provider. It is only a hint for an early
// re-observe: a false positive costs one fetch, a miss waits for the next poll.
func CommandMayChangePR(command string) bool {
	return prChangingCommand.MatchString(command)
}
