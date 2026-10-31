package sessionmanager

import "errors"

// importNotice is a pending import failure written for the user. Its text is a
// fixed sentence: never a local path, command output, or configured value, so it
// is the only part of an import failure that may leave the daemon.
type importNotice string

func (n importNotice) Error() string { return string(n) }

// importFailureFallback is shown when an import fails for a reason that has no
// user-facing notice, such as a Git or filesystem error whose text names local
// paths. The full error stays in the daemon log.
const importFailureFallback = "AO could not prepare this imported chat. See the AO daemon log for details."

// ImportFailureMessage returns the text of a pending import's failure that is
// safe to send to a client: the first notice the failure carries, or a
// generic sentence when it carries none.
func ImportFailureMessage(err error) string {
	var notice importNotice
	if errors.As(err, &notice) {
		return string(notice)
	}
	return importFailureFallback
}
