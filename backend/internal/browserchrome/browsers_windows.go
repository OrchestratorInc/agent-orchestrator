//go:build windows

package browserchrome

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// Windows Chrome reads native hosts from the registry, so the manifest stays
// under ~/.ao and only a registry value points at it.
func defaultBrowsers(string) []Browser { return []Browser{} }

const hostKeyPrefix = `Software\Google\Chrome\NativeMessagingHosts\`

type windowsRegistry struct{}

func platformRegistry() hostRegistry { return windowsRegistry{} }

func (windowsRegistry) Register(name, manifestPath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, hostKeyPrefix+name, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = key.Close() }()
	return key.SetStringValue("", manifestPath)
}

func (windowsRegistry) Unregister(name string) error {
	err := registry.DeleteKey(registry.CURRENT_USER, hostKeyPrefix+name)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func (windowsRegistry) Lookup(name string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, hostKeyPrefix+name, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer func() { _ = key.Close() }()
	value, _, err := key.GetStringValue("")
	return value, err
}
