package hookutil

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

const GitignoreSentinel = "# managed by agent-orchestrator: AO hook files stay out of git status"

const (
	bugTriageMarkerFile = ".ao-managed"
	bugTriageSentinel   = "agent-orchestrator: managed bug-triage skill"
)

func InstallBugTriageSkill(skillsDir string) error {
	skillDir := filepath.Join(skillsDir, skillassets.BugTriageSkillName)
	if _, err := os.Lstat(skillDir); err == nil {
		managed, err := bugTriageManaged(skillDir)
		if err != nil {
			return err
		}
		if !managed {
			entries, err := os.ReadDir(skillDir)
			if err != nil {
				return fmt.Errorf("read bug-triage skill: %w", err)
			}
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".ao-tmp-") {
					return nil
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat bug-triage skill: %w", err)
	}
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		return fmt.Errorf("create bug-triage skill: %w", err)
	}
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return fmt.Errorf("read bug-triage skill: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".ao-tmp-") {
			continue
		}
		if err := os.Remove(filepath.Join(skillDir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove bug-triage temp file: %w", err)
		}
	}
	if err := AtomicWriteFile(filepath.Join(skillDir, bugTriageMarkerFile), []byte(bugTriageSentinel+"\n"), 0o600); err != nil {
		return fmt.Errorf("write bug-triage marker: %w", err)
	}
	if err := AtomicWriteFile(filepath.Join(skillDir, "SKILL.md"), skillassets.BugTriageSkill(), 0o600); err != nil {
		return fmt.Errorf("write bug-triage skill: %w", err)
	}
	if err := EnsureWorkspaceGitignore(skillDir, "SKILL.md", bugTriageMarkerFile); err != nil {
		return fmt.Errorf("bug-triage gitignore: %w", err)
	}
	return nil
}

func UninstallBugTriageSkill(skillsDir string) error {
	skillDir := filepath.Join(skillsDir, skillassets.BugTriageSkillName)
	managed, err := bugTriageManaged(skillDir)
	if err != nil || !managed {
		return err
	}
	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("remove bug-triage skill: %w", err)
	}
	return nil
}

func bugTriageManaged(skillDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(skillDir, bugTriageMarkerFile)) //nolint:gosec // path is rooted in the caller-owned workspace
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read bug-triage marker: %w", err)
	}
	return strings.Contains(string(data), bugTriageSentinel), nil
}

const CopilotAgentProfileSentinel = "<!-- managed by agent-orchestrator: copilot agent profile -->"

func EnsureWorkspaceGitignore(dir string, names ...string) error {
	path := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err == nil && !strings.Contains(string(existing), GitignoreSentinel) {
		return nil
	}
	var b strings.Builder
	b.WriteString(GitignoreSentinel)
	b.WriteString("\n/.gitignore\n")
	for _, name := range names {
		b.WriteString("/")
		b.WriteString(filepath.ToSlash(name))
		b.WriteString("\n")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := AtomicWriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func statRegularFile(path string) (os.FileInfo, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, false
	}
	return info, true
}

func FileExists(path string) bool {
	_, ok := statRegularFile(path)
	return ok
}

func IsExecutableFile(path string) bool {
	info, ok := statRegularFile(path)
	if !ok {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		return false
	}
	return true
}

func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ao-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

const FinalAssistantTextJS = `// The run's final assistant text. The event shape is the agent's, so anything
// unexpected only means "no text".
function finalAssistantText(event) {
  try {
    for (const message of [...(event?.messages ?? [])].reverse()) {
      if (message?.role !== "assistant") continue;
      const parts = typeof message.content === "string" ? [{ type: "text", text: message.content }] : message.content;
      const text = (Array.isArray(parts) ? parts : []).filter((part) => part?.type === "text" && typeof part.text === "string").map((part) => part.text).join("\n");
      if (text.trim()) return text;
    }
  } catch {}
  return "";
}
`

func ReadJSONLTail(path string, window int64) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // a transcript path reported by the harness's own hook
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	offset := info.Size() - window
	if offset < 0 {
		offset = 0
	}
	data, err := io.ReadAll(io.NewSectionReader(file, offset, info.Size()-offset))
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		first := bytes.IndexByte(data, '\n')
		if first < 0 {
			return nil, nil
		}
		data = data[first+1:]
	}
	last := bytes.LastIndexByte(data, '\n')
	if last < 0 {
		return nil, nil
	}
	return data[:last+1], nil
}
