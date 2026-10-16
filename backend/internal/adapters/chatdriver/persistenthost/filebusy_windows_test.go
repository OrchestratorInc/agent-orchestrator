//go:build windows

package persistenthost

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestIsTransientHostFileBusy(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "sharing violation", err: windows.ERROR_SHARING_VIOLATION, want: true},
		{name: "lock violation", err: windows.ERROR_LOCK_VIOLATION, want: true},
		{name: "access denied", err: windows.ERROR_ACCESS_DENIED, want: true},
		{name: "path error wrapping sharing", err: &os.PathError{Op: "open", Path: `C:\x`, Err: windows.ERROR_SHARING_VIOLATION}, want: true},
		{name: "not exist", err: os.ErrNotExist, want: false},
		{name: "other", err: errors.New("boom"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientHostFileBusy(tt.err); got != tt.want {
				t.Fatalf("isTransientHostFileBusy(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
