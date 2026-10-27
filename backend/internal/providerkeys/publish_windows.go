package providerkeys

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Publish sets the current user's persistent Windows environment. Existing
// processes keep their own environment; Explorer receives the change notice
// so processes it launches after this call can inherit the new value.
func Publish(ctx context.Context, name, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validName(name) || !validValue(value) || value == "" {
		return errors.New("invalid provider credential")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	if err := key.SetStringValue(name, value); err != nil {
		_ = key.Close()
		return err
	}
	if err := key.Close(); err != nil {
		return err
	}
	message, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	var result uintptr
	// #nosec G103 -- SendMessageTimeoutW requires stable UTF-16 and result pointers for this synchronous call.
	ret, _, callErr := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(
		0xffff, 0x001a, 0, uintptr(unsafe.Pointer(message)), 0x0002, 5000, uintptr(unsafe.Pointer(&result)),
	)
	if ret == 0 {
		if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return callErr
		}
		return errors.New("user environment change could not be broadcast")
	}
	return nil
}
