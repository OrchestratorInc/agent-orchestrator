// Package awake owns desktop wake intent independently of Connect Mobile.
package awake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Store persists desktop wake intent.
type Store interface {
	LoadAwake(context.Context) (string, error)
	SaveAwake(context.Context, string) error
}

// Host defines native ownership and observation operations.
type Host interface {
	Supported() bool
	Active() bool
	Start() error
	Stop()
	ConfigureLid(bool, bool) error
	SetupLid() error
	LidStatus() (bool, bool, string)
	ConfigureIdle(time.Time, bool) error
	SetDeadline(time.Time) error
	SetProtection(int) error
	ConfigureEffects(domain.AwakeSettings) error
	RequestRecovery() error
	RecoveryReason() string
	RelaunchBrowser(context.Context, string) error
	JoinNetwork(context.Context, string) error
	Observe(context.Context) (domain.AwakeObservation, error)
	Capabilities() []domain.AwakeCapability
}

// Bindings resolves session and application ownership.
type Bindings interface {
	Alive(context.Context, string, string, string) (bool, string, error)
	AgentActive(context.Context) (bool, error)
}

// Service serializes requests, rules, and recovery.
type Service struct {
	mu          sync.Mutex
	store       Store
	host        Host
	bindings    Bindings
	doc         domain.AwakeDocument
	observation domain.AwakeObservation
	deadlines   map[string]time.Time
	activeRules []string
	ruleMatches map[string]ruleMatch
	started     *time.Time
	reason      string
	now         func() time.Time
	// Download events are fed by the desktop, expire independently of the renderer.
	downloads     map[string]bool
	downloadsSeen time.Time
	networkOutage time.Time
	networkBusy   bool
	closed        bool
	workers       sync.WaitGroup
	networkCancel context.CancelFunc
	agentCancel   context.CancelFunc
	agentBusy     bool

	// Reconciliation keeps failed document changes in memory; retry until durable.
	reconcileSavePending   bool
	powerAuthorizationBusy bool
}

// ErrValidation identifies invalid wake requests.
var ErrValidation = errors.New("invalid stay-awake request")

// New loads intent and ends interrupted manual requests.
func New(ctx context.Context, store Store, host Host, bindings Bindings) (*Service, error) {
	s := &Service{store: store, host: host, bindings: bindings, now: time.Now, deadlines: map[string]time.Time{}, downloads: map[string]bool{}, ruleMatches: map[string]ruleMatch{}}
	raw, err := store.LoadAwake(ctx)
	if err != nil {
		return nil, err
	}
	s.doc = domain.AwakeDocument{Version: 1, Settings: domain.AwakeSettings{LidClosed: true, StopBatteryPercent: 10, PauseApps: []string{}}, Sessions: []domain.AwakeSession{}, Rules: []domain.AwakeRule{}}
	if raw == "{}" {
		s.doc.Settings.LidClosed, _, _ = host.LidStatus()
	}
	if raw != "{}" {
		if err = json.Unmarshal([]byte(raw), &s.doc); err != nil {
			return nil, fmt.Errorf("read awake intent: %w", err)
		}
		if s.doc.Version != 1 {
			return nil, errors.New("unsupported awake settings version")
		}
	}
	// Older PR builds could persist deferred opt-ins. Disable them durably
	// before any automatic rule or host action is allowed to run.
	changed := clearDeferredSettings(&s.doc.Settings)
	// A daemon restart never resurrects an unbounded manual request.
	for i := range s.doc.Sessions {
		if s.doc.Sessions[i].EndedAt == nil {
			now := s.now()
			s.doc.Sessions[i].EndedAt = &now
			s.doc.Sessions[i].EndReason = "interrupted"
			changed = true
		}
	}
	if changed {
		if err := s.save(ctx); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func clone(doc domain.AwakeDocument) domain.AwakeDocument {
	data, _ := json.Marshal(doc)
	var out domain.AwakeDocument
	_ = json.Unmarshal(data, &out)
	return out
}
func (s *Service) save(ctx context.Context) error {
	data, err := json.Marshal(s.doc)
	if err != nil {
		return err
	}
	if err := s.store.SaveAwake(ctx, string(data)); err != nil {
		return err
	}
	// Every successful save includes any pending reconciliation changes.
	s.reconcileSavePending = false
	return nil
}

// Status returns current wake state.
func (s *Service) Status() domain.AwakeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := clone(s.doc)
	if d.Sessions == nil {
		d.Sessions = []domain.AwakeSession{}
	}
	if d.Rules == nil {
		d.Rules = []domain.AwakeRule{}
	}
	if d.Settings.PauseApps == nil {
		d.Settings.PauseApps = []string{}
	}
	_, lidActive, lidReason := s.host.LidStatus()
	reason := s.reason
	if recovery := s.host.RecoveryReason(); reason == "" && recovery != "" {
		reason = recovery
	}
	if reason == "" && lidReason != "" && lidReason != "setup_needed" {
		reason = lidReason
	}
	rules := append([]string{}, s.activeRules...)
	observation := s.observation
	observation.NetworkAddresses = append([]string{}, observation.NetworkAddresses...)
	observation.Devices = append([]string{}, observation.Devices...)
	observation.Applications = append([]domain.AwakeApplication{}, observation.Applications...)
	observation.Browsers = append([]domain.AwakeApplication{}, observation.Browsers...)
	observation.KnownNetworks = append([]string{}, observation.KnownNetworks...)
	return domain.AwakeState{Settings: d.Settings, Sessions: d.Sessions, Rules: d.Rules, ActiveRules: rules, Capabilities: s.capabilities(), Observation: observation, Active: s.host.Active(), LidActive: lidActive, Reason: reason, Paused: d.Paused, StartedAt: s.started, NetworkOutcome: d.Network.Outcome, AgentOutcome: d.AgentOutcome}
}
func invalid(message string) error { return fmt.Errorf("%w: %s", ErrValidation, message) }

// Start starts a wake request.
func (s *Service) Start(ctx context.Context, req domain.StartAwakeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if !s.host.Supported() {
		return errors.New("stay awake requires macOS")
	}
	s.refresh(ctx)
	if s.doc.StopReason != "" {
		return invalid("resume after the safety stop before starting another session")
	}
	if req.Scope == "" {
		req.Scope = "ao"
	}
	if req.Mode == "" {
		req.Mode = "indefinite"
	}
	if len(req.SessionID) > 256 || len(req.BoundID) > 256 || len(req.BoundIdentity) > 128 {
		return invalid("awake binding is too long")
	}
	if req.Scope == "ao" {
		req.SessionID = ""
	}
	if req.Scope != "ao" && req.Scope != "session" {
		return invalid("choose AO or this session")
	}
	if req.Scope == "session" && req.SessionID == "" {
		return invalid("session is required")
	}
	if req.Scope == "session" {
		ok, _, err := s.bindings.Alive(ctx, "session", req.SessionID, "")
		if err != nil {
			return err
		}
		if !ok {
			return invalid("session is no longer running")
		}
	}
	now := s.now()
	source := domain.AwakeSession{ID: uuid.NewString(), Scope: req.Scope, SessionID: req.SessionID, Mode: req.Mode, CreatedAt: now, Label: "For AO", LidClosed: req.LidClosed, BoundID: req.BoundID}
	if req.Scope == "session" {
		source.Label = "For this session"
	}
	switch req.Mode {
	case "indefinite":
	case "duration":
		if req.DurationSeconds < 1 || req.DurationSeconds > 604800 {
			return invalid("duration must be between one second and seven days")
		}
		deadline := now.Add(time.Duration(req.DurationSeconds) * time.Second)
		source.Deadline = &deadline
	case "until":
		if req.Until == nil || !req.Until.After(now) || req.Until.Sub(now) > 7*24*time.Hour {
			return invalid("choose an end time within seven days")
		}
		deadline := now.Add(req.Until.Sub(now))
		source.Deadline = &deadline
	case "app":
		if req.BoundID == "" {
			return invalid("choose a running application")
		}
		alive, identity, err := s.bindings.Alive(ctx, "app", req.BoundID, req.BoundIdentity)
		if err != nil {
			return err
		}
		if !alive {
			return invalid("application is not running")
		}
		source.BoundIdentity = identity
	case "download":
		if !s.downloadActive(req.BoundID, now) {
			return invalid("choose an active AO browser download")
		}
	default:
		return invalid("unknown awake session mode")
	}
	s.pruneEndedSessions()
	if len(s.doc.Sessions) >= maxAwakeSessions {
		return invalid("too many active awake sessions; end some first")
	}
	nativeGuard := false
	for _, cap := range s.host.Capabilities() {
		if cap.ID == "native_guard" {
			nativeGuard = cap.Available
		}
	}
	if req.LidClosed {
		supported, _, reason := s.host.LidStatus()
		if !supported {
			return fmt.Errorf("closed-lid wake is unavailable: %s", reason)
		}
	}
	if req.LidClosed || nativeGuard {
		if err := s.host.SetupLid(); err != nil && (req.LidClosed || !errors.Is(err, ErrLidHelperUpgrade)) {
			return err
		}
	}
	previous := clone(s.doc)
	// A new explicit request can rearm only after fresh observations are safe.
	s.refresh(ctx)
	if reason := s.safety(); reason != "" {
		return fmt.Errorf("stay awake stopped: %s", reason)
	}
	s.doc.Sessions = append(s.doc.Sessions, source)
	if source.Deadline != nil {
		s.deadlines[source.ID] = *source.Deadline
	}
	if err := s.save(ctx); err != nil {
		s.doc = previous
		delete(s.deadlines, source.ID)
		return err
	}
	if err := s.reconcile(ctx); err != nil {
		s.doc = previous
		delete(s.deadlines, source.ID)
		rollback := s.save(ctx)
		_ = s.reconcile(ctx)
		return errors.Join(err, rollback)
	}
	return nil
}

// End ends one owned request.
func (s *Service) End(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	previous := clone(s.doc)
	found := false
	for i := range s.doc.Sessions {
		source := &s.doc.Sessions[i]
		if source.ID == id && source.EndedAt == nil {
			now := s.now()
			source.EndedAt = &now
			source.EndReason = "ended"
			found = true
		}
	}
	if !found {
		return invalid("awake session not found")
	}
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	return s.reconcile(ctx)
}

// Extend extends a finite request.
func (s *Service) Extend(ctx context.Context, id string, seconds int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if seconds < 1 || seconds > 604800 {
		return invalid("extension must be within seven days")
	}
	for i := range s.doc.Sessions {
		source := &s.doc.Sessions[i]
		if source.ID != id || source.EndedAt != nil {
			continue
		}
		original, finite := s.deadlines[id]
		if !finite || !original.After(s.now()) {
			return invalid("only an unexpired timed session can be extended")
		}
		previous := clone(s.doc)
		base := s.now()
		if old, ok := s.deadlines[id]; ok && old.After(base) {
			base = old
		}
		end := base.Add(time.Duration(seconds) * time.Second)
		if end.Sub(s.now()) > 7*24*time.Hour {
			return invalid("maximum duration is seven days")
		}
		source.Deadline = &end
		source.Mode = "duration"
		if err := s.save(ctx); err != nil {
			s.doc = previous
			return err
		}
		s.deadlines[id] = end
		return s.reconcile(ctx)
	}
	return invalid("awake session not found")
}

// StopAll ends desktop requests and pauses rules.
func (s *Service) StopAll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	previous := clone(s.doc)
	now := s.now()
	for i := range s.doc.Sessions {
		if s.doc.Sessions[i].EndedAt == nil {
			s.doc.Sessions[i].EndedAt = &now
			s.doc.Sessions[i].EndReason = "stopped"
		}
	}
	s.doc.Paused = true
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	return s.reconcile(ctx)
}

// Resume checks fresh host readings before clearing a safety stop.
func (s *Service) Resume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	s.refresh(ctx)
	if reason := s.safety(); reason != "" {
		return fmt.Errorf("cannot rearm: %s", reason)
	}
	if s.doc.StopReason != "" && (s.observation.Thermal == "" || s.observation.Thermal == "unknown" || s.observation.OnBattery == nil || (*s.observation.OnBattery && s.observation.BatteryPercent == nil)) {
		return errors.New("fresh battery and thermal observations are required to rearm")
	}

	previous := clone(s.doc)
	s.doc.Paused = false
	s.doc.StopReason = ""
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	return s.reconcile(ctx)
}

// clearDeferredSettings returns whether a previous build enabled a policy that
// is outside this release's supported surface. Low Power Mode and display wake
// assertions remain supported; optional brightness/audio/app/network effects do not.
func clearDeferredSettings(settings *domain.AwakeSettings) bool {
	changed := settings.MuteAudio || settings.DarkenKeyboard || settings.RestoreDisplay ||
		len(settings.PauseApps) != 0 || settings.HotspotName != "" || settings.HotspotRecovery ||
		settings.AgentReconnect || settings.BrowserActions
	settings.MuteAudio, settings.DarkenKeyboard, settings.RestoreDisplay = false, false, false
	settings.PauseApps = []string{}
	settings.HotspotName, settings.HotspotRecovery = "", false
	settings.AgentReconnect, settings.BrowserActions = false, false
	return changed
}

// UpdateSettings validates and persists host policy.
func (s *Service) UpdateSettings(ctx context.Context, settings domain.AwakeSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if s.powerAuthorizationBusy && (settings.LowPowerBatteryPercent != 0 || settings.LowPowerOnSeriousHeat) {
		return invalid("wait for the Low Power Mode authorization action to finish")
	}
	supported := settings
	if clearDeferredSettings(&supported) {
		return invalid("App pausing, display/keyboard/audio effects, network recovery, agent reconnect and browser actions are deferred to a follow-up release")
	}
	if settings.LowPowerBatteryPercent != 0 && (settings.LowPowerBatteryPercent < 10 || settings.LowPowerBatteryPercent > 90) {
		return invalid("Low Power Mode threshold must be between 10 and 90 percent")
	}
	if settings.StopBatteryPercent < 10 || settings.StopBatteryPercent > 90 {
		return invalid("battery stop threshold must be between 10 and 90 percent")
	}
	// An unavailable operation is rejected rather than saved as if effective.
	if len(settings.PauseApps) > 20 {
		return invalid("choose at most twenty background apps")
	}
	for _, app := range settings.PauseApps {
		if app == "" || len(app) > 256 {
			return invalid("invalid application bundle ID")
		}
	}
	addedApps := false
	for _, app := range settings.PauseApps {
		if !slices.Contains(s.doc.Settings.PauseApps, app) {
			addedApps = true
		}
	}
	requested := map[string]bool{"lid": settings.LidClosed && !s.doc.Settings.LidClosed, "display_awake": settings.DisplayAwake && !s.doc.Settings.DisplayAwake, "low_power": (settings.LowPowerBatteryPercent > 0 && settings.LowPowerBatteryPercent != s.doc.Settings.LowPowerBatteryPercent) || (settings.LowPowerOnSeriousHeat && !s.doc.Settings.LowPowerOnSeriousHeat), "keyboard": (settings.DarkenKeyboard && !s.doc.Settings.DarkenKeyboard), "display_restore": (settings.RestoreDisplay && !s.doc.Settings.RestoreDisplay), "audio": (settings.MuteAudio && !s.doc.Settings.MuteAudio), "pause_apps": addedApps, "hotspot": (settings.HotspotRecovery && !s.doc.Settings.HotspotRecovery), "agent_reconnect": (settings.AgentReconnect && !s.doc.Settings.AgentReconnect), "browser_actions": settings.BrowserActions && !s.doc.Settings.BrowserActions}
	for id, needed := range requested {
		if !needed {
			continue
		}
		available := false
		reason := "this host does not support " + id
		for _, capability := range s.capabilities() {
			if capability.ID == id {
				available = capability.Available
				if capability.Reason != "" {
					reason = capability.Reason
				}
			}
		}
		if !available {
			return invalid(reason)
		}
	}
	if settings.HotspotRecovery {
		if settings.HotspotName == "" || !slices.Contains(s.observation.KnownNetworks, settings.HotspotName) {
			return invalid("select a saved macOS Wi-Fi network first")
		}
	}
	previous := clone(s.doc)
	if settings.HotspotRecovery && !s.doc.Settings.HotspotRecovery {
		s.doc.Network = domain.AwakeNetworkAttempt{}
	}
	s.doc.Settings = settings
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	if !settings.AgentReconnect && s.agentCancel != nil {
		s.agentCancel()
	}
	if !settings.HotspotRecovery && s.networkCancel != nil {
		s.networkCancel()
	}
	return s.reconcile(ctx)
}

// UpdateRules validates and persists activation rules.
func (s *Service) UpdateRules(ctx context.Context, rules []domain.AwakeRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if len(rules) > 50 {
		return invalid("at most fifty automatic rules")
	}
	seen := map[string]bool{}
	for i := range rules {
		r := &rules[i]
		if r.Enabled && !s.host.Supported() {
			return invalid("Stay Awake requires macOS")
		}
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		if seen[r.ID] {
			return invalid("duplicate rule ID")
		}
		seen[r.ID] = true
		if err := validateRule(*r); err != nil {
			return err
		}
		guard := false
		for _, cap := range s.host.Capabilities() {
			if cap.ID == "native_guard" {
				guard = cap.Available
			}
		}
		if r.Enabled && (r.LidClosed || guard) {
			if err := s.host.SetupLid(); err != nil && (r.LidClosed || !errors.Is(err, ErrLidHelperUpgrade)) {
				return err
			}
		}
	}
	previous := clone(s.doc)
	s.doc.Rules = rules
	s.ruleMatches = map[string]ruleMatch{}
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	s.refresh(ctx)
	return s.reconcile(ctx)
}

// Downloads updates typed download heartbeats.
func (s *Service) Downloads(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if len(ids) > 200 {
		return invalid("too many downloads")
	}
	next := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 {
			return invalid("invalid download ID")
		}
		next[id] = true
	}
	s.downloads = next
	s.downloadsSeen = s.now()
	return nil
}
func (s *Service) downloadActive(id string, now time.Time) bool {
	return !s.downloadsSeen.IsZero() && now.Sub(s.downloadsSeen) < 20*time.Second && ((id == "any" && len(s.downloads) > 0) || s.downloads[id])
}
func (s *Service) refresh(ctx context.Context) {
	observation, err := s.host.Observe(ctx)
	if err != nil {
		observation = domain.AwakeObservation{}
	}
	if active, err := s.bindings.AgentActive(ctx); err == nil {
		observation.AgentActive = &active
	}
	observation.ObservedAt = s.now()
	s.observation = observation
}
func (s *Service) safety() string {
	if s.observation.Thermal == "critical" {
		return "critical_heat"
	}
	if s.observation.OnBattery != nil && *s.observation.OnBattery && s.observation.BatteryPercent != nil && *s.observation.BatteryPercent < s.doc.Settings.StopBatteryPercent {
		return "low_battery"
	}
	return ""
}
func (s *Service) reconcile(ctx context.Context) error {
	now := s.now()
	changed := s.reconcileSavePending
	count := 0
	unknownBinding := false
	lid := false
	// One native lease carries every contribution. Bound the entire aggregate
	// by its earliest finite deadline so independent sequence renewal cannot
	// preserve stale lid permission or policy effects from a shorter owner.
	var earliest time.Time
	for i := range s.doc.Sessions {
		source := &s.doc.Sessions[i]
		if source.EndedAt != nil {
			continue
		}
		reason := ""
		if deadline, ok := s.deadlines[source.ID]; ok && !now.Before(deadline) {
			reason = "expired"
		}
		if reason == "" && source.Scope == "session" {
			alive, _, err := s.bindings.Alive(ctx, "session", source.SessionID, "")
			if err != nil {
				unknownBinding = true
				continue
			}
			if !alive {
				reason = "session_ended"
			}
		}
		if reason == "" && source.Mode == "app" {
			alive, _, err := s.bindings.Alive(ctx, "app", source.BoundID, source.BoundIdentity)
			if err != nil {
				unknownBinding = true
				continue
			}
			if !alive {
				reason = "app_ended"
			}
		}
		if reason == "" && source.Mode == "download" && !s.downloadActive(source.BoundID, now) {
			reason = "download_ended"
			if s.downloadsSeen.IsZero() || now.Sub(s.downloadsSeen) >= 20*time.Second {
				reason = "download_observation_expired"
			}
		}
		if reason != "" {
			source.EndedAt = &now
			source.EndReason = reason
			changed = true
			continue
		}
		count++
		lid = lid || source.LidClosed
		deadline, finite := s.deadlines[source.ID]
		bound := source.Scope == "session" || source.Mode == "app" || source.Mode == "download"
		if bound {
			fresh := now.Add(15 * time.Second)
			if !finite || fresh.Before(deadline) {
				deadline = fresh
			}
			finite = true
		}
		if finite && (earliest.IsZero() || deadline.Before(earliest)) {
			earliest = deadline
		}
	}
	previousRules := s.activeRules
	s.activeRules = []string{}
	for i := range s.doc.Rules {
		r := &s.doc.Rules[i]
		matched := s.matchRuleActive(ctx, *r, slices.Contains(previousRules, r.ID))
		stable := s.stableRule(r.ID, matched, slices.Contains(previousRules, r.ID), now)
		if matched == 0 && r.Suppressed {
			r.Suppressed = false
			changed = true
		}
		if r.Enabled && !s.doc.Paused && !r.Suppressed && stable {
			s.activeRules = append(s.activeRules, r.ID)
			count++
			lid = lid || r.LidClosed
			fresh := now.Add(15 * time.Second)
			if earliest.IsZero() || fresh.Before(earliest) {
				earliest = fresh
			}
		}
	}
	_, _, nativeReason := s.host.LidStatus()
	reason := s.safety()
	if reason == "" && (nativeReason == "critical_heat" || nativeReason == "low_battery" || nativeReason == "battery_unknown") {
		reason = nativeReason
	}
	if reason != "" {
		changed = changed || s.doc.StopReason != reason || !s.doc.Paused
		s.doc.Paused = true
		s.doc.StopReason = reason
	}
	if changed {
		if err := s.save(ctx); err != nil {
			s.reconcileSavePending = true
			s.host.Stop()
			s.reason = "persistence_failed"
			return err
		}
	}
	if s.doc.StopReason != "" || count == 0 {
		if s.agentCancel != nil {
			s.agentCancel()
		}
		if s.networkCancel != nil {
			s.networkCancel()
		}
		s.activeRules = []string{}
		s.host.Stop()
		s.started = nil
		s.reason = s.doc.StopReason
		if s.reason == "" && unknownBinding {
			s.reason = "observation_unavailable"
		}
		return nil
	}
	if err := s.host.ConfigureEffects(s.doc.Settings); err != nil {
		return err
	}
	if err := s.host.SetProtection(s.doc.Settings.StopBatteryPercent); err != nil {
		return err
	}
	if err := s.host.ConfigureIdle(earliest, s.doc.Settings.DisplayAwake); err != nil {
		s.reason = "native_failed"
		return err
	}
	if err := s.host.SetDeadline(earliest); err != nil {
		s.reason = "lease_failed"
		return err
	}
	if err := s.host.ConfigureLid(lid, false); err != nil {
		s.reason = "native_failed"
		return err
	}
	if err := s.host.Start(); err != nil {
		s.reason = "native_failed"
		return err
	}
	if s.started == nil {
		s.started = &now
	}
	s.reason = ""
	return nil
}

// Run reconciles requests until shutdown.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	if s.needsObservations() {
		s.refresh(ctx)
	}
	_ = s.reconcile(ctx)
	s.mu.Unlock()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.closed = true
			s.host.Stop()
			now := s.now()
			for i := range s.doc.Sessions {
				if s.doc.Sessions[i].EndedAt == nil {
					s.doc.Sessions[i].EndedAt = &now
					s.doc.Sessions[i].EndReason = "shutdown"
				}
			}
			saveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = s.save(saveCtx)
			cancel()
			s.mu.Unlock()
			s.workers.Wait()
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.needsObservations() {
				s.refresh(ctx)
			}
			_ = s.reconcile(ctx)
			task := s.networkTask(ctx)
			agent := s.agentTask(ctx)
			s.mu.Unlock()
			if agent != nil {
				s.workers.Add(1)
				go func() { defer s.workers.Done(); s.runAgent(ctx, *agent) }()
			}
			if task != nil {
				s.workers.Add(1)
				go func() { defer s.workers.Done(); s.runNetwork(ctx, *task) }()
			}
		}
	}
}

// Recover ends requests and restores journaled effects.
func (s *Service) Recover(ctx context.Context) error {
	if err := s.StopAll(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host.RequestRecovery()
}

// RelaunchBrowser journals intent before the external action. Replaying an
// uncertain request never closes the browser a second time automatically.
func (s *Service) RelaunchBrowser(ctx context.Context, id, browser string, confirmed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	if _, err := uuid.Parse(id); err != nil {
		return invalid("browser action ID is required")
	}
	if !confirmed || !s.doc.Settings.BrowserActions {
		return invalid("enable browser actions and confirm closing its windows first")
	}
	for _, attempt := range s.doc.BrowserAttempts {
		if attempt.ID == id {
			if attempt.BrowserID != browser {
				return invalid("browser request ID was already used")
			}
			if attempt.Outcome == "completed" {
				return nil
			}
			return errors.New("this browser action is uncertain or failed; inspect the browser before making a new confirmed request")
		}
	}
	s.refresh(ctx)
	found := false
	for _, entry := range s.observation.Browsers {
		if entry.ID == browser {
			found = true
		}
	}
	if !found {
		return invalid("select an installed, signed supported browser")
	}
	if len(s.doc.BrowserAttempts) >= 1000 {
		return invalid("browser action history is full; disable this feature and contact support before issuing more actions")
	}
	previous := clone(s.doc)
	s.doc.BrowserAttempts = append(s.doc.BrowserAttempts, domain.AwakeBrowserAttempt{ID: id, BrowserID: browser, Outcome: "prepared"})
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	s.workers.Add(1)
	s.mu.Unlock()
	result := s.host.RelaunchBrowser(ctx, browser)
	s.mu.Lock()
	s.workers.Done()
	for i := range s.doc.BrowserAttempts {
		if s.doc.BrowserAttempts[i].ID == id {
			s.doc.BrowserAttempts[i].Outcome = "uncertain"
			if result == nil {
				s.doc.BrowserAttempts[i].Outcome = "completed"
			}
		}
	}
	return errors.Join(result, s.save(ctx))
}

type networkTask struct {
	episode, ssid string
	ctx           context.Context
}

func (s *Service) networkTask(ctx context.Context) *networkTask {
	if ctx.Err() != nil || s.closed || s.networkBusy || !s.doc.Settings.HotspotRecovery || !s.host.Active() {
		return nil
	}
	connected := s.observation.NetworkConnected
	if connected == nil {
		return nil
	}
	if *connected {
		previous := s.doc.Network
		if previous.Episode != "" || previous.Attempts != 0 || previous.LastAttempt != nil {
			// Closing an outage must be durable before a later outage can use
			// a fresh retry budget. Keep the last action outcome for status.
			s.doc.Network = domain.AwakeNetworkAttempt{Outcome: previous.Outcome}
			if err := s.save(ctx); err != nil {
				s.doc.Network = previous
				s.reason = "persistence_failed"
				return nil
			}
			if s.reason == "persistence_failed" {
				s.reason = ""
			}
		}
		s.networkOutage = time.Time{}
		return nil
	}
	now := s.now()
	if s.networkOutage.IsZero() {
		s.networkOutage = now
		return nil
	}
	if now.Sub(s.networkOutage) < 10*time.Second || s.doc.Network.Attempts >= 3 {
		return nil
	}
	if last := s.doc.Network.LastAttempt; last != nil && now.Sub(*last) < 30*time.Second {
		return nil
	}
	previous := s.doc.Network
	if s.doc.Network.Episode == "" {
		s.doc.Network.Episode = uuid.NewString()
	}
	s.doc.Network.Attempts++
	s.doc.Network.LastAttempt = &now
	s.doc.Network.Outcome = "pending"
	if err := s.save(ctx); err != nil {
		s.doc.Network = previous
		return nil
	}
	s.networkBusy = true
	actionCtx, cancel := context.WithCancel(ctx)
	s.networkCancel = cancel
	return &networkTask{episode: s.doc.Network.Episode, ssid: s.doc.Settings.HotspotName, ctx: actionCtx}
}
func (s *Service) runNetwork(ctx context.Context, task networkTask) {
	result := s.host.JoinNetwork(task.ctx, task.ssid)
	cancelled := task.ctx.Err() != nil
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networkBusy = false
	if s.networkCancel != nil {
		s.networkCancel()
		s.networkCancel = nil
	}
	if s.doc.Network.Episode != task.episode {
		return
	}
	s.doc.Network.Outcome = "completed"
	if result != nil {
		s.doc.Network.Outcome = "failed"
		if cancelled {
			s.doc.Network.Outcome = "cancelled"
		}
	}
	if err := s.save(ctx); err != nil {
		s.doc.Network.Outcome = "uncertain"
	}
}

const maxAwakeSessions = 1000

// pruneEndedSessions drops the oldest ended sessions once the document is at
// capacity, so history never blocks a new request. Only active sessions count
// toward the limit.
func (s *Service) pruneEndedSessions() {
	excess := len(s.doc.Sessions) - maxAwakeSessions + 1
	if excess <= 0 {
		return
	}
	kept := make([]domain.AwakeSession, 0, len(s.doc.Sessions))
	for _, source := range s.doc.Sessions {
		if excess > 0 && source.EndedAt != nil {
			excess--
			continue
		}
		kept = append(kept, source)
	}
	s.doc.Sessions = kept
}

// ClearHistory removes ended sessions while preserving replay protection.
func (s *Service) ClearHistory(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("stay awake is shutting down")
	}
	previous := clone(s.doc)
	kept := []domain.AwakeSession{}
	for _, source := range s.doc.Sessions {
		if source.EndedAt == nil {
			kept = append(kept, source)
		}
	}
	s.doc.Sessions = kept
	if err := s.save(ctx); err != nil {
		s.doc = previous
		return err
	}
	return nil
}

type agentRecovery interface {
	AgentRecoverySupported() bool
	InterruptedAgents() []domain.AwakeAgentTarget
	ReconnectAgent(context.Context, domain.AwakeAgentTarget) error
}

func (s *Service) capabilities() []domain.AwakeCapability {
	result := s.host.Capabilities()
	revocationPending := s.host.RecoveryReason() == "low_power_revocation_pending"
	for i := range result {
		switch result[i].ID {
		case "audio", "keyboard", "display_restore", "pause_apps", "hotspot", "agent_reconnect", "browser_actions":
			result[i].Available = false
			result[i].Reason = "This control is deferred to a follow-up release."
		}
		if revocationPending && (result[i].ID == "low_power" || result[i].ID == "low_power_authorization") {
			result[i].Available = false
			result[i].Reason = "Complete Revoke authorization again before enabling or authorizing Low Power Mode."
		}
	}
	return result
}

type agentTask struct {
	target domain.AwakeAgentTarget
	ctx    context.Context
}

func (s *Service) agentTask(ctx context.Context) *agentTask {
	recovery, ok := s.bindings.(agentRecovery)
	if !ok || !recovery.AgentRecoverySupported() || ctx.Err() != nil || s.closed || s.agentBusy || !s.doc.Settings.AgentReconnect || !s.host.Active() || s.observation.NetworkConnected == nil || !*s.observation.NetworkConnected {
		return nil
	}
	if len(s.doc.AgentAttempts) >= 1000 {
		s.doc.AgentOutcome = "history_full"
		return nil
	}
	for _, target := range recovery.InterruptedAgents() {
		if target.SessionID == "" || target.Owner.ControllerGeneration == "" || target.Owner.ProviderConversationID == "" {
			continue
		}
		seen := false
		for _, attempt := range s.doc.AgentAttempts {
			if attempt.SessionID == target.SessionID && attempt.Generation == target.Owner.ControllerGeneration {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		previous := clone(s.doc)
		s.doc.AgentAttempts = append(s.doc.AgentAttempts, domain.AwakeAgentAttempt{SessionID: target.SessionID, Generation: target.Owner.ControllerGeneration, Outcome: "prepared"})
		s.doc.AgentOutcome = "pending"
		if err := s.save(ctx); err != nil {
			s.doc = previous
			return nil
		}
		actionCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		s.agentCancel = cancel
		s.agentBusy = true
		return &agentTask{target: target, ctx: actionCtx}
	}
	return nil
}
func (s *Service) runAgent(ctx context.Context, task agentTask) {
	recovery, ok := s.bindings.(agentRecovery)
	result := errors.New("agent recovery is unavailable")
	if ok {
		result = recovery.ReconnectAgent(task.ctx, task.target)
	}
	cancelled := task.ctx.Err() != nil
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentBusy = false
	if s.agentCancel != nil {
		s.agentCancel()
		s.agentCancel = nil
	}
	outcome := "reconnected"
	if result != nil {
		outcome = "failed"
		if cancelled {
			outcome = "cancelled"
		}
	}
	for i := range s.doc.AgentAttempts {
		if s.doc.AgentAttempts[i].SessionID == task.target.SessionID && s.doc.AgentAttempts[i].Generation == task.target.Owner.ControllerGeneration {
			s.doc.AgentAttempts[i].Outcome = outcome
		}
	}
	s.doc.AgentOutcome = outcome
	if err := s.save(ctx); err != nil {
		s.doc.AgentOutcome = "uncertain"
	}
}

// Detailed observations are requested only by General settings or active
// policy. An unused feature must not spawn a native process every five seconds.
func (s *Service) needsObservations() bool {
	if s.doc.StopReason != "" {
		return false
	}
	for _, source := range s.doc.Sessions {
		if source.EndedAt == nil {
			return true
		}
	}
	if !s.doc.Paused {
		for _, rule := range s.doc.Rules {
			if rule.Enabled {
				return true
			}
		}
	}
	return false
}

// ObserveStatus refreshes settings-page observations at a bounded rate.
func (s *Service) ObserveStatus(ctx context.Context) domain.AwakeState {
	s.mu.Lock()
	if !s.closed && (s.observation.ObservedAt.IsZero() || s.now().Sub(s.observation.ObservedAt) >= 4*time.Second) {
		s.refresh(ctx)
	}
	s.mu.Unlock()
	return s.Status()
}

// finishPowerAuthorization refreshes permissions without holding the policy lock
// across a macOS administrator dialog.
func (s *Service) finishPowerAuthorization(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.powerAuthorizationBusy = false
	if !s.closed {
		s.refresh(ctx)
	}
}

// AuthorizeLowPower requests explicit macOS consent without holding the policy lock.
func (s *Service) AuthorizeLowPower(ctx context.Context) error {
	authorizer, ok := s.host.(interface{ AuthorizeLowPower(context.Context) error })
	if !ok {
		return invalid("this host cannot authorize Low Power Mode")
	}
	s.mu.Lock()
	if s.closed || s.powerAuthorizationBusy {
		s.mu.Unlock()
		return invalid("stay awake is shutting down or a power authorization action is in progress")
	}
	s.powerAuthorizationBusy = true
	s.mu.Unlock()
	defer s.finishPowerAuthorization(ctx)
	return authorizer.AuthorizeLowPower(ctx)
}

// RevokeLowPower disables automation durably before restoring owned changes and
// removing its account-specific grant. Cancellation never silently re-enables it.
func (s *Service) RevokeLowPower(ctx context.Context) error {
	revoker, ok := s.host.(interface{ RevokeLowPower(context.Context) error })
	if !ok {
		return invalid("this host cannot revoke Low Power Mode authorization")
	}
	s.mu.Lock()
	if s.closed || s.powerAuthorizationBusy {
		s.mu.Unlock()
		return invalid("stay awake is shutting down or a power authorization action is in progress")
	}
	previous := clone(s.doc)
	s.doc.Settings.LowPowerBatteryPercent = 0
	s.doc.Settings.LowPowerOnSeriousHeat = false
	if err := s.save(ctx); err != nil {
		s.doc = previous
		s.mu.Unlock()
		return err
	}
	if err := s.reconcile(ctx); err != nil {
		s.mu.Unlock()
		return err
	}
	s.powerAuthorizationBusy = true
	s.mu.Unlock()
	defer s.finishPowerAuthorization(ctx)
	if err := s.host.RequestRecovery(); err != nil {
		return err
	}
	return revoker.RevokeLowPower(ctx)
}
