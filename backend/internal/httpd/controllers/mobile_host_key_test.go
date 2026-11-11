package controllers

import (
	"os"
	"path/filepath"
	"testing"
)

// A code without the host key pairs unencrypted, so the status says when the
// key cannot be read and the desktop shows no code.
func TestStatusReportsAnUnreadableHostKey(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json"), LAN: &fakeLAN{}}
	if st := b.Status(); st.HostKey == "" || st.HostKeyUnavailable {
		t.Fatalf("readable key: hostKey=%q unavailable=%v", st.HostKey, st.HostKeyUnavailable)
	}

	unreadable := t.TempDir()
	if err := os.Mkdir(filepath.Join(unreadable, "host.key"), 0o700); err != nil {
		t.Fatal(err)
	}
	b = &BridgeService{ConfigPath: filepath.Join(unreadable, "mobile.json"), LAN: &fakeLAN{}}
	if st := b.Status(); st.HostKey != "" || !st.HostKeyUnavailable {
		t.Fatalf("unreadable key: hostKey=%q unavailable=%v", st.HostKey, st.HostKeyUnavailable)
	}
}
