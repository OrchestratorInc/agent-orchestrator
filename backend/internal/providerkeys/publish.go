package providerkeys

import "errors"

var ErrUserValue = errors.New("the user environment already sets this variable to another value")

var ErrAlreadySet = errors.New("the user environment already holds this value")

func alreadyUsers(current, value, previous string) bool {
	return current != "" && current == value && previous != value
}

func ownedValue(current, value, previous string) bool {
	return current == "" || current == value || (previous != "" && current == previous)
}
