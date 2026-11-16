package browserchrome

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExtensionIDMatchesManifestKey(t *testing.T) {
	// Chrome's ID for ExtensionPublicKey, computed independently with
	// openssl + shasum when the key was generated.
	if ExtensionID != "eefgahhblbmjpccfcnpkhkadgcccnibg" {
		t.Fatalf("ExtensionID = %q", ExtensionID)
	}
	if AllowedOrigin() != "chrome-extension://eefgahhblbmjpccfcnpkhkadgcccnibg/" {
		t.Fatalf("AllowedOrigin = %q", AllowedOrigin())
	}
	if _, err := ExtensionIDFromKey("not base64!"); err == nil {
		t.Fatal("invalid key accepted")
	}
}

func TestEndpointRoundTripIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser-chrome", "endpoint.json")
	want := Endpoint{Address: "/tmp/x.sock", Token: "secret", PID: 42, Protocol: 2}
	if err := WriteEndpoint(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEndpoint(path)
	if err != nil || got != want {
		t.Fatalf("ReadEndpoint = %#v, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("endpoint mode = %v", info.Mode().Perm())
		}
	}
	RemoveEndpoint(path, 7)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("endpoint owned by another daemon was removed")
	}
	RemoveEndpoint(path, 42)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("endpoint was not removed by its owner")
	}
	if err := os.WriteFile(path, []byte(`{"address":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEndpoint(path); err == nil {
		t.Fatal("incomplete endpoint accepted")
	}
}

type fakeRegistry struct{ values map[string]string }

func (r *fakeRegistry) Register(name, path string) error { r.values[name] = path; return nil }
func (r *fakeRegistry) Unregister(name string) error     { delete(r.values, name); return nil }
func (r *fakeRegistry) Lookup(name string) (string, error) {
	if value, ok := r.values[name]; ok {
		return value, nil
	}
	return "", os.ErrNotExist
}

func testInstaller(t *testing.T) (Installer, string, string) {
	t.Helper()
	root := t.TempDir()
	chromeDir := filepath.Join(root, "Chrome")
	if err := os.MkdirAll(chromeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "bundle")
	if err := os.MkdirAll(filepath.Join(source, "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), []byte(`{"version":"1.4.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "pages", "consent.html"), []byte("<p>ok</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "bin", "ao")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Installer{
		Paths:           NewPaths(filepath.Join(root, "data")),
		Executable:      executable,
		ExtensionSource: source,
		Browsers: []Browser{
			{Name: "Google Chrome", UserDataDir: chromeDir},
			{Name: "Google Chrome Beta", UserDataDir: filepath.Join(root, "Chrome Beta")},
		},
	}, chromeDir, root
}

func TestSetupRegistersInstalledChannelsOnly(t *testing.T) {
	installer, chromeDir, root := testInstaller(t)
	if installer.Status().Enabled {
		t.Fatal("enabled before setup")
	}
	status, err := installer.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || !status.ExtensionBundled || status.ExtensionVersion != "1.4.0" {
		t.Fatalf("status = %#v", status)
	}
	if len(status.Channels) != 2 || !status.Channels[0].Registered || status.Channels[1].Registered || status.Channels[1].Available {
		t.Fatalf("channels = %#v", status.Channels)
	}
	if _, err := os.Stat(filepath.Join(root, "Chrome Beta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("setup created an unused channel's directory")
	}

	var manifest hostManifest
	data, err := os.ReadFile(filepath.Join(chromeDir, "NativeMessagingHosts", HostName+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != HostName || manifest.Type != "stdio" || manifest.Path != installer.HostPath() ||
		len(manifest.AllowedOrigins) != 1 || manifest.AllowedOrigins[0] != AllowedOrigin() {
		t.Fatalf("manifest = %#v", manifest)
	}
	wrapper, err := os.ReadFile(installer.HostPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wrapper), installer.Executable) || !strings.Contains(string(wrapper), "browser-host --endpoint") ||
		!strings.Contains(string(wrapper), installer.Paths.Endpoint()) {
		t.Fatalf("wrapper = %s", wrapper)
	}
	if !strings.HasPrefix(installer.HostPath(), installer.Paths.Root) {
		t.Fatal("host wrapper is outside the AO data directory")
	}
	if _, err := os.Stat(filepath.Join(installer.Paths.Extension(), "pages", "consent.html")); err != nil {
		t.Fatalf("extension not installed: %v", err)
	}

	if err := installer.Refresh(); err != nil {
		t.Fatal(err)
	}

	status, err = installer.Remove()
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled {
		t.Fatal("still enabled after remove")
	}
	for _, path := range []string{installer.HostPath(), installer.Paths.Extension(), filepath.Join(chromeDir, "NativeMessagingHosts", HostName+".json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived remove", path)
		}
	}
	if err := installer.Refresh(); err != nil {
		t.Fatal(err)
	}
	if installer.Status().Enabled {
		t.Fatal("refresh re-enabled a removed registration")
	}
}

func TestRemoveKeepsForeignManifest(t *testing.T) {
	installer, chromeDir, _ := testInstaller(t)
	foreign := filepath.Join(chromeDir, "NativeMessagingHosts", HostName+".json")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte(`{"name":"dev.ao.browser_bridge","path":"/elsewhere/host"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if installer.Status().Enabled {
		t.Fatal("a manifest for another data directory counted as enabled")
	}
	if _, err := installer.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("remove deleted a manifest AO did not write for this data directory")
	}
}

func TestSetupWithoutChromeFails(t *testing.T) {
	installer, _, root := testInstaller(t)
	installer.Browsers = []Browser{{Name: "Google Chrome", UserDataDir: filepath.Join(root, "missing")}}
	if _, err := installer.Setup(); !errors.Is(err, ErrChromeNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupUsesRegistryWhenPresent(t *testing.T) {
	installer, _, _ := testInstaller(t)
	reg := &fakeRegistry{values: map[string]string{}}
	installer.registry = reg
	status, err := installer.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || reg.values[HostName] != installer.Paths.HostManifestCopy() {
		t.Fatalf("status=%#v registry=%#v", status, reg.values)
	}
	if _, err := installer.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.values[HostName]; ok {
		t.Fatal("registry value survived remove")
	}
}

func TestRemoveKeepsAnotherInstallsRegistryEntry(t *testing.T) {
	reg := &fakeRegistry{values: map[string]string{}}
	first, _, _ := testInstaller(t)
	first.registry = reg
	second, _, _ := testInstaller(t) // another AO data directory
	second.registry = reg
	if _, err := first.Setup(); err != nil {
		t.Fatal(err)
	}
	if second.Status().Enabled {
		t.Fatal("the other install's registration counted as this one's")
	}
	if _, err := second.Remove(); err != nil {
		t.Fatal(err)
	}
	if reg.values[HostName] != first.Paths.HostManifestCopy() {
		t.Fatalf("removing one install unregistered another: %#v", reg.values)
	}
	if !first.Status().Enabled {
		t.Fatal("first install lost its registration")
	}
	if _, err := first.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.values[HostName]; ok {
		t.Fatal("owner's remove left the registration behind")
	}
}

func TestDefaultExtensionSourceSitsBesideDaemonDir(t *testing.T) {
	got := DefaultExtensionSource(filepath.Join("/Applications", "AO.app", "Contents", "Resources", "daemon", "ao"))
	want := filepath.Join("/Applications", "AO.app", "Contents", "Resources", "browser-extension")
	if got != want {
		t.Fatalf("DefaultExtensionSource = %q, want %q", got, want)
	}
}
