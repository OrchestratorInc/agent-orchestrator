package persistenthost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

func requireEvidencePlatformRetirement(t *testing.T, dataDir string, owner providerOwner) {
	t.Helper()
	supported := (runtime.GOOS == "windows" && owner.Proof == "windows-job-v1") ||
		((runtime.GOOS == "linux" || runtime.GOOS == "darwin") && owner.Proof == "")
	for _, err := range []error{
		ShutdownExact(t.Context(), dataDir, owner.Session, owner.Identity),
		confirmProviderOwnersStopped(dataDir, owner.Session),
	} {
		if supported {
			if err != nil {
				t.Fatal("complete native-platform evidence rejected", err)
			}
		} else if !errors.Is(err, ErrOwnershipInconclusive) {
			t.Fatal("foreign-platform receipt authorized retirement", err)
		}
	}
}

func TestProviderOwnerEvidenceOversizedValidPrefix(t *testing.T) {
	dataDir, sessionID := t.TempDir(), "owner-evidence"
	identity := descriptorIdentity(Descriptor{Token: "owner-evidence-fixture"})
	owner, err := beginProviderOwner(dataDir, sessionID, identity)
	if err != nil {
		t.Fatal(err)
	}
	owner.State, owner.Boot, owner.Group, owner.SessionID = "stopped", "11111111-1111-4111-8111-111111111111", 42, 42
	owner.Members = []providerProcessIdentity{{PID: 42, Start: 1}}
	raw, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, bytes.Repeat([]byte(" "), (1<<20)-len(raw))...)
	path, _ := providerOwnerPath(dataDir, sessionID, identity)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProviderOwner(dataDir, sessionID, identity); err != nil {
		t.Fatal("exactly-limit complete proof rejected", err)
	}
	requireEvidencePlatformRetirement(t, dataDir, owner)
	for _, suffix := range []string{" ", "incomplete second record", "{}"} {
		if err := os.WriteFile(path, append(append([]byte(nil), raw...), []byte(suffix)...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readProviderOwner(dataDir, sessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
			t.Errorf("oversized ownership record accepted: %v", err)
		}
		if err := ShutdownExact(t.Context(), dataDir, sessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
			t.Errorf("oversized evidence acknowledged exact retirement: %v", err)
		}
		if err := confirmProviderOwnersStopped(dataDir, sessionID); !errors.Is(err, ErrOwnershipInconclusive) {
			t.Errorf("oversized evidence acknowledged retirement: %v", err)
		}
	}
}

func TestProviderOwnerEvidenceIncompleteStopped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*providerOwner)
	}{
		{"version", func(o *providerOwner) { o.Version = 0 }},
		{"identity", func(o *providerOwner) { o.Identity = "" }},
		{"session-name", func(o *providerOwner) { o.Session = "" }},
		{"boot", func(o *providerOwner) { o.Boot = "" }},
		{"group", func(o *providerOwner) { o.Group = 0 }},
		{"session", func(o *providerOwner) { o.SessionID = 0 }},
		{"members", func(o *providerOwner) { o.Members = nil }},
		{"pid", func(o *providerOwner) { o.Members[0].PID = 0 }},
		{"start", func(o *providerOwner) { o.Members[0].Start = 0 }},
		{"state", func(o *providerOwner) { o.State = "" }},
		{"interrupted-state", func(o *providerOwner) { o.State = "stop" }},
		{"negative-group", func(o *providerOwner) { o.Group = -2 }},
		{"broadcast-group", func(o *providerOwner) { o.Group = 1 }},
		{"wrong-leader", func(o *providerOwner) { o.Members[0].PID = 43 }},
		{"duplicate-pid", func(o *providerOwner) { o.Members = append(o.Members, providerProcessIdentity{PID: 42, Start: 2}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir, sessionID, identity := t.TempDir(), "owner-evidence", strings.Repeat("c", 64)
			owner, err := beginProviderOwner(dataDir, sessionID, identity)
			if err != nil {
				t.Fatal(err)
			}
			owner.State, owner.Boot, owner.Group, owner.SessionID = "stopped", "11111111-1111-4111-8111-111111111111", 42, 42
			owner.Members = []providerProcessIdentity{{PID: 42, Start: 1}}
			if err := writeProviderOwner(dataDir, owner); err != nil {
				t.Fatal(err)
			}
			if _, err := readProviderOwner(dataDir, sessionID, identity); err != nil {
				t.Fatal("complete control evidence rejected", err)
			}
			requireEvidencePlatformRetirement(t, dataDir, owner)
			tc.change(&owner)
			raw, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			path, _ := providerOwnerPath(dataDir, sessionID, identity)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readProviderOwner(dataDir, sessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("incomplete stopped record accepted", err)
			}
			if err := ShutdownExact(t.Context(), dataDir, sessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("incomplete stopped record authorized deletion", err)
			}
			if err := confirmProviderOwnersStopped(dataDir, sessionID); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("incomplete stopped record authorized unbound retirement", err)
			}
		})
	}
}

func TestProviderOwnerEvidenceWindowsPIDRange(t *testing.T) {
	for _, pid := range []uint64{1<<32 + 42, 1<<63 - 1} {
		t.Run(fmt.Sprint(pid), func(t *testing.T) {
			dataDir, sessionID, identity := t.TempDir(), "owner-pid-range", strings.Repeat("d", 64)
			owner, err := beginProviderOwner(dataDir, sessionID, identity)
			if err != nil {
				t.Fatal(err)
			}
			owner.State, owner.Proof, owner.Group = "stopped", "windows-job-v1", 42
			owner.Members = []providerProcessIdentity{{PID: 42, Start: 1}}
			if err := writeProviderOwner(dataDir, owner); err != nil {
				t.Fatal(err)
			}
			if _, err := readProviderOwner(dataDir, sessionID, identity); err != nil {
				t.Fatal("complete in-range evidence rejected", err)
			}
			requireEvidencePlatformRetirement(t, dataDir, owner)
			raw, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte(`"group":42`), []byte(fmt.Sprintf(`"group":%d`, pid)), 1)
			raw = bytes.Replace(raw, []byte(`"pid":42`), []byte(fmt.Sprintf(`"pid":%d`, pid)), 1)
			path, _ := providerOwnerPath(dataDir, sessionID, identity)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readProviderOwner(dataDir, sessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("out-of-range Windows PID accepted", err)
			}
			if err := ShutdownExact(t.Context(), dataDir, sessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("out-of-range PID acknowledged exact retirement", err)
			}
			if err := confirmProviderOwnersStopped(dataDir, sessionID); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("out-of-range PID acknowledged unbound retirement", err)
			}
		})
	}
}
