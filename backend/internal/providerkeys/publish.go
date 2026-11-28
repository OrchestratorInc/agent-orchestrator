package providerkeys

import "errors"

// ErrUserValue reports that the OS user environment already holds a value
// for the variable that AO did not put there. Publish leaves it unchanged.
var ErrUserValue = errors.New("the user environment already sets this variable to another value")

// ErrAlreadySet reports that the OS user environment already holds exactly
// value, set by the user rather than by AO. The key is shared, but the value
// is the user's: AO must never remove it.
var ErrAlreadySet = errors.New("the user environment already holds this value")

// alreadyUsers reports that current is value but was not put there by AO:
// previous, AO's earlier copy, is something else.
func alreadyUsers(current, value, previous string) bool {
	return current != "" && current == value && previous != value
}

// ownedValue reports whether current, the variable's value in the OS user
// environment, may be replaced: it is unset, already value, or previous,
// the copy AO published before.
func ownedValue(current, value, previous string) bool {
	return current == "" || current == value || (previous != "" && current == previous)
}
