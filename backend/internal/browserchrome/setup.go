package browserchrome

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Browser is one Chrome channel AO can register its native host with.
type Browser struct {
	// Name is the user-facing channel name, for example "Google Chrome".
	Name string
	// UserDataDir is Chrome's per-user directory. Registration is only
	// written for channels whose directory exists (the channel is in use).
	UserDataDir string
}

// ManifestPath is where this channel looks for AO's host manifest.
func (b Browser) ManifestPath() string {
	return filepath.Join(b.UserDataDir, "NativeMessagingHosts", HostName+".json")
}

// Installer writes and removes Chrome-mode registration.
type Installer struct {
	Paths Paths
	// Executable is the ao binary the host wrapper runs. The daemon pins
	// connecting hosts to this same file.
	Executable string
	// ExtensionSource is the bundled unpacked extension shipped with AO.
	ExtensionSource string
	// Browsers lists the Chrome channels to register with. Defaults to the
	// platform's channels under the user's home directory.
	Browsers []Browser
	// registry abstracts the Windows registry for tests; nil uses the
	// platform implementation.
	registry hostRegistry
}

// ChannelStatus reports registration for one Chrome channel.
type ChannelStatus struct {
	Name         string `json:"name"`
	Available    bool   `json:"available"`
	Registered   bool   `json:"registered"`
	ManifestPath string `json:"manifestPath"`
}

// Status is Chrome mode's installation state.
type Status struct {
	Enabled          bool            `json:"enabled"`
	ExtensionID      string          `json:"extensionId"`
	ExtensionPath    string          `json:"extensionPath"`
	ExtensionBundled bool            `json:"extensionBundled"`
	ExtensionVersion string          `json:"extensionVersion,omitempty"`
	HostPath         string          `json:"hostPath"`
	Channels         []ChannelStatus `json:"channels"`
}

type hostManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

// ErrChromeNotFound means no Chrome channel's user data directory exists.
var ErrChromeNotFound = errors.New("google Chrome user data was not found; open Chrome once, then retry")

// DefaultExtensionSource locates the extension bundled next to the daemon:
// <resources>/daemon/ao → <resources>/browser-extension.
func DefaultExtensionSource(executable string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(executable)), "browser-extension")
}

func (i Installer) browsers() []Browser {
	if i.Browsers != nil {
		return i.Browsers
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return defaultBrowsers(home)
}

func (i Installer) reg() hostRegistry {
	if i.registry != nil {
		return i.registry
	}
	return platformRegistry()
}

// HostPath is the wrapper Chrome launches.
func (i Installer) HostPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(i.Paths.Root, "ao-browser-host.bat")
	}
	return filepath.Join(i.Paths.Root, "ao-browser-host")
}

// Setup installs the host wrapper and extension under the data directory and
// registers the host with every Chrome channel in use.
func (i Installer) Setup() (Status, error) {
	if i.Executable == "" {
		return Status{}, errors.New("ao executable is unknown")
	}
	registered := 0
	for _, browser := range i.browsers() {
		if dirExists(browser.UserDataDir) {
			registered++
		}
	}
	if registered == 0 && i.reg() == nil {
		return i.Status(), ErrChromeNotFound
	}
	if err := i.install(); err != nil {
		return i.Status(), err
	}
	manifest, err := i.manifestJSON()
	if err != nil {
		return i.Status(), err
	}
	if err := writeFileAtomic(i.Paths.HostManifestCopy(), manifest, 0o600); err != nil {
		return i.Status(), fmt.Errorf("write host manifest: %w", err)
	}
	if reg := i.reg(); reg != nil {
		if err := reg.Register(HostName, i.Paths.HostManifestCopy()); err != nil {
			return i.Status(), fmt.Errorf("register native host: %w", err)
		}
		return i.Status(), nil
	}
	for _, browser := range i.browsers() {
		if !dirExists(browser.UserDataDir) {
			continue
		}
		// The single file AO writes outside ~/.ao (see package doc).
		if err := writeFileAtomic(browser.ManifestPath(), manifest, 0o600); err != nil {
			return i.Status(), fmt.Errorf("register native host with %s: %w", browser.Name, err)
		}
	}
	return i.Status(), nil
}

// Refresh re-points an existing registration at the current executable and
// extension bundle. It does nothing when Chrome mode is not enabled.
func (i Installer) Refresh() error {
	if !i.Status().Enabled {
		return nil
	}
	_, err := i.Setup()
	return err
}

// Remove unregisters the host and deletes AO's Chrome-mode files.
func (i Installer) Remove() (Status, error) {
	var errs []error
	// Only unregister a registration that points at this data directory's
	// manifest: the Windows name is shared by every AO install.
	if reg := i.reg(); reg != nil && i.ownsRegistration(reg) {
		errs = append(errs, reg.Unregister(HostName))
	}
	for _, browser := range i.browsers() {
		if i.ownsManifest(browser.ManifestPath()) {
			if err := os.Remove(browser.ManifestPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	for _, path := range []string{i.HostPath(), i.Paths.HostManifestCopy()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := os.RemoveAll(i.Paths.Extension()); err != nil {
		errs = append(errs, err)
	}
	return i.Status(), errors.Join(errs...)
}

// Status reports the current registration without changing anything.
func (i Installer) Status() Status {
	status := Status{
		ExtensionID:   ExtensionID,
		ExtensionPath: i.Paths.Extension(),
		HostPath:      i.HostPath(),
		Channels:      []ChannelStatus{},
	}
	if version, err := extensionVersion(i.ExtensionSource); err == nil {
		status.ExtensionBundled = true
		status.ExtensionVersion = version
	}
	if reg := i.reg(); reg != nil {
		registered := i.ownsRegistration(reg)
		status.Channels = append(status.Channels, ChannelStatus{
			Name:         "Google Chrome",
			Available:    true,
			Registered:   registered,
			ManifestPath: i.Paths.HostManifestCopy(),
		})
		status.Enabled = registered
		return status
	}
	for _, browser := range i.browsers() {
		registered := i.ownsManifest(browser.ManifestPath())
		status.Channels = append(status.Channels, ChannelStatus{
			Name:         browser.Name,
			Available:    dirExists(browser.UserDataDir),
			Registered:   registered,
			ManifestPath: browser.ManifestPath(),
		})
		if registered {
			status.Enabled = true
		}
	}
	return status
}

// ownsRegistration reports whether the registry's AO host entry points at
// this data directory's manifest.
func (i Installer) ownsRegistration(reg hostRegistry) bool {
	path, err := reg.Lookup(HostName)
	if err != nil || path == "" {
		return false
	}
	want := i.Paths.HostManifestCopy()
	return sameFile(path, want) || strings.EqualFold(filepath.Clean(path), filepath.Clean(want))
}

// ownsManifest reports whether path is AO's manifest pointing at this data
// directory's host, so Remove never deletes a manifest it did not write.
func (i Installer) ownsManifest(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var manifest hostManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Name != HostName {
		return false
	}
	return filepath.Clean(manifest.Path) == filepath.Clean(i.HostPath())
}

func (i Installer) manifestJSON() ([]byte, error) {
	data, err := json.MarshalIndent(hostManifest{
		Name:           HostName,
		Description:    "Agent Orchestrator browser bridge",
		Path:           i.HostPath(),
		Type:           "stdio",
		AllowedOrigins: []string{AllowedOrigin()},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (i Installer) install() error {
	if err := os.MkdirAll(i.Paths.Root, 0o700); err != nil {
		return fmt.Errorf("create Chrome mode directory: %w", err)
	}
	if err := writeFileAtomic(i.HostPath(), []byte(i.wrapperScript()), 0o700); err != nil {
		return fmt.Errorf("write host wrapper: %w", err)
	}
	if !i.extensionBundled() {
		// Registration still works; the status reports the missing bundle.
		return nil
	}
	if err := replaceDir(i.ExtensionSource, i.Paths.Extension()); err != nil {
		return fmt.Errorf("install extension: %w", err)
	}
	return nil
}

func (i Installer) extensionBundled() bool {
	_, err := extensionVersion(i.ExtensionSource)
	return err == nil
}

func (i Installer) wrapperScript() string {
	if runtime.GOOS == "windows" {
		return "@echo off\r\n\"" + i.Executable + "\" browser-host --endpoint \"" + i.Paths.Endpoint() + "\" %*\r\n"
	}
	return "#!/bin/sh\n# Agent Orchestrator: Chrome native messaging host for the AO Browser Bridge.\nexec " +
		quoteShell(i.Executable) + " browser-host --endpoint " + quoteShell(i.Paths.Endpoint()) + " \"$@\"\n"
}

func extensionVersion(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no extension bundle")
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version == "" {
		return "", errors.New("invalid extension manifest")
	}
	return manifest.Version, nil
}

// replaceDir copies src to dst through a sibling staging directory so Chrome
// never loads a half-written extension.
func replaceDir(src, dst string) error {
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".extension-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	err = filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(staging, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
	if err != nil {
		return err
	}
	old := dst + ".old"
	_ = os.RemoveAll(old)
	if dirExists(dst) {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(staging, dst); err != nil {
		if dirExists(old) {
			_ = os.Rename(old, dst)
		}
		return err
	}
	return os.RemoveAll(old)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// hostRegistry is the Windows registry's native-host registration.
type hostRegistry interface {
	Register(name, manifestPath string) error
	Unregister(name string) error
	Lookup(name string) (string, error)
}

func browsersUnder(base string, channels ...[2]string) []Browser {
	out := make([]Browser, 0, len(channels))
	for _, channel := range channels {
		out = append(out, Browser{Name: channel[0], UserDataDir: filepath.Join(base, filepath.FromSlash(channel[1]))})
	}
	return out
}
