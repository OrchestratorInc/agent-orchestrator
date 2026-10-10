// Package host embeds CLIProxyAPI behind AO's exact-session routing boundary.
package host

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

var ErrBusy = errors.New("session has an in-flight model request")
var ErrRevision = errors.New("routing revision conflict")

// Route carries a hashed capability, never a provider credential.
type Route struct {
	SessionID  string `json:"session_id"`
	TicketHash string `json:"ticket_hash"`
	Provider   string `json:"provider"`
	AuthID     string `json:"auth_id"`
}
type Snapshot struct {
	Revision        uint64   `json:"revision"`
	Routes          []Route  `json:"routes"`
	AuthIDs         []string `json:"auth_ids,omitempty"`
	RequestBoundary bool     `json:"request_boundary,omitempty"`
}

// Routes serializes AO's routing snapshots with request admission. AO's database
// owns routing; the file here is only a restart cache that AO can always refill.
// In-flight requests retain their selected account; provider rebinds may apply at
// the next request.
type Routes struct {
	mu         sync.Mutex
	path       string
	snapshot   Snapshot
	byTicket   map[string]Route
	active     map[string]int
	activeAuth map[string]int
}

func OpenRoutes(path string) (*Routes, error) {
	r := &Routes{snapshot: Snapshot{Routes: []Route{}}, path: path, byTicket: make(map[string]Route), active: make(map[string]int), activeAuth: make(map[string]int)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return r, nil
	}
	// Unusable cache content is discarded, never served: every ticket is denied
	// until AO's next push refills the routes.
	if err = json.Unmarshal(data, &r.snapshot); err == nil {
		r.byTicket, err = validateSnapshot(r.snapshot)
	}
	if err != nil {
		log.Printf("discarding unusable routing cache %s: %v", path, err)
		r.snapshot, r.byTicket = Snapshot{Routes: []Route{}}, make(map[string]Route)
	}
	return r, nil
}
func TicketHash(ticket string) string {
	sum := sha256.Sum256([]byte(ticket))
	return hex.EncodeToString(sum[:])
}
func validateSnapshot(s Snapshot) (map[string]Route, error) {
	index := make(map[string]Route, len(s.Routes))
	sessions := make(map[string]bool, len(s.Routes))
	for _, route := range s.Routes {
		if s.AuthIDs != nil && route.AuthID != "" && !slices.Contains(s.AuthIDs, route.AuthID) {
			return nil, errors.New("route account absent from signed-in inventory")
		}
		hash, err := hex.DecodeString(route.TicketHash)
		if err != nil || len(hash) != 32 || strings.ToLower(route.TicketHash) != route.TicketHash || route.SessionID == "" || (route.Provider != "codex" && route.Provider != "claude") {
			return nil, errors.New("invalid session route")
		}
		if _, ok := index[route.TicketHash]; ok || sessions[route.SessionID] {
			return nil, errors.New("duplicate session route")
		}
		index[route.TicketHash] = route
		sessions[route.SessionID] = true
	}
	return index, nil
}
func (r *Routes) Apply(s Snapshot) error {
	requestBoundary := s.RequestBoundary
	s.RequestBoundary = false // Admission instruction, not an effective routing fact.
	index, err := validateSnapshot(s)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Exact replay is safe after a lost acknowledgement; mismatched replay is not.
	old, _ := json.Marshal(r.snapshot)
	next, _ := json.Marshal(s)
	if s.Revision == r.snapshot.Revision && string(old) == string(next) {
		return nil
	}
	// Any newer revision from AO replaces this cache, so a lost or stale file
	// heals on AO's next push. An older or mismatched same-revision snapshot is
	// still refused.
	if s.Revision <= r.snapshot.Revision {
		return ErrRevision
	}
	for _, authID := range r.snapshot.AuthIDs {
		if !slices.Contains(s.AuthIDs, authID) && r.activeAuth[authID] > 0 {
			return ErrBusy
		}
	}
	for hash, route := range r.byTicket {
		if index[hash] != route && r.active[route.SessionID] > 0 {
			next := index[hash]
			if requestBoundary && slices.Contains(s.AuthIDs, route.AuthID) && slices.Contains(s.AuthIDs, next.AuthID) && (route.Provider == "codex" || route.Provider == "claude") && next.Provider == route.Provider && next.SessionID == route.SessionID && next.TicketHash == route.TicketHash {
				continue
			}
			return ErrBusy
		}
	}
	if err = writePrivate(r.path, next); err != nil {
		return err
	}
	r.snapshot = s
	r.snapshot.Routes = slices.Clone(s.Routes)
	r.snapshot.AuthIDs = slices.Clone(s.AuthIDs)
	r.byTicket = index
	return nil
}
func (r *Routes) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := r.snapshot
	result.Routes = slices.Clone(result.Routes)
	result.AuthIDs = slices.Clone(result.AuthIDs)
	return result
}
func (r *Routes) Acquire(ticket string) (Route, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	route, ok := r.byTicket[TicketHash(ticket)]
	if !ok || ticket == "" {
		return Route{}, nil, errors.New("unknown session ticket")
	}
	if route.AuthID == "" {
		return Route{}, nil, errors.New("login required")
	}
	r.active[route.SessionID]++
	r.activeAuth[route.AuthID]++
	var once sync.Once
	return route, func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.active[route.SessionID]--
			r.activeAuth[route.AuthID]--
			if r.activeAuth[route.AuthID] == 0 {
				delete(r.activeAuth, route.AuthID)
			}
			if r.active[route.SessionID] == 0 {
				delete(r.active, route.SessionID)
			}
		})
	}, nil
}
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".routes-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
