//go:build linux

package persistenthost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestProviderOwnerEvidenceDuplicateFields(t *testing.T) {
	for _, field := range []string{"state", "State", "escaped-state", "group", "members", "pid", "start", "Start"} {
		t.Run(field, func(t *testing.T) {
			dataDir := t.TempDir()
			_, descriptor, processes := startRemovalCrashHost(t, dataDir, "duplicate-evidence")
			identity := descriptorIdentity(descriptor)
			owner, err := readProviderOwner(dataDir, descriptor.SessionID, identity)
			if err != nil || owner.State != "active" {
				t.Fatal("valid active proof control failed", err)
			}
			if field != "state" && field != "State" && field != "escaped-state" {
				owner.State = "stopped"
			}
			raw, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "state", "State":
				raw = append(raw[:len(raw)-1], []byte(fmt.Sprintf(",%q:\"stopped\"}", field))...)
			case "escaped-state":
				raw = append(raw[:len(raw)-1], []byte(`,"\u0073tate":"stopped"}`)...)
			case "group":
				raw = append(raw[:len(raw)-1], []byte(fmt.Sprintf(",\"group\":%d}", owner.Group))...)
			case "members":
				members, err := json.Marshal(owner.Members)
				if err != nil {
					t.Fatal(err)
				}
				raw = append(raw[:len(raw)-1], []byte(fmt.Sprintf(",\"members\":%s}", members))...)
			case "pid":
				needle := []byte(fmt.Sprintf("\"pid\":%d", owner.Members[0].PID))
				raw = bytes.Replace(raw, needle, append(append([]byte(nil), needle...), append([]byte(","), needle...)...), 1)
			case "start", "Start":
				needle := []byte(fmt.Sprintf("\"start\":%d", owner.Members[0].Start))
				raw = bytes.Replace(raw, needle, []byte(fmt.Sprintf("%s,%q:%d", needle, field, owner.Members[0].Start)), 1)
			}
			path, _ := providerOwnerPath(dataDir, descriptor.SessionID, identity)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readProviderOwner(dataDir, descriptor.SessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("ambiguous duplicate field accepted", err)
			}
			if err := ShutdownExact(t.Context(), dataDir, descriptor.SessionID, identity); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("duplicate evidence acknowledged retirement", err)
			}
			if err := confirmProviderOwnersStopped(dataDir, descriptor.SessionID); !errors.Is(err, ErrOwnershipInconclusive) {
				t.Error("duplicate evidence acknowledged unbound retirement", err)
			}
			if !removalCrashProcessRunning(processes.Provider) || !removalCrashProcessRunning(processes.Child) {
				t.Error("ambiguous evidence authorized a destructive action")
			}
		})
	}
}
