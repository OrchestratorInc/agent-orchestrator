package providerkeys

import (
	"os"
	"slices"
	"sync"
)

// The daemon exports stored keys to its own environment so authentication
// probes and model discovery see them, and runtimes start from that
// environment. inherited remembers what each variable held before AO first
// exported it in this process, so a value the user exported before starting
// AO is never lost: sessions that must not receive AO's copy get the user's
// value back, and removing the key restores it.
var envMu sync.Mutex

var inherited = map[string]inheritedValue{}

type inheritedValue struct {
	value string
	set   bool
}

// ExportForProbes sets name to value in this process's environment, first
// recording the value it inherited.
//
// published lists values AO itself published for name to the OS user
// environment (the stored value of a key whose "also set for apps outside
// AO" marker is on). After a restart AO inherits its own publication from
// that environment; an inherited value equal to one of them is AO's, not the
// user's, so it is recorded as unset and never handed back as theirs.
func ExportForProbes(name, value string, published ...string) error {
	envMu.Lock()
	defer envMu.Unlock()
	if _, ok := inherited[name]; !ok {
		previous, set := os.LookupEnv(name)
		if set && slices.Contains(published, previous) {
			previous, set = "", false
		}
		inherited[name] = inheritedValue{value: previous, set: set}
	}
	return os.Setenv(name, value)
}

// RestoreInherited puts back the value name held before AO exported it, or
// unsets it when it had none. A variable AO never exported is left alone.
func RestoreInherited(name string) error {
	envMu.Lock()
	defer envMu.Unlock()
	original, ok := inherited[name]
	if !ok {
		return nil
	}
	delete(inherited, name)
	if original.set {
		return os.Setenv(name, original.value)
	}
	return os.Unsetenv(name)
}

// Inherited reports the value name held before AO exported it into this
// process's environment. exported is false when AO has not exported name.
func Inherited(name string) (value string, set, exported bool) {
	envMu.Lock()
	defer envMu.Unlock()
	original, ok := inherited[name]
	return original.value, original.set, ok
}
