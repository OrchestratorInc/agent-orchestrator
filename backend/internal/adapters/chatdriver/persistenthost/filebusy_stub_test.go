//go:build !windows

package persistenthost

import (
	"errors"
	"testing"
)

func TestIsTransientHostFileBusyNeverOnUnix(t *testing.T) {
	if isTransientHostFileBusy(errors.New("the process cannot access the file because it is being used by another process")) {
		t.Fatal("unix stub must not treat string errors as busy")
	}
}
