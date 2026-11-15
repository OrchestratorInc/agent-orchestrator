package preview

import (
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const previewHostLabel = "ao-preview"

const previewArtifactHostLabel = "ao-preview-artifact"

const inlineArtifactHostLabel = "ao-inline-artifact"

var ErrPreviewHostUnsupported = errors.New("session ID is too long for a preview hostname")

var entryCandidates = []string{"index.html", "public/index.html", "dist/index.html", "build/index.html"}

var previewableExts = map[string]struct{}{
	".html":     {},
	".htm":      {},
	".md":       {},
	".markdown": {},
}

const maxPreviewWalkFiles = 5000

type Entry struct {
	Path    string
	AbsPath string
	ModTime time.Time
	Size    int64
}

func DiscoverEntry(workspacePath string) (Entry, bool) {
	if entry, ok := DiscoverWebEntrypoint(workspacePath); ok {
		return entry, true
	}
	return mostRecentPreviewable(workspacePath)
}

func DiscoverWebEntrypoint(workspacePath string) (Entry, bool) {
	if strings.TrimSpace(workspacePath) == "" {
		return Entry{}, false
	}
	for _, candidate := range entryCandidates {
		if entry, ok := EntryAtPath(workspacePath, candidate); ok {
			return entry, true
		}
	}
	return Entry{}, false
}

func mostRecentPreviewable(workspacePath string) (Entry, bool) {
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return Entry{}, false
	}
	var best Entry
	found := false
	seen := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			//nolint:nilerr // skip unreadable entries rather than aborting the whole scan
			return nil
		}
		if d.IsDir() {
			if p != root && skipPreviewDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := previewableExts[strings.ToLower(filepath.Ext(d.Name()))]; !ok {
			return nil
		}
		seen++
		if seen > maxPreviewWalkFiles {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			//nolint:nilerr // skip this file, keep scanning the rest of the workspace
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		entry, ok := EntryAtPath(root, relSlash)
		if !ok {
			return nil
		}
		if !found || newerPreviewable(entry, relSlash, best) {
			best = entry
			found = true
		}
		return nil
	})
	return best, found
}

func newerPreviewable(entry Entry, relSlash string, best Entry) bool {
	mod := entry.ModTime
	if mod.After(best.ModTime) {
		return true
	}
	if mod.Equal(best.ModTime) {
		return relSlash < best.Path
	}
	return false
}

func skipPreviewDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

func IsMarkdownPath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

func ConfinedPath(workspacePath, assetPath string) (string, bool) {
	root, err := filepath.Abs(workspacePath)
	if err != nil || root == "" {
		return "", false
	}
	clean, ok := CleanWorkspacePath(assetPath)
	if !ok {
		if path.Clean("/"+filepath.ToSlash(assetPath)) != "/" {
			return "", false
		}
		clean = "index.html"
	}
	file := filepath.Join(root, filepath.FromSlash(clean))
	absFile, err := filepath.Abs(file)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, absFile)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return absFile, true
}

func FileURL(baseURL string, id domain.SessionID, entry string) (string, error) {
	return labeledFileURL(baseURL, previewHostLabel, id, entry)
}

func ArtifactFileURL(baseURL string, id domain.SessionID, entry string) (string, error) {
	return labeledFileURL(baseURL, previewArtifactHostLabel, id, entry)
}

func labeledFileURL(baseURL, label string, id domain.SessionID, entry string) (string, error) {
	u := normalizedBaseURL(baseURL)
	host, err := previewHost(u, label, id)
	if err != nil {
		return "", err
	}
	u.Host = host
	u.Path = path.Clean("/" + entry)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func InlineArtifactFileURL(baseURL string, id domain.SessionID, entry string) (string, error) {
	return labeledFileURL(baseURL, inlineArtifactHostLabel, id, entry)
}

func SessionIDFromInlineArtifactHost(rawHost string) (domain.SessionID, bool) {
	return sessionIDFromLabeledHost(rawHost, inlineArtifactHostLabel)
}

func SessionIDFromHost(rawHost string) (domain.SessionID, bool) {
	return sessionIDFromLabeledHost(rawHost, previewHostLabel)
}

func SessionIDFromArtifactHost(rawHost string) (domain.SessionID, bool) {
	return sessionIDFromLabeledHost(rawHost, previewArtifactHostLabel)
}

func sessionIDFromLabeledHost(rawHost, label string) (domain.SessionID, bool) {
	host := rawHost
	if parsedHost, _, err := net.SplitHostPort(rawHost); err == nil {
		host = parsedHost
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || len(host) > 253 {
		return "", false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 3 || labels[0] != label || labels[len(labels)-1] != "localhost" {
		return "", false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return "", false
		}
	}
	encoded := strings.Join(labels[1:len(labels)-1], "")
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(encoded))
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) {
		return "", false
	}
	return domain.SessionID(decoded), true
}

func previewHost(u url.URL, label string, id domain.SessionID) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%w: empty session ID", ErrPreviewHostUnsupported)
	}
	encoded := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(id)))
	labels := []string{label}
	const maxChunk = 50
	for encoded != "" {
		n := min(len(encoded), maxChunk)
		labels = append(labels, encoded[:n])
		encoded = encoded[n:]
	}
	labels = append(labels, "localhost")
	host := strings.Join(labels, ".")
	if len(host) > 253 {
		return "", fmt.Errorf("%w: encoded hostname is %d characters", ErrPreviewHostUnsupported, len(host))
	}
	if port := u.Port(); port != "" {
		return host + ":" + port, nil
	}
	return host, nil
}

func normalizedBaseURL(raw string) url.URL {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		raw = "http://127.0.0.1:3001"
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return url.URL{Scheme: "http", Host: raw}
	}
	return *u
}
