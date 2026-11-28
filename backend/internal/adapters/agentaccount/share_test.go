package agentaccount

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
)

// Only Windows refusing a symlink for want of Developer Mode asks the user to
// turn it on; any other failed link, or the same code elsewhere, does not.
func TestNeedsDeveloperModeOnlyForWindowsPrivilegeError(t *testing.T) {
	refused := &os.LinkError{Op: "symlink", Old: "shared", New: "link", Err: errPrivilegeNotHeld}
	if got, want := needsDeveloperMode(refused), runtime.GOOS == "windows"; got != want {
		t.Fatalf("needsDeveloperMode(privilege not held) = %v, want %v", got, want)
	}
	for _, err := range []error{
		&os.LinkError{Op: "symlink", Old: "shared", New: "link", Err: os.ErrPermission},
		&os.LinkError{Op: "symlink", Old: "shared", New: "link", Err: syscall.Errno(5)},
		errors.New("disk full"),
	} {
		if needsDeveloperMode(err) {
			t.Fatalf("needsDeveloperMode(%v) = true, want false", err)
		}
	}
}
