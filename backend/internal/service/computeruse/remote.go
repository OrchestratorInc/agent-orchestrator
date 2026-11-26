package computeruse

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Remote computer use runs an agent's computer-use calls on another of the
// user's computers. Two settings, each in Settings > Computer use agents,
// make it work:
//
//   - On the computer that is used, "Let agents on your other computers use
//     this computer" (AllowRemote) offers its Cua Driver to devices that can
//     already reach it as a remote host. Those calls arrive on the
//     authenticated /api/v1/remote-computer-use routes, the only computer-use
//     routes the LAN listener serves, and they still need "Let agents use this
//     computer" on.
//   - On the computer the agent runs on, "Run computer use on" (Target) names
//     that other computer. Its address and the credential this computer's
//     desktop app already holds for it are handed to the daemon, which forwards
//     every computer-use call there and saves the screenshots locally, so the
//     agent's tools work unchanged.
//
// Both live in <dataDir>/computer-use-remote.json (mode 0600), which holds the
// one credential; no API response ever carries it.

// remoteFileName holds both settings beside the screenshot folder, not inside
// it: that folder holds one directory per session.
const remoteFileName = "computer-use-remote.json"

const (
	// remoteCallTimeout covers a run_steps batch on the other computer, its
	// screenshots, and the trip there and back.
	remoteCallTimeout = batchTimeout + 30*time.Second
	remoteReadTimeout = 20 * time.Second
	// maxRemoteResponseBytes bounds one call's answer: a few screenshots,
	// base64-encoded, and the window tree.
	maxRemoteResponseBytes = 64 << 20
	// maxRemoteImageBytes bounds one screenshot sent to another computer, as
	// the MCP server bounds one sent to an agent.
	maxRemoteImageBytes = 10 << 20
	maxRemoteLabelRunes = 120
	// maxRemoteCallers bounds how many other computers' activity is kept.
	maxRemoteCallers = 64
	// remoteResponseOverhead is room in maxRemoteResponseBytes for the JSON
	// around the result and screenshots.
	remoteResponseOverhead = 64 << 10
	// identityTimeout bounds the identity check on a new connection.
	identityTimeout = 10 * time.Second
	// remoteActiveWindow is how long after its last call another computer
	// still counts as using this one; a pause longer than this starts a new run.
	remoteActiveWindow = 2 * time.Minute
	// maxRemoteCallerBytes bounds the ID another computer sends for itself.
	maxRemoteCallerBytes = 128
	// MaxRemoteCallBodyBytes bounds one call's request body from another
	// computer: a tool's arguments, or a run_steps batch.
	MaxRemoteCallBodyBytes = 4 << 20
	// remoteScopeMaxAge is how long another computer's screenshot folder
	// outlives its last screenshot; remoteSweepInterval spaces the sweeps.
	remoteScopeMaxAge   = 7 * 24 * time.Hour
	remoteSweepInterval = time.Hour
)

var (
	remoteHostIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	unsafeScopeChars         = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	errRemoteSettingsCorrupt = errors.New("computeruse: remote settings file is damaged")
	errRemoteHostMismatch    = errors.New("computeruse: another host answered at the remote host's address")
	errRemoteOff             = apierr.Forbidden("COMPUTER_USE_REMOTE_OFF", "This computer does not let agents on other computers use it. Turn on \"Let agents on your other computers use this computer\" in Settings > Computer use agents on it.")
)

// RemoteTarget is the other computer an agent's computer-use calls run on.
type RemoteTarget struct {
	HostID string `json:"hostId" description:"The other computer's Orchestrator host ID."`
	Label  string `json:"label" description:"The name this computer lists it under."`
	URL    string `json:"url" description:"The other computer's remote-host address."`
}

// RemoteSettings is what Settings shows about remote computer use on this
// computer. It never carries the credential.
type RemoteSettings struct {
	AllowRemote bool          `json:"allowRemote" description:"Agents on the user's other computers may use this computer's Cua Driver."`
	Target      *RemoteTarget `json:"target,omitempty" description:"The other computer this computer's agents run computer use on; absent runs it here."`
	// Activity lists the other computers whose agents used this one recently.
	Activity []RemoteActivity `json:"activity" description:"Other computers whose agents used this computer in the last two minutes, most recent first."`
}

// RemoteActivity is another computer whose agents are using this one.
type RemoteActivity struct {
	Caller  string    `json:"caller" description:"The other computer's random ID."`
	Name    string    `json:"name,omitempty" description:"The other computer's name, when it sent one."`
	Started time.Time `json:"started" description:"When this run of calls began."`
	LastAt  time.Time `json:"lastAt" description:"When its latest call arrived."`
}

// SetRemoteTargetRequest names the other computer to run computer use on. The
// desktop app sends it with the credential it holds for that computer.
type SetRemoteTargetRequest struct {
	HostID     string `json:"hostId"`
	Label      string `json:"label"`
	URL        string `json:"url"`
	Credential string `json:"credential" description:"The bearer credential this computer uses for the remote host. Stored with mode 0600 and never returned."`
	CallerName string `json:"callerName,omitempty" description:"This computer's name, shown on the other computer while this computer's agents use it."`
	// SessionID is the chat the user chose the computer from, which moves to
	// it rather than being refused for having started elsewhere.
	SessionID string `json:"sessionId,omitempty" description:"The AO session the computer was chosen from. That session's calls run on it from now on, though the session started with computer use elsewhere; other sessions that started elsewhere are refused."`
}

// RemoteOffer is whether this computer currently lets agents on the user's
// other computers use it, as those computers read it.
type RemoteOffer struct {
	Offered  bool   `json:"offered" description:"Agents on other computers can run computer use here now: Cua Driver is installed and both \"Let agents use this computer\" and \"Let agents on your other computers use this computer\" are on."`
	Platform string `json:"platform" description:"Operating system this computer runs: darwin, linux, or windows."`
}

// RemoteCallRequest is one computer-use call from an agent on another
// computer.
type RemoteCallRequest struct {
	Tool       string         `json:"tool"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	SessionID  string         `json:"sessionId,omitempty" description:"The calling computer's AO session; with caller it names this computer's screenshot folder and Cua Driver session for the calls."`
	Caller     string         `json:"caller,omitempty" description:"A random ID the calling computer keeps, so sessions with the same ID on two computers stay apart."`
	CallerName string         `json:"callerName,omitempty" description:"The calling computer's name, shown here while its agents use this computer."`
}

// RemoteImage is a screenshot sent back to the calling computer.
type RemoteImage struct {
	Path     string `json:"path" description:"Where this computer saved it; the result's fields name this path."`
	MimeType string `json:"mimeType"`
	Data     string `json:"data" description:"The image, base64-encoded."`
}

// RemoteCallResult is a call's result with its screenshots inline, because
// the calling computer cannot read this computer's files.
type RemoteCallResult struct {
	Tool   string        `json:"tool"`
	Result any           `json:"result"`
	Images []RemoteImage `json:"images"`
	// StepError is the failed step of a run_steps batch whose report Result
	// still carries, so the calling computer's timeline records it as failed.
	StepError string `json:"stepError,omitempty" description:"Which run_steps step failed and why; the batch's report is still in result."`
}

// remoteState is the file's content.
type remoteState struct {
	AllowRemote bool          `json:"allowRemote,omitempty"`
	Target      *RemoteTarget `json:"target,omitempty"`
	Credential  string        `json:"credential,omitempty"`
	// CallerID tells the other computer which computer's sessions a call is
	// for. Kept across targets.
	CallerID string `json:"callerId,omitempty"`
	// CallerName is this computer's name, shown on the other computer while
	// its agents use it.
	CallerName string `json:"callerName,omitempty"`
	// Adopted maps a session to the target the user moved it to from that
	// session's own panel: "local" or a host ID. Only entries naming the
	// current target are kept.
	Adopted map[string]string `json:"adopted,omitempty"`
}

// remoteStore reads and writes remoteState. The Service's mutex guards it.
type remoteStore struct {
	path   string
	loaded bool
	state  remoteState
}

func newRemoteStore(dataDir string) remoteStore {
	return remoteStore{path: filepath.Join(dataDir, remoteFileName)}
}

func (r *remoteStore) load() (remoteState, error) {
	if r.loaded {
		return r.state, nil
	}
	data, err := os.ReadFile(r.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.state = remoteState{}
	case err != nil:
		return remoteState{}, fmt.Errorf("computeruse: read remote settings: %w", err)
	default:
		var state remoteState
		if err := json.Unmarshal(data, &state); err != nil {
			return remoteState{}, fmt.Errorf("%w: %w", errRemoteSettingsCorrupt, err)
		}
		r.state = state
	}
	r.loaded = true
	return r.state, nil
}

func (r *remoteStore) save(state remoteState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("computeruse: create data folder: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(r.path), remoteFileName+".*")
	if err != nil {
		return fmt.Errorf("computeruse: save remote settings: %w", err)
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("computeruse: save remote settings: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("computeruse: save remote settings: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("computeruse: save remote settings: %w", err)
	}
	if err := os.Rename(temp.Name(), r.path); err != nil {
		return fmt.Errorf("computeruse: save remote settings: %w", err)
	}
	r.state = state
	r.loaded = true
	return nil
}

func (s remoteState) view() RemoteSettings {
	settings := RemoteSettings{AllowRemote: s.AllowRemote, Activity: []RemoteActivity{}}
	if s.Target != nil {
		target := *s.Target
		settings.Target = &target
	}
	return settings
}

// updateRemote applies change to the saved state under the lock. With
// recoverDamaged, a settings file that cannot be parsed is replaced by the
// change applied to empty settings: turning things off must work even then,
// while every other read keeps reporting the damage.
func (s *Service) updateRemote(recoverDamaged bool, change func(*remoteState) error) (RemoteSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.remote.load()
	if err != nil && recoverDamaged && errors.Is(err, errRemoteSettingsCorrupt) {
		state, err = remoteState{}, nil
	}
	if err != nil {
		return RemoteSettings{}, err
	}
	if err := change(&state); err != nil {
		return RemoteSettings{}, err
	}
	if err := s.remote.save(state); err != nil {
		return RemoteSettings{}, err
	}
	settings := state.view()
	settings.Activity = s.recentActivityLocked(time.Now())
	return settings, nil
}

func (s *Service) remoteState() (remoteState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remote.load()
}

// RemoteSettings reports both remote computer-use settings.
func (s *Service) RemoteSettings(context.Context) (RemoteSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.remote.load()
	if err != nil {
		return RemoteSettings{}, err
	}
	settings := state.view()
	settings.Activity = s.recentActivityLocked(time.Now())
	return settings, nil
}

// noteRemoteCall records that another computer's agent made a call here.
func (s *Service) noteRemoteCall(caller, name string, at time.Time) {
	key := strings.TrimSpace(caller)
	if key == "" {
		key = "unknown"
	}
	if runes := []rune(strings.TrimSpace(name)); len(runes) > maxRemoteLabelRunes {
		name = string(runes[:maxRemoteLabelRunes])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activity == nil {
		s.activity = map[string]RemoteActivity{}
	}
	s.recentActivityLocked(at)
	if _, known := s.activity[key]; !known && len(s.activity) >= maxRemoteCallers {
		oldest := ""
		for caller, activity := range s.activity {
			if oldest == "" || activity.LastAt.Before(s.activity[oldest].LastAt) || (activity.LastAt.Equal(s.activity[oldest].LastAt) && caller < oldest) {
				oldest = caller
			}
		}
		delete(s.activity, oldest)
	}
	current, ok := s.activity[key]
	if !ok || at.Sub(current.LastAt) > remoteActiveWindow {
		current = RemoteActivity{Caller: key, Started: at.UTC()}
	}
	if name = strings.TrimSpace(name); name != "" {
		current.Name = name
	}
	current.LastAt = at.UTC()
	s.activity[key] = current
}

// recentActivityLocked lists the computers active within remoteActiveWindow,
// most recent first, and forgets older ones. The caller holds s.mu.
func (s *Service) recentActivityLocked(now time.Time) []RemoteActivity {
	recent := []RemoteActivity{}
	for key, activity := range s.activity {
		if now.Sub(activity.LastAt) > remoteActiveWindow {
			delete(s.activity, key)
			continue
		}
		recent = append(recent, activity)
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].LastAt.After(recent[j].LastAt) })
	return recent
}

// SetAllowRemote changes whether agents on the user's other computers may use
// this computer. Turning it on needs Cua Driver installed.
func (s *Service) SetAllowRemote(ctx context.Context, allow bool) (RemoteSettings, error) {
	if allow {
		installed, err := s.Installed(ctx)
		if err != nil {
			return RemoteSettings{}, err
		}
		if !installed {
			return RemoteSettings{}, errNotInstalled
		}
	}
	settings, err := s.updateRemote(!allow, func(state *remoteState) error {
		state.AllowRemote = allow
		return nil
	})
	if err == nil && !allow {
		// Stop means stop: calls already running stop acting on the screen.
		s.cancelRemoteCalls()
	}
	s.sweepRemoteScopes(time.Now())
	return settings, err
}

// trackRemoteCall registers a call from another computer, so turning sharing
// off cancels it. done unregisters it.
func (s *Service) trackRemoteCall(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	s.mu.Lock()
	if s.remoteCalls == nil {
		s.remoteCalls = map[uint64]context.CancelCauseFunc{}
	}
	s.remoteCallSeq++
	id := s.remoteCallSeq
	s.remoteCalls[id] = cancel
	s.mu.Unlock()
	return ctx, func() {
		s.mu.Lock()
		delete(s.remoteCalls, id)
		s.mu.Unlock()
		cancel(nil)
	}
}

// cancelRemoteCalls cancels every call from another computer running now.
func (s *Service) cancelRemoteCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.remoteCalls {
		cancel(errRemoteOff)
	}
}

// sweepRemoteScopes removes other computers' screenshot folders whose newest
// file is older than remoteScopeMaxAge, at most once per remoteSweepInterval.
// Best effort.
func (s *Service) sweepRemoteScopes(now time.Time) {
	s.mu.Lock()
	due := now.Sub(s.remoteSweptAt) >= remoteSweepInterval
	if due {
		s.remoteSweptAt = now
	}
	s.mu.Unlock()
	if due {
		s.images.expireRemoteScopes(now.Add(-remoteScopeMaxAge))
	}
}

// SetRemoteTarget makes this computer's agents run computer use on another
// computer.
func (s *Service) SetRemoteTarget(_ context.Context, request SetRemoteTargetRequest) (RemoteSettings, error) {
	target, err := validRemoteTarget(request)
	if err != nil {
		return RemoteSettings{}, err
	}
	credential := strings.TrimSpace(request.Credential)
	sessionID, err := adoptingSession(request.SessionID)
	if err != nil {
		return RemoteSettings{}, err
	}
	return s.updateRemote(false, func(state *remoteState) error {
		state.Target = &target
		state.Credential = credential
		if name := strings.TrimSpace(request.CallerName); name != "" {
			state.CallerName = name
		}
		if state.CallerID == "" {
			state.CallerID = randomID()
		}
		state.adopt(target.HostID, sessionID)
		return nil
	})
}

// ClearRemoteTarget runs this computer's agents' computer use here again. It
// also replaces a damaged settings file. A sessionID names the chat it was
// chosen from, which moves here with it.
func (s *Service) ClearRemoteTarget(_ context.Context, sessionID string) (RemoteSettings, error) {
	sessionID, err := adoptingSession(sessionID)
	if err != nil {
		return RemoteSettings{}, err
	}
	return s.updateRemote(true, func(state *remoteState) error {
		state.Target = nil
		state.Credential = ""
		state.adopt(LocalTarget, sessionID)
		return nil
	})
}

// maxAdoptedSessions bounds the sessions remembered as moved to the current
// target.
const maxAdoptedSessions = 256

// adoptingSession checks the session a target was chosen from; "" is none.
func adoptingSession(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" && !scopePattern.MatchString(sessionID) {
		return "", apierr.Invalid("COMPUTER_USE_INVALID_SESSION", "sessionId may contain only letters, digits, '.', '_' and '-'.", nil)
	}
	return sessionID, nil
}

// adopt records that computer use now runs on current: sessions moved to an
// earlier target no longer match it, and sessionID, when set, moves to it.
func (s *remoteState) adopt(current, sessionID string) {
	for session, target := range s.Adopted {
		if target != current {
			delete(s.Adopted, session)
		}
	}
	if sessionID == "" {
		return
	}
	if s.Adopted == nil {
		s.Adopted = map[string]string{}
	}
	if _, ok := s.Adopted[sessionID]; !ok && len(s.Adopted) >= maxAdoptedSessions {
		for session := range s.Adopted {
			delete(s.Adopted, session)
			break
		}
	}
	s.Adopted[sessionID] = current
}

// maxBoundSessions bounds the sessions whose computer-use target is kept.
const maxBoundSessions = 1024

// BindSession records where sessionID's computer use was set up to run:
// LocalTarget or a remote host ID. Session starts and calls report it; either
// being empty records nothing.
func (s *Service) BindSession(sessionID, target string) {
	sessionID, target = strings.TrimSpace(sessionID), strings.TrimSpace(target)
	if sessionID == "" || target == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bindings == nil {
		s.bindings = map[string]string{}
	}
	if _, ok := s.bindings[sessionID]; !ok && len(s.bindings) >= maxBoundSessions {
		for session := range s.bindings {
			delete(s.bindings, session)
			break
		}
	}
	s.bindings[sessionID] = target
}

// SessionTarget is where sessionID's computer-use calls run: LocalTarget or a
// remote host ID. A session the user moved from its own panel runs on the
// current target; another runs where it was set up. "" means this daemon has
// not seen the session start or call since it started.
func (s *Service) SessionTarget(_ context.Context, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if !scopePattern.MatchString(sessionID) {
		return "", apierr.Invalid("COMPUTER_USE_INVALID_SESSION", "sessionId may contain only letters, digits, '.', '_' and '-'.", nil)
	}
	state, err := s.remoteState()
	if err != nil {
		return "", err
	}
	current := LocalTarget
	if state.Target != nil {
		current = state.Target.HostID
	}
	if state.Adopted[sessionID] == current {
		return current, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindings[sessionID], nil
}

// CurrentTarget names the other computer this computer's agents run computer
// use on, by host ID and name, or "" for both when they run it here.
func (s *Service) CurrentTarget(context.Context) (hostID, label string) {
	state, err := s.remoteState()
	if err != nil || state.Target == nil {
		return "", ""
	}
	return state.Target.HostID, state.Target.Label
}

// RemoteOffer reports whether agents on other computers can use this one now.
func (s *Service) RemoteOffer(ctx context.Context) (RemoteOffer, error) {
	offer := RemoteOffer{Platform: s.goos}
	state, err := s.remoteState()
	if err != nil {
		return offer, err
	}
	if !state.AllowRemote || !s.computerUseEnabled(ctx) {
		return offer, nil
	}
	installed, err := s.Installed(ctx)
	if err != nil {
		return offer, err
	}
	offer.Offered = installed
	return offer, nil
}

func (s *Service) requireRemoteAllowed() error {
	state, err := s.remoteState()
	if err != nil {
		return err
	}
	if !state.AllowRemote {
		return errRemoteOff
	}
	return nil
}

// RemoteAgentTools lists the tools agents on other computers get here.
func (s *Service) RemoteAgentTools(ctx context.Context) ([]AgentTool, error) {
	if err := s.requireRemoteAllowed(); err != nil {
		return nil, err
	}
	if !s.computerUseEnabled(ctx) {
		return nil, errDisabled
	}
	return s.localAgentTools(ctx)
}

// ServeRemoteCall runs one call from an agent on another computer, with every
// rule a local call has, and returns its screenshots inline.
func (s *Service) ServeRemoteCall(ctx context.Context, request RemoteCallRequest) (RemoteCallResult, error) {
	if len(request.Caller) > maxRemoteCallerBytes {
		return RemoteCallResult{}, apierr.Invalid("COMPUTER_USE_INVALID_CALLER", fmt.Sprintf("caller may be at most %d bytes.", maxRemoteCallerBytes), nil)
	}
	// Registered before the check, so a Stop between the two still cancels it.
	ctx, done := s.trackRemoteCall(ctx)
	defer done()
	if err := s.requireRemoteAllowed(); err != nil {
		return RemoteCallResult{}, err
	}
	s.sweepRemoteScopes(time.Now())
	// Another computer's call runs in its own driver session, never one it
	// names: a session argument could otherwise reach this computer's sessions.
	arguments := make(map[string]any, len(request.Arguments))
	for key, value := range request.Arguments {
		if key != "session" {
			arguments[key] = value
		}
	}
	result, stepErr, err := s.callHereAdmitted(ctx, CallRequest{
		Tool:      request.Tool,
		Arguments: arguments,
		SessionID: remoteScope(request.Caller, request.SessionID),
	}, func() { s.noteRemoteCall(request.Caller, request.CallerName, time.Now()) })
	if errors.Is(context.Cause(ctx), errRemoteOff) {
		return RemoteCallResult{}, errRemoteOff
	}
	if err != nil {
		return RemoteCallResult{}, err
	}
	answer := remoteResult(result)
	if stepErr != nil {
		answer.StepError = stepErr.Error()
	}
	return answer, nil
}

// remoteResult inlines a call's screenshots, newest last, within
// maxRemoteResponseBytes. The action already ran, so its result always goes
// back: a screenshot that does not fit, or cannot be read, is left out and its
// path in the result says why.
func remoteResult(result CallResult) RemoteCallResult {
	encodedResult, _ := json.Marshal(result.Result)
	budget := maxRemoteResponseBytes - remoteResponseOverhead - len(encodedResult)
	images := make([]RemoteImage, 0, len(result.Images))
	omitted := map[string]string{}
	for _, image := range result.Images {
		info, err := os.Stat(image.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxRemoteImageBytes {
			omitted[image.Path] = "screenshot not sent: it could not be read or is over 10 MB"
			continue
		}
		size := base64.StdEncoding.EncodedLen(int(info.Size())) + len(image.Path) + len(image.MimeType) + 64
		if size > budget {
			omitted[image.Path] = "screenshot not sent: the response would be over the size limit"
			continue
		}
		data, err := os.ReadFile(image.Path)
		if err != nil {
			omitted[image.Path] = "screenshot not sent: it could not be read"
			continue
		}
		budget -= size
		images = append(images, RemoteImage{Path: image.Path, MimeType: image.MimeType, Data: base64.StdEncoding.EncodeToString(data)})
	}
	return RemoteCallResult{Tool: result.Tool, Result: replacePaths(result.Result, omitted), Images: images}
}

// remoteScope names the screenshot folder and driver session for another
// computer's AO session: "remote-<caller>-<session>-<hash>", within the scope
// rules. The names are cleaned and shortened for reading; the hash of the
// exact pair keeps two pairs that clean alike apart.
func remoteScope(caller, sessionID string) string {
	sum := sha256.Sum256([]byte(caller + "\x00" + sessionID))
	clean := func(value string, limit int) string {
		value = strings.Trim(unsafeScopeChars.ReplaceAllString(strings.TrimSpace(value), "-"), "-._")
		if len(value) > limit {
			value = value[:limit]
		}
		return value
	}
	parts := []string{"remote"}
	if caller = clean(caller, 16); caller != "" {
		parts = append(parts, caller)
	}
	if sessionID = clean(sessionID, 90); sessionID != "" {
		parts = append(parts, sessionID)
	}
	return strings.Join(append(parts, hex.EncodeToString(sum[:5])), "-")
}

func validRemoteTarget(request SetRemoteTargetRequest) (RemoteTarget, error) {
	hostID := strings.TrimSpace(request.HostID)
	if !remoteHostIDPattern.MatchString(hostID) {
		return RemoteTarget{}, apierr.Invalid("COMPUTER_USE_REMOTE_INVALID", "hostId is not an Orchestrator host ID.", nil)
	}
	address, err := url.Parse(strings.TrimSpace(request.URL))
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" || address.User != nil ||
		address.RawQuery != "" || address.Fragment != "" || strings.Trim(address.Path, "/") != "" {
		return RemoteTarget{}, apierr.Invalid("COMPUTER_USE_REMOTE_INVALID", "url must be the remote host's http or https address.", nil)
	}
	if strings.TrimSpace(request.Credential) == "" {
		return RemoteTarget{}, apierr.Invalid("COMPUTER_USE_REMOTE_INVALID", "credential is required.", nil)
	}
	label := strings.TrimSpace(request.Label)
	if label == "" {
		label = address.Hostname()
	}
	if runes := []rune(label); len(runes) > maxRemoteLabelRunes {
		label = string(runes[:maxRemoteLabelRunes])
	}
	return RemoteTarget{HostID: hostID, Label: label, URL: address.Scheme + "://" + address.Host}, nil
}

func randomID() string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// forwarding is where this computer's calls go: the other computer, its
// credential, and how this computer names itself there. A nil target runs
// them here.
type forwarding struct {
	target     *RemoteTarget
	credential string
	caller     string
	callerName string
	// adopted is the target sessionID was moved to from its own panel, or "".
	adopted string
}

// forwarding reads where calls go now, and where sessionID was moved to.
func (s *Service) forwarding(sessionID string) (forwarding, error) {
	state, err := s.remoteState()
	if err != nil {
		return forwarding{}, err
	}
	to := forwarding{adopted: state.Adopted[strings.TrimSpace(sessionID)]}
	if state.Target == nil {
		return to, nil
	}
	target := *state.Target
	to.target, to.credential, to.caller, to.callerName = &target, state.Credential, state.CallerID, state.CallerName
	return to, nil
}

// remoteAgentTools reads the tools the other computer offers.
func (s *Service) remoteAgentTools(ctx context.Context, target RemoteTarget, credential string) ([]AgentTool, error) {
	var response struct {
		Tools []AgentTool `json:"tools"`
	}
	if err := s.remoteRequest(ctx, target, credential, http.MethodGet, "agent-tools", nil, remoteReadTimeout, &response); err != nil {
		return nil, err
	}
	if response.Tools == nil {
		response.Tools = []AgentTool{}
	}
	return response.Tools, nil
}

// callRemote runs one call on the other computer and saves its screenshots
// here, under the session's own folder, so the result's paths are local.
func (s *Service) callRemote(ctx context.Context, target RemoteTarget, credential, caller, callerName string, request CallRequest) (CallResult, error) {
	scope, err := s.images.scope(request.SessionID)
	if err != nil {
		return CallResult{}, err
	}
	body := RemoteCallRequest{Tool: strings.TrimSpace(request.Tool), Arguments: request.Arguments, SessionID: request.SessionID, Caller: caller, CallerName: callerName}
	var response RemoteCallResult
	if err := s.remoteRequest(ctx, target, credential, http.MethodPost, "call", body, remoteCallTimeout, &response); err != nil {
		// The step shows in this session's timeline, with why the other
		// computer refused it or could not be reached.
		s.recordCallOn(scope, target.Label, body.Tool, request.Arguments, nil, err)
		return CallResult{}, err
	}
	saved := &savedImages{store: s.images, scope: scope, tool: body.Tool, images: []Image{}}
	local := make(map[string]string, len(response.Images))
	for _, image := range response.Images {
		path, err := saved.write(image.Data)
		if err != nil {
			// The action ran; the result says the screenshot is missing rather
			// than naming a file only the other computer has.
			path = "screenshot not saved on this computer: " + err.Error()
		}
		if image.Path != "" {
			local[image.Path] = path
		}
	}
	s.images.prune(scope)
	var stepErr error
	if response.StepError != "" {
		stepErr = errors.New(response.StepError)
	}
	s.recordCallOn(scope, target.Label, body.Tool, request.Arguments, saved.images, stepErr)
	return CallResult{Tool: body.Tool, Result: replacePaths(response.Result, local), Images: saved.images}, nil
}

// replacePaths points every string in a result that names one of the other
// computer's screenshots at its copy here.
func replacePaths(value any, local map[string]string) any {
	if len(local) == 0 {
		return value
	}
	switch typed := value.(type) {
	case string:
		if path, ok := local[typed]; ok {
			return path
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = replacePaths(item, local)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = replacePaths(item, local)
		}
		return typed
	default:
		return value
	}
}

// remoteRequest sends one request to the other computer's
// /api/v1/remote-computer-use routes and decodes its answer into out.
func (s *Service) remoteRequest(ctx context.Context, target RemoteTarget, credential, method, route string, body any, timeout time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(target.URL, "/")+"/api/v1/remote-computer-use/"+route, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	// The other computer refuses the call if another machine now answers at
	// this address.
	request.Header.Set("X-AO-Expected-Host-ID", target.HostID)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.remoteClientFor(target).Do(request)
	if err != nil {
		if errors.Is(err, errRemoteHostMismatch) {
			return apierr.Conflict("COMPUTER_USE_REMOTE_HOST_CHANGED", fmt.Sprintf("Another computer now answers at %s's address, so this computer did not send it anything. Choose it again under \"Run computer use on\" in Settings > Computer use agents.", target.Label), nil)
		}
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return apierr.Unavailable("COMPUTER_USE_REMOTE_TIMEOUT", fmt.Sprintf("%s did not answer the computer-use call in time.", target.Label))
		}
		return apierr.Unavailable("COMPUTER_USE_REMOTE_UNREACHABLE", fmt.Sprintf("Could not reach %s for computer use. Check that it is on and Orchestrator is running there.", target.Label))
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponseBytes+1))
	if err != nil {
		return apierr.Unavailable("COMPUTER_USE_REMOTE_UNREACHABLE", fmt.Sprintf("%s stopped answering during the computer-use call.", target.Label))
	}
	if len(data) > maxRemoteResponseBytes {
		return apierr.Unavailable("COMPUTER_USE_REMOTE_TOO_LARGE", fmt.Sprintf("%s sent back more than %d MB for one computer-use call.", target.Label, maxRemoteResponseBytes>>20))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return remoteFailure(target, response.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return apierr.Unavailable("COMPUTER_USE_REMOTE_INVALID_RESPONSE", fmt.Sprintf("%s sent an answer this computer could not read.", target.Label))
	}
	return nil
}

// remoteFailure turns the other computer's refusal into this computer's
// error, keeping its code and message so the agent reads why.
func remoteFailure(target RemoteTarget, status int, data []byte) error {
	var envelope struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	_ = json.Unmarshal(data, &envelope)
	switch {
	case status == http.StatusUnauthorized:
		return apierr.Conflict("COMPUTER_USE_REMOTE_UNAUTHORIZED", fmt.Sprintf("%s refused this computer's credential. Choose it again under \"Run computer use on\" in Settings > Computer use agents.", target.Label), nil)
	case status == http.StatusNotFound && envelope.Code != "" && strings.HasPrefix(envelope.Code, "COMPUTER_USE_"):
		// A real computer-use answer, not a missing route.
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		return apierr.Conflict("COMPUTER_USE_REMOTE_UNSUPPORTED", fmt.Sprintf("%s does not offer computer use to other computers. Update Orchestrator there.", target.Label), nil)
	case status == http.StatusMisdirectedRequest || envelope.Code == "HOST_ID_MISMATCH":
		return apierr.Conflict("COMPUTER_USE_REMOTE_HOST_CHANGED", fmt.Sprintf("Another computer now answers at %s's address. Choose it again under \"Run computer use on\" in Settings > Computer use agents.", target.Label), nil)
	}
	code := envelope.Code
	if code == "" {
		code = "COMPUTER_USE_REMOTE_FAILED"
	}
	message := strings.TrimSpace(envelope.Message)
	if message == "" {
		message = fmt.Sprintf("the call failed with HTTP %d", status)
	}
	return apierr.New(remoteKind(status), code, fmt.Sprintf("On %s: %s", target.Label, message), envelope.Details)
}

func remoteKind(status int) apierr.Kind {
	switch status {
	case http.StatusBadRequest:
		return apierr.KindInvalid
	case http.StatusNotFound:
		return apierr.KindNotFound
	case http.StatusConflict:
		return apierr.KindConflict
	case http.StatusForbidden:
		return apierr.KindForbidden
	case http.StatusTooManyRequests:
		return apierr.KindTooManyRequests
	case http.StatusNotImplemented:
		return apierr.KindNotImplemented
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return apierr.KindUnavailable
	default:
		return apierr.KindInternal
	}
}

// remoteClientFor returns the client for target. Every connection it opens
// first asks the address which Orchestrator host answers there, over that same
// connection and with no credential (GET /api/v1/identity, see ADR 0003), and
// is used only when the answer is target's host ID: the credential and the
// call never reach another machine that took over the address. Redirects are
// not followed for the same reason.
func (s *Service) remoteClientFor(target RemoteTarget) *http.Client {
	key := target.HostID + "\x00" + target.URL
	s.mu.Lock()
	defer s.mu.Unlock()
	if client := s.remoteClients[key]; client != nil {
		return client
	}
	for other, client := range s.remoteClients {
		client.CloseIdleConnections()
		delete(s.remoteClients, other)
	}
	if s.remoteClients == nil {
		s.remoteClients = map[string]*http.Client{}
	}
	hostHeader := target.URL
	if address, err := url.Parse(target.URL); err == nil {
		hostHeader = address.Host
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.remoteTLS != nil {
		tlsConfig = s.remoteTLS.Clone()
	}
	dialer := &net.Dialer{Timeout: identityTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Straight to the host: a proxy in between would get the connection
		// the identity check vouches for.
		Proxy:               nil,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return verifyRemoteHost(ctx, conn, hostHeader, target.HostID)
		},
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			raw, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			config := tlsConfig.Clone()
			if config.ServerName == "" {
				config.ServerName, _, _ = net.SplitHostPort(address)
			}
			config.NextProtos = []string{"http/1.1"}
			conn := tls.Client(raw, config)
			handshake, cancel := context.WithTimeout(ctx, identityTimeout)
			err = conn.HandshakeContext(handshake)
			cancel()
			if err != nil {
				_ = raw.Close()
				return nil, err
			}
			return verifyRemoteHost(ctx, conn, hostHeader, target.HostID)
		},
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.remoteClients[key] = client
	return client
}

// verifyRemoteHost reads which Orchestrator host answers on conn and returns
// conn only when it is hostID; otherwise it closes conn.
func verifyRemoteHost(ctx context.Context, conn net.Conn, host, hostID string) (net.Conn, error) {
	fail := func(err error) (net.Conn, error) {
		_ = conn.Close()
		return nil, err
	}
	deadline := time.Now().Add(identityTimeout)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	_ = conn.SetDeadline(deadline)
	request, err := http.NewRequest(http.MethodGet, "http://"+host+"/api/v1/identity", http.NoBody)
	if err != nil {
		return fail(err)
	}
	request.Header.Set("Accept", "application/json")
	if err := request.Write(conn); err != nil {
		return fail(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return fail(err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if err != nil {
		return fail(err)
	}
	var identity struct {
		HostID string `json:"hostId"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &identity) != nil || identity.HostID != hostID {
		return fail(errRemoteHostMismatch)
	}
	if response.Close || reader.Buffered() > 0 {
		return fail(errors.New("computeruse: the remote host closed the connection after the identity check"))
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
