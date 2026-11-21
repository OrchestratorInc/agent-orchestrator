package computeruse

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// remoteActiveWindow is how long after its last call another computer
	// still counts as using this one; a pause longer than this starts a new run.
	remoteActiveWindow = 2 * time.Minute
)

var (
	remoteHostIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	unsafeScopeChars    = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	errRemoteOff        = apierr.Forbidden("COMPUTER_USE_REMOTE_OFF", "This computer does not let agents on other computers use it. Turn on \"Let agents on your other computers use this computer\" in Settings > Computer use agents on it.")
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
			return remoteState{}, fmt.Errorf("computeruse: parse remote settings: %w", err)
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

// updateRemote applies change to the saved state under the lock.
func (s *Service) updateRemote(change func(*remoteState) error) (RemoteSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.remote.load()
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
	return s.updateRemote(func(state *remoteState) error {
		state.AllowRemote = allow
		return nil
	})
}

// SetRemoteTarget makes this computer's agents run computer use on another
// computer.
func (s *Service) SetRemoteTarget(_ context.Context, request SetRemoteTargetRequest) (RemoteSettings, error) {
	target, err := validRemoteTarget(request)
	if err != nil {
		return RemoteSettings{}, err
	}
	credential := strings.TrimSpace(request.Credential)
	return s.updateRemote(func(state *remoteState) error {
		state.Target = &target
		state.Credential = credential
		if name := strings.TrimSpace(request.CallerName); name != "" {
			state.CallerName = name
		}
		if state.CallerID == "" {
			state.CallerID = randomID()
		}
		return nil
	})
}

// ClearRemoteTarget runs this computer's agents' computer use here again.
func (s *Service) ClearRemoteTarget(context.Context) (RemoteSettings, error) {
	return s.updateRemote(func(state *remoteState) error {
		state.Target = nil
		state.Credential = ""
		return nil
	})
}

// RemoteTargetLabel names the other computer this computer's agents run
// computer use on, or "" when they run it here.
func (s *Service) RemoteTargetLabel(context.Context) string {
	state, err := s.remoteState()
	if err != nil || state.Target == nil {
		return ""
	}
	return state.Target.Label
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
	if err := s.requireRemoteAllowed(); err != nil {
		return RemoteCallResult{}, err
	}
	s.noteRemoteCall(request.Caller, request.CallerName, time.Now())
	result, err := s.callHere(ctx, CallRequest{
		Tool:      request.Tool,
		Arguments: request.Arguments,
		SessionID: remoteScope(request.Caller, request.SessionID),
	})
	if err != nil {
		return RemoteCallResult{}, err
	}
	images := make([]RemoteImage, 0, len(result.Images))
	for _, image := range result.Images {
		info, err := os.Stat(image.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxRemoteImageBytes {
			continue
		}
		data, err := os.ReadFile(image.Path)
		if err != nil {
			continue
		}
		images = append(images, RemoteImage{Path: image.Path, MimeType: image.MimeType, Data: base64.StdEncoding.EncodeToString(data)})
	}
	return RemoteCallResult{Tool: result.Tool, Result: result.Result, Images: images}, nil
}

// remoteScope names the screenshot folder and driver session for another
// computer's AO session: "remote-<caller>-<session>", within the scope rules.
func remoteScope(caller, sessionID string) string {
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
	if sessionID = clean(sessionID, 100); sessionID != "" {
		parts = append(parts, sessionID)
	}
	return strings.Join(parts, "-")
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
}

func (s *Service) forwarding() (forwarding, error) {
	state, err := s.remoteState()
	if err != nil || state.Target == nil {
		return forwarding{}, err
	}
	target := *state.Target
	return forwarding{target: &target, credential: state.Credential, caller: state.CallerID, callerName: state.CallerName}, nil
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
	started := time.Now()
	var response RemoteCallResult
	if err := s.remoteRequest(ctx, target, credential, http.MethodPost, "call", body, remoteCallTimeout, &response); err != nil {
		// The step shows in this session's timeline, with why the other
		// computer refused it or could not be reached.
		s.recordCallOn(scope, target.Label, body.Tool, request.Arguments, started, nil, err)
		return CallResult{}, err
	}
	saved := &savedImages{store: s.images, scope: scope, tool: body.Tool, images: []Image{}}
	local := make(map[string]string, len(response.Images))
	for _, image := range response.Images {
		path, err := saved.write(image.Data)
		if err != nil {
			continue
		}
		if image.Path != "" {
			local[image.Path] = path
		}
	}
	s.images.prune(scope)
	s.recordCallOn(scope, target.Label, body.Tool, request.Arguments, started, saved.images, nil)
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
	response, err := s.remoteClient.Do(request)
	if err != nil {
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
