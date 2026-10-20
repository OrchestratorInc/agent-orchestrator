package triage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func readRetainedContext(ctx context.Context, dataDir string, origin Origin) ([]Evidence, error) {
	if !filepath.IsAbs(dataDir) || !localContextAllowed(origin) || origin.SessionID != "" && !validRunID(origin.SessionID) {
		return nil, errors.New("selected session is not local or has an invalid identity")
	}
	db, err := sqlite.OpenReadOnly(ctx, dataDir)
	if err != nil {
		id := "retained-session"
		if origin.SessionID == "" {
			id = "session-candidates"
		}
		return []Evidence{unavailableItem(id, "existing local SQLite records", err.Error())}, err
	}
	defer func() { _ = db.Close() }()
	roots, _ := db.ReadTriageWorkspaceRoots(ctx, domain.SessionID(origin.SessionID))
	paths := make([]string, 0, len(roots)+1)
	for _, root := range roots {
		paths = append(paths, root.Path)
	}
	var evidence []Evidence
	for _, part := range db.ReadTriageSnapshot(ctx, domain.SessionID(origin.SessionID)) {
		if part.Err != nil {
			evidence = append(evidence, unavailableItem(part.Kind, "selected local SQLite records", part.Err.Error()))
			continue
		}
		entries := make([]map[string]any, 0, len(part.Entries))
		for _, raw := range part.Entries {
			var entry map[string]any
			if err := json.Unmarshal(raw, &entry); err != nil {
				return evidence, err
			}
			if path, ok := entry["workspacePath"].(string); ok {
				paths = append(paths, path)
			}
			delete(entry, "workspacePath")
			for _, key := range []string{"detail", "payload"} {
				if text, ok := entry[key].(string); ok {
					var value any
					if decodeJSON([]byte(text), &value, false) == nil {
						entry[key] = value
					} else {
						entry[key] = "Invalid or truncated JSON excerpt omitted."
					}
				}
			}
			entries = append(entries, entry)
		}
		raw, err := json.Marshal(entries)
		if err != nil {
			return evidence, err
		}
		for _, path := range paths {
			if path != "" {
				encoded, _ := json.Marshal(path)
				raw = []byte(strings.ReplaceAll(string(raw), string(encoded[1:len(encoded)-1]), "[workspace]"))
			}
		}
		evidence = append(evidence, Evidence{ID: part.Kind, Kind: part.Kind, Source: "selected local SQLite records", CollectedAt: time.Now().UTC(), Status: "collected", Text: "Bounded retained excerpts. Fields and older entries may be omitted; these records are not live measurements.", Values: map[string]any{"entries": json.RawMessage(raw), "bounded": true}})
	}
	return evidence, ctx.Err()
}

// InvestigationPaths gives native read tools complete, registered local roots.
func InvestigationPaths(ctx context.Context, dataDir string, origin Origin) map[string]string {
	paths := map[string]string{}
	if !filepath.IsAbs(dataDir) || !validRunID(origin.SessionID) || !localContextAllowed(origin) {
		return paths
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	db, err := sqlite.OpenReadOnly(ctx, dataDir)
	if err != nil {
		return paths
	}
	defer func() { _ = db.Close() }()
	roots, err := db.ReadTriageWorkspaceRoots(ctx, domain.SessionID(origin.SessionID))
	if err != nil {
		return paths
	}
	for _, root := range roots {
		key := "workspace:" + root.Name
		if root.Name == domain.RootWorkspaceRepoName {
			key = "workspace"
		} else if root.Name == "." || root.Name == ".." || !retainedRepoName.MatchString(root.Name) {
			continue
		}
		if path, ok := validatedWorkspacePath(dataDir, root.Path); ok {
			paths[key] = path
		}
	}
	return paths
}

func validatedWorkspacePath(dataDir, path string) (string, bool) {
	if path == "" || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	canonical = filepath.Clean(canonical)
	if filepath.Dir(canonical) == canonical {
		return "", false
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == canonical {
		return "", false
	}
	if sensitivePath(canonical) {
		return "", false
	}
	if dataRoot, err := filepath.EvalSymlinks(dataDir); err == nil {
		dataRoot = filepath.Clean(dataRoot)
		worktrees := filepath.Join(dataRoot, "worktrees")
		if pathContains(canonical, dataRoot) || canonical == dataRoot || pathContains(dataRoot, canonical) && (canonical == worktrees || !pathContains(canonical, worktrees)) {
			return "", false
		}
	}
	return canonical, true
}

func sensitivePath(path string) bool {
	for _, part := range strings.Split(strings.ToLower(filepath.Clean(path)), string(filepath.Separator)) {
		switch part {
		case ".ssh", ".aws", ".azure", ".config", ".codex", ".claude", ".npmrc", ".netrc", "credentials", "browser", "browser profiles", "cookies", "local storage", "session storage", "login data", "web data":
			return true
		}
		if strings.HasPrefix(part, ".env") || strings.Contains(part, "credential") || strings.HasPrefix(part, "id_rsa") || strings.HasPrefix(part, "id_ed25519") {
			return true
		}
	}
	return false
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

var retainedRepoName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func unavailableItem(id, source, text string) Evidence {
	return Evidence{ID: id, Kind: id, Source: source, CollectedAt: time.Now().UTC(), Status: "unavailable", Text: text}
}
