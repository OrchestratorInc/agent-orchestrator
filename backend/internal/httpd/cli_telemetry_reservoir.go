package httpd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/telemetrymeta"
)

const cliTelemetryStateFile = "telemetry_cli_daily.json"

type cliTelemetryReservoir struct {
	mu sync.Mutex

	path        string
	activeDay   string
	activeDone  bool
	invokedDay  string
	invokedSeen map[string]struct{}
}

type cliTelemetryState struct {
	ActiveDay string `json:"active_day"`
	// ActiveDone replaced ActiveSlots when the heartbeat moved from four
	// six-hour slots to one report per UTC day. ActiveSlots is still written so
	// a downgraded build reads the day as fully spent instead of emitting again.
	ActiveDone  bool     `json:"active_done"`
	ActiveSlots []int    `json:"active_slots"`
	InvokedDay  string   `json:"invoked_day"`
	InvokedSeen []string `json:"invoked_seen"`
}

func newCLITelemetryReservoir(dataDir string) *cliTelemetryReservoir {
	r := &cliTelemetryReservoir{
		invokedSeen: make(map[string]struct{}),
	}
	if dataDir != "" {
		r.path = filepath.Join(dataDir, cliTelemetryStateFile)
		r.load()
	}
	return r
}

// reserveActive hands out one ao.app.active report per UTC day. It was four per
// day, one per six-hour slot, which cost four events per install for a number
// that is a unique count and therefore never changed.
func (r *cliTelemetryReservoir) reserveActive(now time.Time) bool {
	day := telemetryUTCDate(now)
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.activeDay != day {
		r.activeDay = day
		r.activeDone = false
	}
	if r.activeDone {
		return false
	}
	r.activeDone = true
	_ = r.saveLocked()
	return true
}

func (r *cliTelemetryReservoir) reserveInvoked(now time.Time, actorType, commandPath string) bool {
	day := telemetryUTCDate(now)
	key := cliInvokedReservationKey(actorType, commandPath)
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.invokedDay != day {
		r.invokedDay = day
		r.invokedSeen = make(map[string]struct{})
	}
	if _, seen := r.invokedSeen[key]; seen {
		return false
	}
	r.invokedSeen[key] = struct{}{}
	_ = r.saveLocked()
	return true
}

func telemetryUTCDate(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

func cliInvokedReservationKey(actorType, commandPath string) string {
	return actorType + "\t" + telemetrymeta.NormalizeCommandPath(commandPath)
}

func (r *cliTelemetryReservoir) load() {
	if r.path == "" {
		return
	}
	b, err := os.ReadFile(r.path)
	if err != nil {
		return
	}
	var st cliTelemetryState
	if err := json.Unmarshal(b, &st); err != nil {
		return
	}
	r.activeDay = st.ActiveDay
	// A slot record written by an older build means that day already reported,
	// so an upgrade mid-day cannot hand out a second reservation.
	r.activeDone = st.ActiveDone || len(st.ActiveSlots) > 0 || st.ActiveDay != ""
	r.invokedDay = st.InvokedDay
	r.invokedSeen = make(map[string]struct{}, len(st.InvokedSeen))
	for _, commandPath := range st.InvokedSeen {
		if commandPath != "" {
			r.invokedSeen[commandPath] = struct{}{}
		}
	}
}

func (r *cliTelemetryReservoir) saveLocked() error {
	if r.path == "" {
		return nil
	}
	activeSlots := []int{}
	if r.activeDone {
		activeSlots = []int{0, 1, 2, 3}
	}
	seen := make([]string, 0, len(r.invokedSeen))
	for commandPath := range r.invokedSeen {
		seen = append(seen, commandPath)
	}
	body, err := json.Marshal(cliTelemetryState{
		ActiveDay:   r.activeDay,
		ActiveDone:  r.activeDone,
		ActiveSlots: activeSlots,
		InvokedDay:  r.invokedDay,
		InvokedSeen: seen,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".telemetry-cli-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, r.path); err != nil {
		if removeErr := os.Remove(r.path); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		return os.Rename(tmpName, r.path)
	}
	return nil
}
