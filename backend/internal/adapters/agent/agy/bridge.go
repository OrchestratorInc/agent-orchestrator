package agy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The storage roots under GEMINI_HOME where the ACP server and the terminal
// CLI keep their conversations, settings, and brain files.
const (
	ACPStorageDir = "antigravity-acp"
	CLIStorageDir = "antigravity-cli"
)

// GeminiHome resolves Antigravity's storage directory (~/.gemini or $GEMINI_HOME).
func GeminiHome(env map[string]string) (string, error) {
	if dir := strings.TrimSpace(env["GEMINI_HOME"]); dir != "" {
		return dir, nil
	}
	if dir := strings.TrimSpace(os.Getenv("GEMINI_HOME")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, ".gemini"), nil
}

// BridgeConversationToCLI ensures Antigravity conversation files created in
// antigravity-acp are accessible to antigravity-cli under geminiHome.
func BridgeConversationToCLI(geminiHome, conversationID string) error {
	id := strings.TrimSpace(conversationID)
	if id == "" {
		return nil
	}
	srcDir := filepath.Join(geminiHome, ACPStorageDir)
	dstDir := filepath.Join(geminiHome, CLIStorageDir)
	return bridgeConversation(srcDir, dstDir, id)
}

// BridgeConversationToACP ensures Antigravity conversation files created in
// antigravity-cli are accessible to antigravity-acp under geminiHome.
func BridgeConversationToACP(geminiHome, conversationID string) error {
	id := strings.TrimSpace(conversationID)
	if id == "" {
		return nil
	}
	srcDir := filepath.Join(geminiHome, CLIStorageDir)
	dstDir := filepath.Join(geminiHome, ACPStorageDir)
	return bridgeConversation(srcDir, dstDir, id)
}

func validConversationID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`)
}

func bridgeConversation(srcDir, dstDir, conversationID string) error {
	if !validConversationID(conversationID) {
		return fmt.Errorf("invalid Antigravity conversation id %q", conversationID)
	}
	// The caller must invoke this only after the source process is stopped. Copy
	// SQLite's database and sidecars together; hardlinks would let later writes
	// from one interface silently mutate the other interface's database.
	srcDB := filepath.Join(srcDir, "conversations", conversationID+".db")
	if _, err := os.Stat(srcDB); err != nil {
		return fmt.Errorf("find source conversation database %s: %w", conversationID, err)
	}
	for _, suffix := range []string{".db", ".db-wal", ".db-shm", ".meta"} {
		src := filepath.Join(srcDir, "conversations", conversationID+suffix)
		dst := filepath.Join(dstDir, "conversations", conversationID+suffix)
		if err := copyIfPresent(src, dst); err != nil {
			return fmt.Errorf("bridge conversation file %s: %w", filepath.Base(src), err)
		}
	}
	srcBrain := filepath.Join(srcDir, "brain", conversationID)
	dstBrain := filepath.Join(dstDir, "brain", conversationID)
	if info, err := os.Stat(srcBrain); err == nil && info.IsDir() {
		if err := syncBrainDir(srcBrain, dstBrain); err != nil {
			return fmt.Errorf("bridge conversation brain %s: %w", conversationID, err)
		}
	} else if os.IsNotExist(err) {
		if err := removeBrainDir(dstBrain); err != nil {
			return fmt.Errorf("clean destination conversation brain %s: %w", conversationID, err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect conversation brain %s: %w", conversationID, err)
	} else {
		return fmt.Errorf("source conversation brain %s is not a directory", conversationID)
	}

	return nil
}

func copyIfPresent(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			removeErr := os.Remove(dst)
			if os.IsNotExist(removeErr) {
				return nil
			}
			return removeErr
		}
		return err
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file")
	}

	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec // path built from a validated conversation id
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp := fmt.Sprintf("%s.tmp.%d", dst, os.Getpid())
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func removeBrainDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(dir)
	}
	return os.RemoveAll(dir)
}

func syncBrainDir(src, dst string) error {
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}

	tmp := fmt.Sprintf("%s.tmp.%d", dst, os.Getpid())
	_ = removeBrainDir(tmp)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	defer func() { _ = removeBrainDir(tmp) }()

	if err := copyDir(src, tmp); err != nil {
		return err
	}

	if err := removeBrainDir(dst); err != nil {
		return err
	}

	return os.Rename(tmp, dst)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}
