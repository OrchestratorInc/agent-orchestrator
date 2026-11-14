package sessionmanager

import "errors"

type importNotice string

func (n importNotice) Error() string { return string(n) }

const ImportFailureFallback = "Orchestrator could not prepare this imported chat. See the Orchestrator daemon log for details."

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
