package mobilebridge

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAccessDevicesIssueMatchRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobile", "access-devices.json")
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	devices, err := LoadAccessDevices(path)
	if err != nil {
		t.Fatal(err)
	}
	phone, token, err := devices.Issue("  Dhruv's\x00 iPhone  ", "iOS", now)
	if err != nil {
		t.Fatal(err)
	}
	if phone.Name != "Dhruv's iPhone" || phone.Platform != "ios" {
		t.Fatalf("device = %+v", phone)
	}
	// An account token is 64 hex characters; a device token must not look like one.
	if !strings.HasPrefix(token, DeviceTokenPrefix) || regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(token) {
		t.Fatalf("token %q could be taken for an account token", token)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("the token is stored in the registry file")
	}
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("registry mode = %v, want 0600", info.Mode().Perm())
	}

	// A reload still accepts the token: the registry survives a daemon restart.
	reloaded, err := LoadAccessDevices(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reloaded.Match(token); !ok || got.ID != phone.ID {
		t.Fatalf("Match after reload = %+v, %v", got, ok)
	}
	for _, wrong := range []string{"", "password", token + "0", strings.TrimPrefix(token, DeviceTokenPrefix)} {
		if _, ok := reloaded.Match(wrong); ok {
			t.Fatalf("Match(%q) succeeded", wrong)
		}
	}

	laptop, laptopToken, err := reloaded.Issue("MacBook", "darwin", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if revoked, ok, err := reloaded.Revoke(phone.ID); err != nil || !ok || revoked.TokenHash != phone.TokenHash {
		t.Fatalf("Revoke = %+v, %v, %v", revoked, ok, err)
	}
	if _, ok := reloaded.Match(token); ok {
		t.Fatal("a revoked token still matches")
	}
	if _, ok := reloaded.Match(laptopToken); !ok {
		t.Fatal("revoking one device revoked another")
	}
	if _, ok, _ := reloaded.Revoke(phone.ID); ok {
		t.Fatal("revoking twice reported a device")
	}
	if list := reloaded.List(); len(list) != 1 || list[0].ID != laptop.ID {
		t.Fatalf("List = %+v", list)
	}
	if err := reloaded.RevokeAll(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Match(laptopToken); ok || len(reloaded.List()) != 0 {
		t.Fatal("RevokeAll left a device")
	}
}

func TestAccessDevicesTouchAndLink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access-devices.json")
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	devices, _ := LoadAccessDevices(path)
	phone, _, err := devices.Issue("phone", "ios", now)
	if err != nil {
		t.Fatal(err)
	}
	// Inside the write limit nothing moves; past it the time is stored.
	devices.Touch(phone.ID, now.Add(touchInterval-time.Second))
	if got := devices.List()[0].LastSeenAt; !got.Equal(now) {
		t.Fatalf("LastSeenAt moved inside the write limit: %v", got)
	}
	later := now.Add(touchInterval + time.Second)
	devices.Touch(phone.ID, later)
	devices.TouchShared(SharedPassword, later)
	if err := devices.LinkInstall(phone.ID, "install-1"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadAccessDevices(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.List()[0]
	if !got.LastSeenAt.Equal(later) || got.InstallID != "install-1" {
		t.Fatalf("reloaded device = %+v", got)
	}
	if !reloaded.SharedLastUsed(SharedPassword).Equal(later) || !reloaded.SharedLastUsed(SharedAccount).IsZero() {
		t.Fatalf("shared last used = %v / %v", reloaded.SharedLastUsed(SharedPassword), reloaded.SharedLastUsed(SharedAccount))
	}
}

func TestAccessDevicesCapAndDamagedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access-devices.json")
	devices, _ := LoadAccessDevices(path)
	now := time.Now()
	for range maxAccessDevices {
		if _, _, err := devices.Issue("d", "ios", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := devices.Issue("one too many", "ios", now); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("Issue past the cap: %v", err)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	damaged, err := LoadAccessDevices(path)
	if err == nil || len(damaged.List()) != 0 {
		t.Fatalf("damaged file: err=%v devices=%d", err, len(damaged.List()))
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("the damaged file was not kept aside: %v", err)
	}
	// The empty registry is usable: a device can pair again.
	if _, _, err := damaged.Issue("again", "ios", now); err != nil {
		t.Fatal(err)
	}
}

func TestAnUnreadableRegistryIsMovedAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access-devices.json")
	// A directory in the file's place fails to read on every platform.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	devices, err := LoadAccessDevices(path)
	if err == nil {
		t.Fatal("an unreadable registry loaded without an error")
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("the unreadable registry was not moved aside: %v", err)
	}
	if _, _, err := devices.Issue("Phone", "ios", time.Now()); err != nil {
		t.Fatalf("pair after an unreadable registry: %v", err)
	}
}

func TestARegistryThatCannotBeMovedAsideIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access-devices.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory at the .bad path makes the rename fail everywhere.
	if err := os.MkdirAll(filepath.Join(path+".bad", "taken"), 0o700); err != nil {
		t.Fatal(err)
	}
	devices, err := LoadAccessDevices(path)
	if err == nil {
		t.Fatal("a damaged registry loaded without an error")
	}
	if _, _, err := devices.Issue("Phone", "ios", time.Now()); err == nil {
		t.Fatal("a device paired over a registry that was not moved aside")
	}
	if raw, _ := os.ReadFile(path); string(raw) != "{not json" {
		t.Fatalf("the registry was written over: %q", raw)
	}
}

func TestPairingCodesAreSingleUseAndExpire(t *testing.T) {
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	codes := NewPairingCodes(func() time.Time { return now })
	code, expiry, err := codes.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 || !expiry.Equal(now.Add(PairingCodeTTL)) {
		t.Fatalf("code %q expires %v", code, expiry)
	}
	if !codes.Valid(code) || codes.Valid("") || codes.Valid("wrongcod") {
		t.Fatal("Valid is wrong")
	}
	if !codes.Consume(code) {
		t.Fatal("first use refused")
	}
	if codes.Consume(code) || codes.Valid(code) {
		t.Fatal("a code worked twice")
	}

	expiring, _, _ := codes.Issue()
	now = now.Add(PairingCodeTTL)
	if codes.Valid(expiring) || codes.Consume(expiring) {
		t.Fatal("an expired code worked")
	}

	// Past the cap, the code closest to expiry makes room.
	first, _, _ := codes.Issue()
	for range maxPairingCodes {
		now = now.Add(time.Second)
		if _, _, err := codes.Issue(); err != nil {
			t.Fatal(err)
		}
	}
	if codes.Valid(first) {
		t.Fatal("the oldest code survived past the cap")
	}
	last, _, _ := codes.Issue()
	codes.Clear()
	if codes.Valid(last) {
		t.Fatal("Clear left a code")
	}
}
