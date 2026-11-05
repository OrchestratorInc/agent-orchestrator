package agy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBridgeConversationToCLIAndACP(t *testing.T) {
	geminiHome := t.TempDir()
	id := "test-conv-1234"

	// 1. Setup an ACP conversation
	acpDir := filepath.Join(geminiHome, "antigravity-acp")
	if err := os.MkdirAll(filepath.Join(acpDir, "conversations"), 0700); err != nil {
		t.Fatal(err)
	}
	acpDB := filepath.Join(acpDir, "conversations", id+".db")
	if err := os.WriteFile(acpDB, []byte("acp-sqlite-data"), 0600); err != nil {
		t.Fatal(err)
	}
	acpMeta := filepath.Join(acpDir, "conversations", id+".meta")
	if err := os.WriteFile(acpMeta, []byte(`{"mode_id":"yolo"}`), 0600); err != nil {
		t.Fatal(err)
	}
	acpBrain := filepath.Join(acpDir, "brain", id)
	if err := os.MkdirAll(acpBrain, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acpBrain, "scratch.txt"), []byte("brain-content"), 0600); err != nil {
		t.Fatal(err)
	}

	// 2. Bridge to CLI
	if err := BridgeConversationToCLI(geminiHome, id); err != nil {
		t.Fatalf("BridgeConversationToCLI: %v", err)
	}

	cliDB := filepath.Join(geminiHome, "antigravity-cli", "conversations", id+".db")
	cliData, err := os.ReadFile(cliDB)
	if err != nil {
		t.Fatalf("read bridged cli db: %v", err)
	}
	if string(cliData) != "acp-sqlite-data" {
		t.Fatalf("got cli db content %q, want acp-sqlite-data", string(cliData))
	}
	acpInfo, err := os.Stat(acpDB)
	if err != nil {
		t.Fatal(err)
	}
	cliInfo, err := os.Stat(cliDB)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(acpInfo, cliInfo) {
		t.Fatal("bridged databases share an inode; expected an independent snapshot")
	}

	cliMeta := filepath.Join(geminiHome, "antigravity-cli", "conversations", id+".meta")
	if _, err := os.Stat(cliMeta); err != nil {
		t.Fatalf("bridged cli meta missing: %v", err)
	}

	cliBrain := filepath.Join(geminiHome, "antigravity-cli", "brain", id)
	if _, err := os.Stat(filepath.Join(cliBrain, "scratch.txt")); err != nil {
		t.Fatalf("bridged cli brain missing: %v", err)
	}

	// 3. Update CLI db and bridge back to ACP
	if err := os.WriteFile(cliDB, []byte("cli-updated-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BridgeConversationToACP(geminiHome, id); err != nil {
		t.Fatalf("BridgeConversationToACP: %v", err)
	}

	acpUpdated, err := os.ReadFile(acpDB)
	if err != nil {
		t.Fatalf("read bridged acp db: %v", err)
	}
	if string(acpUpdated) != "cli-updated-data" {
		t.Fatalf("got acp db content %q, want cli-updated-data", string(acpUpdated))
	}
}

func TestBridgeRejectsPathTraversalAndCopiesSQLiteSidecars(t *testing.T) {
	home := t.TempDir()
	if err := BridgeConversationToCLI(home, "../outside"); err == nil {
		t.Fatal("expected invalid conversation id error")
	}
	const id = "safe-id"
	src := filepath.Join(home, "antigravity-acp", "conversations")
	if err := os.MkdirAll(src, 0700); err != nil {
		t.Fatal(err)
	}
	for suffix, data := range map[string]string{".db": "database", ".db-wal": "wal", ".db-shm": "shm"} {
		if err := os.WriteFile(filepath.Join(src, id+suffix), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := BridgeConversationToCLI(home, id); err != nil {
		t.Fatal(err)
	}
	for suffix, want := range map[string]string{".db": "database", ".db-wal": "wal", ".db-shm": "shm"} {
		got, err := os.ReadFile(filepath.Join(home, "antigravity-cli", "conversations", id+suffix))
		if err != nil || string(got) != want {
			t.Fatalf("sidecar %s = %q, %v; want %q", suffix, got, err, want)
		}
	}
}

func TestBridgeRequiresSourceDatabase(t *testing.T) {
	home := t.TempDir()
	const id = "conv-missing-src"

	// If destination DB exists but source DB is missing, bridge must fail.
	dst := filepath.Join(home, "antigravity-acp", "conversations")
	if err := os.MkdirAll(dst, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, id+".db"), []byte("older-acp-db"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := BridgeConversationToACP(home, id); err == nil {
		t.Fatal("BridgeConversationToACP succeeded with missing source CLI db; want error")
	}

	// Reverse direction: missing ACP source db must also fail.
	cliDst := filepath.Join(home, "antigravity-cli", "conversations")
	if err := os.MkdirAll(cliDst, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDst, "other.db"), []byte("older-cli-db"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BridgeConversationToCLI(home, "other"); err == nil {
		t.Fatal("BridgeConversationToCLI succeeded with missing source ACP db; want error")
	}
}

func TestGeminiHomePrefersSessionThenDaemonThenDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEMINI_HOME", "")
	if got, _ := GeminiHome(nil); got != filepath.Join(home, ".gemini") {
		t.Errorf("default GeminiHome = %q", got)
	}
	t.Setenv("GEMINI_HOME", "/daemon/home")
	if got, _ := GeminiHome(nil); got != "/daemon/home" {
		t.Errorf("daemon GeminiHome = %q", got)
	}
	if got, _ := GeminiHome(map[string]string{"GEMINI_HOME": "/session/home"}); got != "/session/home" {
		t.Errorf("session GeminiHome = %q", got)
	}
}

func TestBridgeSyncsBrainDeletionsAndAbsentFolder(t *testing.T) {
	home := t.TempDir()
	const id = "conv-brain-sync"

	acpDir := filepath.Join(home, "antigravity-acp")
	cliDir := filepath.Join(home, "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(acpDir, "conversations"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acpDir, "conversations", id+".db"), []byte("acp-db"), 0o600); err != nil {
		t.Fatal(err)
	}

	acpBrain := filepath.Join(acpDir, "brain", id)
	if err := os.MkdirAll(filepath.Join(acpBrain, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acpBrain, "f1.txt"), []byte("f1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acpBrain, "f2.txt"), []byte("f2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acpBrain, "sub", "f3.txt"), []byte("f3"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Initial bridge ACP -> CLI
	if err := BridgeConversationToCLI(home, id); err != nil {
		t.Fatalf("BridgeConversationToCLI: %v", err)
	}

	cliBrain := filepath.Join(cliDir, "brain", id)
	for _, rel := range []string{"f1.txt", "f2.txt", filepath.Join("sub", "f3.txt")} {
		if _, err := os.Stat(filepath.Join(cliBrain, rel)); err != nil {
			t.Fatalf("expected bridged file %s: %v", rel, err)
		}
	}

	// 2. CLI deletes f2.txt and sub/, adds f4.txt
	if err := os.Remove(filepath.Join(cliBrain, "f2.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cliBrain, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliBrain, "f4.txt"), []byte("f4"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 3. Bridge CLI -> ACP: ACP must remove f2.txt and sub/, and reflect f4.txt
	if err := BridgeConversationToACP(home, id); err != nil {
		t.Fatalf("BridgeConversationToACP: %v", err)
	}

	if _, err := os.Stat(filepath.Join(acpBrain, "f1.txt")); err != nil {
		t.Fatalf("f1.txt missing in ACP: %v", err)
	}
	if _, err := os.Stat(filepath.Join(acpBrain, "f4.txt")); err != nil {
		t.Fatalf("f4.txt missing in ACP: %v", err)
	}
	if _, err := os.Stat(filepath.Join(acpBrain, "f2.txt")); !os.IsNotExist(err) {
		t.Fatalf("f2.txt still present in ACP after deletion; want not exist, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(acpBrain, "sub")); !os.IsNotExist(err) {
		t.Fatalf("sub/ directory still present in ACP after deletion; want not exist, err=%v", err)
	}

	// 4. Repeated handoff back to CLI: deleted files must not be copied back
	if err := BridgeConversationToCLI(home, id); err != nil {
		t.Fatalf("second BridgeConversationToCLI: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cliBrain, "f2.txt")); !os.IsNotExist(err) {
		t.Fatalf("f2.txt resurrected in CLI after second handoff: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cliBrain, "sub")); !os.IsNotExist(err) {
		t.Fatalf("sub/ resurrected in CLI after second handoff: %v", err)
	}

	// 5. CLI removes entire brain folder: bridge must clean ACP destination brain
	if err := os.RemoveAll(cliBrain); err != nil {
		t.Fatal(err)
	}
	if err := BridgeConversationToACP(home, id); err != nil {
		t.Fatalf("BridgeConversationToACP with absent source brain: %v", err)
	}
	if _, err := os.Lstat(acpBrain); !os.IsNotExist(err) {
		t.Fatalf("ACP brain folder still present when source brain was absent; want not exist, err=%v", err)
	}
}

func TestBridgeBrainHandlesExistingSymlinksSafely(t *testing.T) {
	home := t.TempDir()
	const id = "conv-symlink-safe"

	acpDir := filepath.Join(home, "antigravity-acp")
	cliDir := filepath.Join(home, "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(acpDir, "conversations"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acpDir, "conversations", id+".db"), []byte("acp-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	acpBrain := filepath.Join(acpDir, "brain", id)
	if err := os.MkdirAll(acpBrain, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acpBrain, "data.txt"), []byte("real-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Case 1: Destination brain folder itself is a symlink pointing to an outside dir
	outsideDir := filepath.Join(home, "outside-victim-dir")
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "keep-me.txt")
	if err := os.WriteFile(outsideFile, []byte("sensitive-outside-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	cliBrainParent := filepath.Join(cliDir, "brain")
	if err := os.MkdirAll(cliBrainParent, 0o700); err != nil {
		t.Fatal(err)
	}
	cliBrain := filepath.Join(cliBrainParent, id)
	if err := os.Symlink(outsideDir, cliBrain); err != nil {
		t.Fatal(err)
	}

	if err := BridgeConversationToCLI(home, id); err != nil {
		t.Fatalf("BridgeConversationToCLI over symlinked dst brain: %v", err)
	}

	// Verify outside file was NOT removed or corrupted
	outsideContent, err := os.ReadFile(outsideFile)
	if err != nil || string(outsideContent) != "sensitive-outside-data" {
		t.Fatalf("outside file was affected: content=%q, err=%v", string(outsideContent), err)
	}

	// Verify cliBrain is now a real directory with bridged data
	info, err := os.Lstat(cliBrain)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("cliBrain should now be a real directory, got info=%v, err=%v", info, err)
	}
	bridgedData, err := os.ReadFile(filepath.Join(cliBrain, "data.txt"))
	if err != nil || string(bridgedData) != "real-content" {
		t.Fatalf("bridged data mismatch: %q, %v", string(bridgedData), err)
	}

	// Case 2: Destination brain is absent source, and destination was a symlink
	if err := os.RemoveAll(acpBrain); err != nil {
		t.Fatal(err)
	}
	// Make dstBrain a symlink again to outside dir
	if err := os.RemoveAll(cliBrain); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, cliBrain); err != nil {
		t.Fatal(err)
	}
	if err := BridgeConversationToCLI(home, id); err != nil {
		t.Fatalf("BridgeConversationToCLI with absent source brain and symlinked dst: %v", err)
	}
	if _, err := os.Lstat(cliBrain); !os.IsNotExist(err) {
		t.Fatalf("symlinked cliBrain was not removed when source was absent: %v", err)
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("outside file was deleted when absent-source cleaned symlink: %v", err)
	}
}
