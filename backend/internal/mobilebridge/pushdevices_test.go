package mobilebridge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPushDevicesPath(t *testing.T) {
	got := PushDevicesPath("/data")
	want := filepath.Join("/data", "mobile", "push-devices.json")
	if got != want {
		t.Fatalf("path = %q want %q", got, want)
	}
}

func TestValidPushToken(t *testing.T) {
	valid := []string{
		"ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]",
		"ExpoPushToken[abc-123_DEF]",
	}
	for _, tok := range valid {
		if !ValidPushToken(tok) {
			t.Errorf("ValidPushToken(%q) = false, want true", tok)
		}
	}
	invalid := []string{
		"",
		"garbage",
		"ExponentPushToken[]",
		"fcm:some-raw-token",
		"ExponentPushToken[abc",
	}
	for _, tok := range invalid {
		if ValidPushToken(tok) {
			t.Errorf("ValidPushToken(%q) = true, want false", tok)
		}
	}
}

func TestLoadRegistryMissingIsEmpty(t *testing.T) {
	reg, err := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("fresh registry = %+v, want empty", got)
	}
}

func TestUpsertListDeleteRoundTrip(t *testing.T) {
	path := PushDevicesPath(t.TempDir())
	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	a := PushDevice{InstallID: "inst-a", Token: "ExpoPushToken[a]", Platform: "android", DeviceName: "Pixel", CreatedAt: now, LastSeenAt: now}
	b := PushDevice{InstallID: "inst-b", Token: "ExpoPushToken[b]", Platform: "ios", DeviceName: "iPhone", CreatedAt: now, LastSeenAt: now}
	if err := reg.Upsert(a); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := reg.Upsert(b); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	if got := reg.List(); len(got) != 2 {
		t.Fatalf("list len = %d, want 2", len(got))
	}

	// Reload from disk: the two devices must survive a restart.
	reloaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.List(); len(got) != 2 {
		t.Fatalf("reloaded list len = %d, want 2", len(got))
	}

	if err := reg.Delete("inst-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got := reg.List()
	if len(got) != 1 || got[0].Token != "ExpoPushToken[b]" {
		t.Fatalf("after delete = %+v, want only b", got)
	}
}

func TestUpsertPreservesCreatedAt(t *testing.T) {
	reg, err := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	created := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	first := PushDevice{InstallID: "inst-a", Token: "ExpoPushToken[a]", Platform: "android", CreatedAt: created, LastSeenAt: created}
	if err := reg.Upsert(first); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	// Re-register the same token (e.g. foreground refresh) with a fresh CreatedAt;
	// the store must keep the ORIGINAL CreatedAt and only advance LastSeenAt.
	again := PushDevice{InstallID: "inst-a", Token: "ExpoPushToken[a]", Platform: "android", DeviceName: "renamed", CreatedAt: later, LastSeenAt: later}
	if err := reg.Upsert(again); err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("list len = %d, want 1 (idempotent upsert)", len(got))
	}
	if !got[0].CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want preserved %v", got[0].CreatedAt, created)
	}
	if !got[0].LastSeenAt.Equal(later) {
		t.Fatalf("LastSeenAt = %v, want advanced %v", got[0].LastSeenAt, later)
	}
	if got[0].DeviceName != "renamed" {
		t.Fatalf("DeviceName = %q, want updated to renamed", got[0].DeviceName)
	}
}

func TestUpsertKeepsPushHostLabelAcrossTokenlessAnnounce(t *testing.T) {
	reg, err := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Upsert(PushDevice{InstallID: "phone", Token: "ExpoPushToken[a]", HostName: "Host A"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Upsert(PushDevice{InstallID: "phone"}); err != nil {
		t.Fatal(err)
	}
	if got := reg.List()[0].HostName; got != "Host A" {
		t.Fatalf("host name = %q", got)
	}
	if err := reg.Upsert(PushDevice{InstallID: "phone", HostName: "Renamed A"}); err != nil {
		t.Fatal(err)
	}
	if got := reg.List()[0].HostName; got != "Renamed A" {
		t.Fatalf("renamed host = %q", got)
	}
}

func TestUpsertRejectsInvalidToken(t *testing.T) {
	reg, _ := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err := reg.Upsert(PushDevice{Token: "garbage"}); err == nil {
		t.Fatal("expected error upserting invalid token")
	}
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("invalid upsert leaked a row: %+v", got)
	}
}

func TestRegistryFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows does not honor Unix file-permission bits; os.Chmod only toggles
		// the read-only flag, so Stat reports 0666. The 0600 intent is a no-op there.
		t.Skip("file mode bits are not meaningful on Windows")
	}
	path := PushDevicesPath(t.TempDir())
	reg, _ := LoadRegistry(path)
	now := time.Now().UTC()
	if err := reg.Upsert(PushDevice{InstallID: "inst-a", Token: "ExpoPushToken[a]", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v want 0600", info.Mode().Perm())
	}
}

func TestDeleteMissingTokenIsNoop(t *testing.T) {
	reg, _ := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err := reg.Delete("missing-install-id"); err != nil {
		t.Fatalf("delete missing install id should be a no-op, got %v", err)
	}
}

func TestUpsertKeysByInstallID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := reg.Upsert(PushDevice{
		InstallID: "inst-1", Token: "ExponentPushToken[a]", DeviceName: "Phone",
		CreatedAt: created, LastSeenAt: created,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// The phone reinstalls: same install ID, brand new push token.
	later := created.Add(48 * time.Hour)
	if err := reg.Upsert(PushDevice{
		InstallID: "inst-1", Token: "ExponentPushToken[b]", DeviceName: "Phone",
		CreatedAt: later, LastSeenAt: later,
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	devices := reg.List()
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1 (reinstall must not duplicate)", len(devices))
	}
	got := devices[0]
	if got.Token != "ExponentPushToken[b]" {
		t.Fatalf("token = %q, want the new one", got.Token)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want original %v", got.CreatedAt, created)
	}
}

func TestUpsertAdoptsLegacyRowByToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	legacy := `{"devices":[{"token":"ExponentPushToken[a]","platform":"ios","deviceName":"iPhone","createdAt":"2026-01-01T00:00:00Z","lastSeenAt":"2026-01-02T00:00:00Z"}]}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	migrated := reg.List()
	if len(migrated) != 1 || migrated[0].InstallID == "" {
		t.Fatalf("migration did not synthesize an install ID: %+v", migrated)
	}
	// That same phone now registers with its real install ID and same token.
	if err := reg.Upsert(PushDevice{
		InstallID: "real-inst", Token: "ExponentPushToken[a]", DeviceName: "iPhone",
		CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	devices := reg.List()
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1 (adoption must not duplicate)", len(devices))
	}
	if devices[0].InstallID != "real-inst" {
		t.Fatalf("InstallID = %q, want the adopted real one", devices[0].InstallID)
	}
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !devices[0].CreatedAt.Equal(want) {
		t.Fatalf("CreatedAt = %v, want preserved %v", devices[0].CreatedAt, want)
	}
}

func TestUnregisterTokenAndDeleteByInstallID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, _ := LoadRegistry(path)
	now := time.Now().UTC()
	_ = reg.Upsert(PushDevice{InstallID: "i1", Token: "ExponentPushToken[a]", CreatedAt: now, LastSeenAt: now})
	_ = reg.Upsert(PushDevice{InstallID: "i2", Token: "ExponentPushToken[b]", CreatedAt: now, LastSeenAt: now})

	// The two removal doors differ on purpose: unregistering a token detaches the
	// registration but leaves the paired phone listed, while the desktop's Delete
	// removes the phone outright.
	if err := reg.UnregisterToken("ExponentPushToken[a]"); err != nil {
		t.Fatalf("unregister token: %v", err)
	}
	if err := reg.Delete("i2"); err != nil {
		t.Fatalf("delete by install id: %v", err)
	}

	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("devices = %+v, want only i1 surviving (tokenless)", got)
	}
	if got[0].InstallID != "i1" || got[0].Token != "" {
		t.Fatalf("device = %+v, want i1 with its token cleared", got[0])
	}
	if err := reg.Delete("missing"); err != nil {
		t.Fatalf("deleting unknown install id must be a no-op, got %v", err)
	}
}

// The desktop's remove leaves nothing behind. A phone still in use reconnects
// (every phone shares the connection password) and must come back as a plain
// paired device, matched by install id or by token after a reinstall.
func TestRemovedDeviceReRegistersAsFresh(t *testing.T) {
	for _, tc := range []struct{ name, reinstallID string }{
		{"same install id", "inst-1"},
		{"reinstall keeps the token", "inst-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "push-devices.json")
			reg, err := LoadRegistry(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			now := time.Now().UTC()
			if err := reg.Upsert(PushDevice{InstallID: "inst-1", Token: "ExponentPushToken[a]", CreatedAt: now, LastSeenAt: now}); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if err := reg.Delete("inst-1"); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if err := reg.Upsert(PushDevice{InstallID: tc.reinstallID, Token: "ExponentPushToken[a]", CreatedAt: now, LastSeenAt: now}); err != nil {
				t.Fatalf("re-upsert: %v", err)
			}
			got := reg.List()
			if len(got) != 1 || got[0].InstallID != tc.reinstallID || got[0].Token != "ExponentPushToken[a]" {
				t.Fatalf("devices = %+v, want one row for %s with its token", got, tc.reinstallID)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if strings.Contains(string(b), `"removed"`) {
				t.Fatalf("registry file still records removals: %s", b)
			}
		})
	}
}

// Files written by older daemons carry a per-device "muted" flag and a
// "removed" list. Both are ignored: the devices load normally, and the next
// save drops the stale keys.
func TestLoadIgnoresLegacyMuteAndRemovalRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	old := `{"devices":[{"installId":"inst-1","token":"ExponentPushToken[a]","muted":true,"createdAt":"2026-01-01T00:00:00Z","lastSeenAt":"2026-01-02T00:00:00Z"}],` +
		`"removed":[{"installId":"inst-gone","token":"ExponentPushToken[z]","removedAt":"2026-01-03T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := reg.List(); len(got) != 1 || got[0].InstallID != "inst-1" {
		t.Fatalf("devices = %+v, want inst-1 loaded", got)
	}

	now := time.Now().UTC()
	if err := reg.Upsert(PushDevice{InstallID: "inst-gone", Token: "ExponentPushToken[z]", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, stale := range []string{`"muted"`, `"removed"`} {
		if strings.Contains(string(b), stale) {
			t.Fatalf("saved registry still carries %s: %s", stale, b)
		}
	}
}

// TestTokenlessDeviceSurvivesReload pins the fix for a daemon-restart bug: a
// paired phone with no push token (permission not granted, or a build that
// can't mint one) must survive a LoadRegistry reload with its identity
// intact, not be silently dropped because it has no token.
func TestTokenlessDeviceSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dev := PushDevice{InstallID: "inst-a", Token: "", Platform: "ios", DeviceName: "iPhone", CreatedAt: created, LastSeenAt: created}
	if err := reg.Upsert(dev); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	reloaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := reloaded.List()
	if len(got) != 1 {
		t.Fatalf("devices = %d, want 1 (tokenless row must survive reload)", len(got))
	}
	if got[0].InstallID != "inst-a" {
		t.Fatalf("InstallID = %q, want inst-a preserved", got[0].InstallID)
	}
	if !got[0].CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want preserved %v (not reset on reload)", got[0].CreatedAt, created)
	}
}

// TestRowWithNeitherTokenNorInstallIDIsDroppedOnLoad pins the deliberate
// exception: a row with no token AND no install ID carries no identity to
// preserve or synthesize, so it must be dropped rather than kept (which would
// leave a permanently-unaddressable row) or re-synthesized every reload
// (which would multiply it). Two reload cycles must not accumulate rows.
func TestRowWithNeitherTokenNorInstallIDIsDroppedOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	raw := `{"devices":[{"platform":"ios","deviceName":"ghost","createdAt":"2026-01-01T00:00:00Z","lastSeenAt":"2026-01-02T00:00:00Z"}]}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("devices = %+v, want empty (identity-less row must be dropped)", got)
	}

	reloaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.List(); len(got) != 0 {
		t.Fatalf("devices = %+v, want still empty after a second reload cycle (must not accumulate)", got)
	}
}

func TestUpsertAcceptsDeviceWithoutToken(t *testing.T) {
	reg, err := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	now := time.Now().UTC()
	dev := PushDevice{InstallID: "inst-a", Token: "", Platform: "ios", DeviceName: "iPhone", CreatedAt: now, LastSeenAt: now}
	if err := reg.Upsert(dev); err != nil {
		t.Fatalf("upsert without token: %v", err)
	}
	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("list len = %d, want 1", len(got))
	}
	if got[0].InstallID != "inst-a" || got[0].Token != "" {
		t.Fatalf("stored device = %+v, want tokenless inst-a", got[0])
	}
}

func TestUpsertStillRejectsMalformedToken(t *testing.T) {
	reg, err := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := reg.Upsert(PushDevice{InstallID: "inst-a", Token: "garbage"}); err == nil {
		t.Fatal("expected error upserting malformed token")
	}
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("malformed upsert leaked a row: %+v", got)
	}
}

func TestTokenAttachesToExistingIdentityRow(t *testing.T) {
	reg, err := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	now := time.Now().UTC()
	// The phone announces its identity with no token (permission not yet granted).
	if err := reg.Upsert(PushDevice{InstallID: "inst-a", Token: "", Platform: "ios", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatalf("announce: %v", err)
	}
	later := now.Add(time.Minute)
	// Permission is granted; the same install ID registers WITH a token.
	if err := reg.Upsert(PushDevice{InstallID: "inst-a", Token: "ExponentPushToken[a]", Platform: "ios", CreatedAt: later, LastSeenAt: later}); err != nil {
		t.Fatalf("register with token: %v", err)
	}
	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("devices = %d, want 1 (token must attach to the same row)", len(got))
	}
	if got[0].InstallID != "inst-a" || got[0].Token != "ExponentPushToken[a]" {
		t.Fatalf("device = %+v, want inst-a with the new token", got[0])
	}
}

func TestAnnounceDoesNotBlankExistingToken(t *testing.T) {
	reg, err := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	now := time.Now().UTC()
	if err := reg.Upsert(PushDevice{InstallID: "inst-a", Token: "ExponentPushToken[a]", Platform: "ios", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatalf("register with token: %v", err)
	}
	later := now.Add(time.Minute)
	// A later announce (e.g. app foregrounded) carries no token at all.
	if err := reg.Upsert(PushDevice{InstallID: "inst-a", Token: "", Platform: "ios", CreatedAt: later, LastSeenAt: later}); err != nil {
		t.Fatalf("announce: %v", err)
	}
	got := reg.List()
	if len(got) != 1 {
		t.Fatalf("devices = %d, want 1", len(got))
	}
	if got[0].Token != "ExponentPushToken[a]" {
		t.Fatalf("token = %q, want the original token to survive a tokenless announce", got[0].Token)
	}
}

func TestTokenlessAnnounceDoesNotAdoptByToken(t *testing.T) {
	reg, err := LoadRegistry(PushDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	now := time.Now().UTC()
	if err := reg.Upsert(PushDevice{InstallID: "inst-a", Token: "", Platform: "ios", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatalf("announce a: %v", err)
	}
	if err := reg.Upsert(PushDevice{InstallID: "inst-b", Token: "", Platform: "android", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatalf("announce b: %v", err)
	}
	got := reg.List()
	if len(got) != 2 {
		t.Fatalf("devices = %d, want 2 (two distinct tokenless phones must not merge)", len(got))
	}
}

// A phone upgrading to a build that sends an install id reaches the daemon under
// two identities: the legacy row that owns its push token, and its real install
// id. PushManager announces (tokenless) and registers (with token) from separate
// effects, so the announce can land first and create the real-id row before the
// token ever arrives. Both identities must end up as ONE row: two rows sharing a
// token would double every notification — the exact defect install ids exist to
// prevent.
func TestLegacyRowMergesWhenAnnounceLandsBeforeRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	token := "ExponentPushToken[abc]"
	legacyCreated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)

	if err := reg.Upsert(PushDevice{
		InstallID: "legacy-old", Token: token, DeviceName: "Phone", Platform: "android",
		CreatedAt: legacyCreated, LastSeenAt: legacyCreated,
	}); err != nil {
		t.Fatalf("legacy upsert: %v", err)
	}
	if err := reg.Upsert(PushDevice{
		InstallID: "real-id", DeviceName: "Phone", Platform: "android",
		CreatedAt: now, LastSeenAt: now,
	}); err != nil {
		t.Fatalf("announce: %v", err)
	}
	if err := reg.Upsert(PushDevice{
		InstallID: "real-id", Token: token, DeviceName: "Phone", Platform: "android",
		CreatedAt: now, LastSeenAt: now,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	devices := reg.List()
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1 (the legacy row must be merged, not left behind): %+v", len(devices), devices)
	}
	got := devices[0]
	if got.InstallID != "real-id" {
		t.Fatalf("InstallID = %q, want the real install id to win", got.InstallID)
	}
	if got.Token != token {
		t.Fatalf("Token = %q, want %q", got.Token, token)
	}
	if !got.CreatedAt.Equal(legacyCreated) {
		t.Fatalf("CreatedAt = %v, want the older legacy value %v (paired-since must not jump forward)", got.CreatedAt, legacyCreated)
	}
}

// No row may ever share a push token with another row, whatever order the
// announce and registration arrive in.
func TestNoTwoRowsEverShareAToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, _ := LoadRegistry(path)
	now := time.Now().UTC()
	token := "ExponentPushToken[abc]"

	_ = reg.Upsert(PushDevice{InstallID: "legacy-old", Token: token, CreatedAt: now, LastSeenAt: now})
	_ = reg.Upsert(PushDevice{InstallID: "real-id", CreatedAt: now, LastSeenAt: now})
	_ = reg.Upsert(PushDevice{InstallID: "real-id", Token: token, CreatedAt: now, LastSeenAt: now})
	_ = reg.Upsert(PushDevice{InstallID: "third", Token: token, CreatedAt: now, LastSeenAt: now})

	seen := map[string]string{}
	for _, d := range reg.List() {
		if d.Token == "" {
			continue
		}
		if prev, dup := seen[d.Token]; dup {
			t.Fatalf("token %s held by both %q and %q", d.Token, prev, d.InstallID)
		}
		seen[d.Token] = d.InstallID
	}
}

// Losing a push token must not unpair the phone. A row represents a paired
// phone, so switching notifications off in the mobile app (or Expo reporting the
// token dead) leaves the device listed — keeping its paired-since — instead of
// vanishing from the roster until it happens to announce again.
func TestUnregisterTokenKeepsThePairedRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, _ := LoadRegistry(path)
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	token := "ExponentPushToken[a]"

	if err := reg.Upsert(PushDevice{
		InstallID: "real-id", Token: token, DeviceName: "Phone",
		CreatedAt: created, LastSeenAt: created,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := reg.UnregisterToken(token); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	devices := reg.List()
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want the paired phone to survive: %+v", len(devices), devices)
	}
	got := devices[0]
	if got.Token != "" {
		t.Fatalf("Token = %q, want cleared", got.Token)
	}
	if got.InstallID != "real-id" || got.DeviceName != "Phone" {
		t.Fatalf("identity lost: %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want preserved %v", got.CreatedAt, created)
	}
}

// A synthesized install ID is a placeholder, not an identity the phone knows.
// Once its token is gone the row can never be reclaimed by any announce, so it
// is deleted rather than stranded as a permanently unreachable tokenless row.
func TestUnregisterTokenDropsSynthesizedRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, _ := LoadRegistry(path)
	now := time.Now().UTC()
	token := "ExponentPushToken[a]"

	if err := reg.Upsert(PushDevice{
		InstallID: LegacyInstallIDPrefix + "synthesized", Token: token,
		CreatedAt: now, LastSeenAt: now,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := reg.UnregisterToken(token); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("devices = %+v, want the placeholder row dropped", got)
	}
}

// The cleared token must survive a reload: a restart cannot resurrect a
// registration the phone already gave up, which would resume delivery.
func TestUnregisterTokenPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, _ := LoadRegistry(path)
	now := time.Now().UTC()
	token := "ExponentPushToken[a]"

	_ = reg.Upsert(PushDevice{InstallID: "real-id", Token: token, CreatedAt: now, LastSeenAt: now})
	_ = reg.UnregisterToken(token)

	reloaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	devices := reloaded.List()
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1 after reload: %+v", len(devices), devices)
	}
	if devices[0].Token != "" {
		t.Fatalf("Token = %q after reload, want it to stay cleared", devices[0].Token)
	}
}

func TestUnregisterUnknownOrEmptyTokenIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, _ := LoadRegistry(path)
	now := time.Now().UTC()
	_ = reg.Upsert(PushDevice{InstallID: "real-id", Token: "ExponentPushToken[a]", CreatedAt: now, LastSeenAt: now})

	if err := reg.UnregisterToken("ExponentPushToken[missing]"); err != nil {
		t.Fatalf("unknown token: %v", err)
	}
	if err := reg.UnregisterToken(""); err != nil {
		t.Fatalf("empty token: %v", err)
	}
	got := reg.List()
	if len(got) != 1 || got[0].Token != "ExponentPushToken[a]" {
		t.Fatalf("an unrelated row was touched: %+v", got)
	}
}

// Dead-token pruning goes through the same door, so a phone whose token Expo
// reports as dead stays paired and listed rather than disappearing from the
// desktop's roster.
func TestDeadTokenPruningKeepsThePairedRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push-devices.json")
	reg, _ := LoadRegistry(path)
	now := time.Now().UTC()
	token := "ExponentPushToken[dead]"
	_ = reg.Upsert(PushDevice{InstallID: "real-id", Token: token, DeviceName: "Phone", CreatedAt: now, LastSeenAt: now})

	// What dispatcher.prune does when Expo answers DeviceNotRegistered.
	if err := reg.UnregisterToken(token); err != nil {
		t.Fatalf("prune: %v", err)
	}

	got := reg.List()
	if len(got) != 1 || got[0].InstallID != "real-id" || got[0].Token != "" {
		t.Fatalf("devices = %+v, want the phone still paired with no token", got)
	}
}

// Every registry mutation must be all-or-nothing with the file write. If the
// save fails, the running daemon has to keep behaving exactly as the file says —
// otherwise the API reports failure while memory quietly diverges, and a restart
// silently reverts whatever the user thought had happened.
func TestEveryMutationRollsBackWhenTheWriteFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not block writes on Windows")
	}
	token := "ExponentPushToken[a]"

	// setup seeds one device and returns the registry plus its directory.
	setup := func(t *testing.T) (*DeviceRegistry, string) {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "mobile", "push-devices.json")
		reg, err := LoadRegistry(path)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		now := time.Now().UTC()
		if err := reg.Upsert(PushDevice{InstallID: "real-id", Token: token, DeviceName: "Phone", CreatedAt: now, LastSeenAt: now}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		regDir := filepath.Dir(path)
		if err := os.Chmod(regDir, 0o500); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(regDir, 0o700) })
		return reg, regDir
	}

	t.Run("Upsert", func(t *testing.T) {
		reg, _ := setup(t)
		now := time.Now().UTC()
		if err := reg.Upsert(PushDevice{InstallID: "second-id", Token: "ExponentPushToken[b]", CreatedAt: now, LastSeenAt: now}); err == nil {
			t.Fatal("expected the write to fail")
		}
		if got := reg.List(); len(got) != 1 || got[0].InstallID != "real-id" {
			t.Fatalf("memory kept a device the write never saved: %+v", got)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		reg, _ := setup(t)
		if err := reg.Delete("real-id"); err == nil {
			t.Fatal("expected the write to fail")
		}
		if got := reg.List(); len(got) != 1 {
			t.Fatalf("device removed from memory despite the failed write: %+v", got)
		}
	})

	t.Run("UnregisterToken", func(t *testing.T) {
		reg, _ := setup(t)
		if err := reg.UnregisterToken(token); err == nil {
			t.Fatal("expected the write to fail")
		}
		if got := reg.List(); len(got) != 1 || got[0].Token != token {
			t.Fatalf("token cleared in memory despite the failed write: %+v", got)
		}
	})

}

// Unpairing and switching notifications off are different intents and must have
// different outcomes: a phone that turns notifications off is still paired and
// belongs in the roster; a phone that forgets this server is gone and must not
// be left behind on the old desktop.
func TestUnpairRemovesTheRowWhileUnregisterKeepsIt(t *testing.T) {
	token := "ExponentPushToken[a]"

	t.Run("notifications off keeps the phone", func(t *testing.T) {
		reg, _ := LoadRegistry(filepath.Join(t.TempDir(), "d.json"))
		now := time.Now().UTC()
		_ = reg.Upsert(PushDevice{InstallID: "real-id", Token: token, CreatedAt: now, LastSeenAt: now})
		if err := reg.UnregisterToken(token); err != nil {
			t.Fatalf("unregister: %v", err)
		}
		got := reg.List()
		if len(got) != 1 || got[0].Token != "" {
			t.Fatalf("want the phone still paired with no token, got %+v", got)
		}
	})

	t.Run("forget server removes the phone by install id", func(t *testing.T) {
		reg, _ := LoadRegistry(filepath.Join(t.TempDir(), "d.json"))
		now := time.Now().UTC()
		_ = reg.Upsert(PushDevice{InstallID: "real-id", Token: token, CreatedAt: now, LastSeenAt: now})
		if err := reg.Unpair("real-id"); err != nil {
			t.Fatalf("unpair: %v", err)
		}
		if got := reg.List(); len(got) != 0 {
			t.Fatalf("want the phone gone, got %+v", got)
		}
	})

	t.Run("forget server works by token for older builds", func(t *testing.T) {
		reg, _ := LoadRegistry(filepath.Join(t.TempDir(), "d.json"))
		now := time.Now().UTC()
		_ = reg.Upsert(PushDevice{InstallID: "real-id", Token: token, CreatedAt: now, LastSeenAt: now})
		if err := reg.Unpair(token); err != nil {
			t.Fatalf("unpair by token: %v", err)
		}
		if got := reg.List(); len(got) != 0 {
			t.Fatalf("want the phone gone, got %+v", got)
		}
	})

	t.Run("unpair touches only the matching phone", func(t *testing.T) {
		reg, _ := LoadRegistry(filepath.Join(t.TempDir(), "d.json"))
		now := time.Now().UTC()
		_ = reg.Upsert(PushDevice{InstallID: "phone-a", Token: token, CreatedAt: now, LastSeenAt: now})
		_ = reg.Upsert(PushDevice{InstallID: "phone-b", Token: "ExponentPushToken[b]", CreatedAt: now, LastSeenAt: now})
		_ = reg.Unpair("phone-a")
		got := reg.List()
		if len(got) != 1 || got[0].InstallID != "phone-b" {
			t.Fatalf("want only phone-b left, got %+v", got)
		}
	})

	t.Run("unknown or empty id is a no-op", func(t *testing.T) {
		reg, _ := LoadRegistry(filepath.Join(t.TempDir(), "d.json"))
		now := time.Now().UTC()
		_ = reg.Upsert(PushDevice{InstallID: "real-id", Token: token, CreatedAt: now, LastSeenAt: now})
		if err := reg.Unpair("nobody"); err != nil {
			t.Fatalf("unknown: %v", err)
		}
		if err := reg.Unpair(""); err != nil {
			t.Fatalf("empty: %v", err)
		}
		if got := reg.List(); len(got) != 1 {
			t.Fatalf("an unrelated row was touched: %+v", got)
		}
	})
}
