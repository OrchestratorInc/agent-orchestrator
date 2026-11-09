package sessionmanager

import "errors"

// importNotice is a pending import failure written for the user. Its text is a
// fixed sentence: never a local path, command output, or configured value, so it
// may leave the daemon.
type importNotice string

func (n importNotice) Error() string { return string(n) }

// ImportFailureFallback is shown when an import fails for a reason that has no
// user-facing notice, such as a Git or filesystem error whose text names local
// paths. The caller logs the full error.
const ImportFailureFallback = "AO could not prepare this imported chat. See the AO daemon log for details."

// ImportFailureNotice returns the text of a pending import's failure that is
// safe to send to a client: the first notice the failure carries, or a failed
// Setup step, whose output is already redacted of the project's configured
// values, as a native spawn shows it. ok is false for any other failure.
func ImportFailureNotice(err error) (message string, ok bool) {
	var notice importNotice
	if errors.As(err, &notice) {
		return string(notice), true
	}
	var setup *SetupStepError
	if errors.As(err, &setup) {
		return setup.Error(), true
	}
	return "", false
}
