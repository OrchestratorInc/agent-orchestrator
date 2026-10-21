package daemon

import "os"

// stopTeardownEnvFlag is the AO_* behavioral flag (#93 convention: behavioral
// toggles are environment variables read at the point of behavior, not UI
// settings) behind the `ao stop` child-process teardown (#112). It ships dark
// at the owner's interim call on issue #35: until the daemon is started with
// AO_STOP_TEARDOWN=1|true, a teardown-marked shutdown logs and leaves session
// processes running, restoring the pre-#112 stop behavior. Flip the default
// back to on once the owner signs off on the behavior.
const stopTeardownEnvFlag = "AO_STOP_TEARDOWN"

// stopTeardownEnabled reports whether the daemon may act on the `ao stop`
// teardown marker. Only "1" and "true" opt in; anything else — unset, empty,
// any other value — keeps the teardown off.
func stopTeardownEnabled() bool {
	switch os.Getenv(stopTeardownEnvFlag) {
	case "1", "true":
		return true
	default:
		return false
	}
}
