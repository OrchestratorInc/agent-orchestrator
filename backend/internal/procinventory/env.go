package procinventory

import "os"

// envDisableFlag is the AO_* behavioral flag (#93 convention: behavioral
// toggles are environment variables read at the point of behavior, not UI
// settings) that turns the process-footprint surface off at runtime: the
// status bar hides and polling stops. The feature is on without it.
const envDisableFlag = "AO_NO_PROCINVENTORY"

// EnvDisabled reports whether envDisableFlag turns the surface off. Only "1"
// and "true" count as off; anything else — unset, empty, any other value —
// keeps the default-on behavior.
func EnvDisabled() bool {
	switch os.Getenv(envDisableFlag) {
	case "1", "true":
		return true
	default:
		return false
	}
}
