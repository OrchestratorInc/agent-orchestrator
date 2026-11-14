package providerkeys

import (
	"os"
	"slices"
	"sync"
)

var envMu sync.Mutex

var inherited = map[string]inheritedValue{}

type inheritedValue struct {
	value string
	set   bool
}

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

func Inherited(name string) (value string, set, exported bool) {
	envMu.Lock()
	defer envMu.Unlock()
	original, ok := inherited[name]
	return original.value, original.set, ok
}
