package providerkeys

import (
	"os"
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
func ExportForProbes(name, value string) error {
	envMu.Lock()
	defer envMu.Unlock()
	if _, ok := inherited[name]; !ok {
		previous, set := os.LookupEnv(name)
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
