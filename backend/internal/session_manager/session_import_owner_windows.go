//go:build windows

package sessionmanager

// ponytail: Windows relies on the provider refusing a second writer; add a
// LockFileEx probe if imported chats there report races.
func providerLockHeld(string) bool { return false }

func processAlive(int) bool { return false }
