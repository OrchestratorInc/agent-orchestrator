package sessionartifacts

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ShareLinksFile is where the desktop records the links it published.
const ShareLinksFile = "share-links.json"

// ShareLinks reads the public links of published artifacts.
type ShareLinks struct {
	path    string
	baseURL string

	mu      sync.Mutex
	loaded  bool
	size    int64
	modTime time.Time
	urls    map[domain.SessionID]sessionShareLinks
}

// sessionShareLinks are one session's links, by artifact ID and, for records
// written before artifacts had one, by path.
type sessionShareLinks struct {
	byID   map[string]string
	byPath map[string]string
}

// NewShareLinks reads dataDir's share links for one control plane.
func NewShareLinks(dataDir, controlPlaneURL string) *ShareLinks {
	return &ShareLinks{path: filepath.Join(dataDir, ShareLinksFile), baseURL: controlPlaneURL}
}

// Apply sets ShareURL on each file that has a public link.
func (l *ShareLinks) Apply(id domain.SessionID, files []domain.SessionArtifactFile) {
	if l == nil || l.baseURL == "" || len(files) == 0 {
		return
	}
	urls := l.load()[id]
	for i := range files {
		if link, ok := urls.byID[files[i].ID]; ok && files[i].ID != "" {
			files[i].ShareURL = link
			continue
		}
		files[i].ShareURL = urls.byPath[files[i].Path]
	}
}

func (l *ShareLinks) load() map[domain.SessionID]sessionShareLinks {
	l.mu.Lock()
	defer l.mu.Unlock()
	info, err := os.Stat(l.path)
	if err != nil {
		l.loaded, l.urls = false, nil
		return nil
	}
	if l.loaded && info.Size() == l.size && info.ModTime().Equal(l.modTime) {
		return l.urls
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		l.loaded, l.urls = false, nil
		return nil
	}
	l.loaded, l.size, l.modTime = true, info.Size(), info.ModTime()
	l.urls = parseShareLinks(data, l.baseURL)
	return l.urls
}

func parseShareLinks(data []byte, baseURL string) map[domain.SessionID]sessionShareLinks {
	var records map[string]json.RawMessage
	if json.Unmarshal(data, &records) != nil {
		return nil
	}
	urls := map[domain.SessionID]sessionShareLinks{}
	for key, raw := range records {
		// [baseURL, session, path] or [baseURL, session, "", artifact ID].
		var parts []string
		if json.Unmarshal([]byte(key), &parts) != nil || len(parts) < 3 || len(parts) > 4 || parts[0] != baseURL {
			continue
		}
		var record struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(raw, &record) != nil || !isWebURL(record.URL) {
			continue
		}
		id := domain.SessionID(parts[1])
		links, ok := urls[id]
		if !ok {
			links = sessionShareLinks{byID: map[string]string{}, byPath: map[string]string{}}
			urls[id] = links
		}
		switch {
		case len(parts) == 4 && parts[2] == "" && parts[3] != "":
			links.byID[parts[3]] = record.URL
		case len(parts) == 3:
			links.byPath[parts[2]] = record.URL
		}
	}
	return urls
}

func isWebURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
