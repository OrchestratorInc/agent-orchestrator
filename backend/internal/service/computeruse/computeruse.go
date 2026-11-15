package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type DaemonState string

const (
	DaemonRunning DaemonState = "running"
	DaemonStopped DaemonState = "stopped"
	DaemonUnknown DaemonState = "unknown"
)

type PermissionState string

const (
	PermissionGranted PermissionState = "granted"
	PermissionMissing PermissionState = "missing"
	PermissionUnknown PermissionState = "unknown"
)

type Permissions struct {
	Accessibility   PermissionState `json:"accessibility" enum:"granted,missing,unknown" description:"Accessibility, needed to read and operate app controls."`
	ScreenRecording PermissionState `json:"screenRecording" enum:"granted,missing,unknown" description:"Screen Recording, needed for screenshots."`
	Granting        bool            `json:"granting" description:"A permission request is waiting on the user in System Settings."`
	GrantError      string          `json:"grantError,omitempty" description:"Why the last permission request failed."`
}

type Status struct {
	Platform        string       `json:"platform" description:"Operating system the daemon runs on: darwin, linux, or windows."`
	Installed       bool         `json:"installed"`
	BinaryPath      string       `json:"binaryPath,omitempty" description:"The cua-driver executable AO runs."`
	Version         string       `json:"version,omitempty"`
	LatestVersion   string       `json:"latestVersion,omitempty" description:"Newest release on the driver's update channel, when it could be checked."`
	UpdateAvailable bool         `json:"updateAvailable"`
	ReleaseNotesURL string       `json:"releaseNotesUrl,omitempty"`
	Daemon          DaemonState  `json:"daemon" enum:"running,stopped,unknown" description:"Whether Cua Driver's background service answers."`
	Permissions     *Permissions `json:"permissions,omitempty" description:"macOS only."`
	ImageDirectory  string       `json:"imageDirectory" description:"Where screenshots from computer-use calls are saved."`
}

type CallRequest struct {
	Tool      string         `json:"tool" description:"Driver tool name, e.g. list_windows or get_window_state."`
	Arguments map[string]any `json:"arguments,omitempty" description:"Tool arguments as a JSON object."`
	SessionID string         `json:"sessionId,omitempty" description:"AO session the call is made for. Screenshots are saved under it, and calls for the same AO session share one Cua Driver session unless arguments.session names another."`
}

type Image struct {
	Path     string `json:"path"`
	MimeType string `json:"mimeType"`
}

type CallResult struct {
	Tool   string  `json:"tool"`
	Result any     `json:"result" description:"The tool's JSON output. Inline images are replaced by *_file_path fields. After an action, observation holds AO's read of the window: the tree diff and a screenshot file."`
	Images []Image `json:"images" description:"Screenshots this call saved. Open them with an image viewer."`
}

const (
	probeTimeout    = 10 * time.Second
	callTimeout     = 45 * time.Second
	doctorTimeout   = 30 * time.Second
	batchTimeout    = 75 * time.Second
	grantTimeout    = 10 * time.Minute
	daemonStartWait = 8 * time.Second
)

var (
	toolNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	unknownSessionArg = regexp.MustCompile(`\bunknown argument session\b`)
	endedSession      = regexp.MustCompile(`^session has ended; tool call '[a-z0-9_]+' was rejected\b`)
	terminalEscape    = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	errNotInstalled   = apierr.Conflict("COMPUTER_USE_NOT_INSTALLED", "Cua Driver is not installed. Install it in Settings > Computer use agents.", nil)
	errDaemonStopped  = apierr.Conflict("COMPUTER_USE_DAEMON_STOPPED", "Cua Driver's background service is not running and could not be started. Start it from Settings > Computer use agents.", nil)
)

var errDisabled = apierr.Forbidden("COMPUTER_USE_DISABLED", "Computer use is turned off. Ask the user to turn on \"Let agents use this computer\" in Settings > Computer use agents.")

func toolNotOffered(tool string) error {
	return apierr.Forbidden("COMPUTER_USE_TOOL_NOT_OFFERED", fmt.Sprintf("%q is not one of the computer-use tools Orchestrator offers: %s.", tool, strings.Join(agentToolNames, ", ")))
}

type Driver interface {
	Locate(ctx context.Context) (string, error)
	Run(ctx context.Context, binary string, args ...string) (stdout, stderr []byte, err error)
	StartDaemon(ctx context.Context, binary string) error
}

type Service struct {
	driver Driver
	images imageStore
	goos   string

	background context.Context
	stop       context.CancelFunc
	workers    sync.WaitGroup

	mu          sync.Mutex
	granting    bool
	grantError  string
	sessionless map[string]bool
	snapshots   snapshotWindows
	agentTools  agentToolCache
	enabled     func(context.Context) bool
}

func (s *Service) SetEnabled(enabled func(context.Context) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = enabled
}

func (s *Service) computerUseEnabled(ctx context.Context) bool {
	s.mu.Lock()
	enabled := s.enabled
	s.mu.Unlock()
	return enabled != nil && enabled(ctx)
}

func New(driver Driver, dataDir string) *Service {
	background, stop := context.WithCancel(context.Background())
	return &Service{
		driver:      driver,
		images:      newImageStore(dataDir),
		goos:        runtime.GOOS,
		background:  background,
		stop:        stop,
		sessionless: map[string]bool{},
		snapshots:   snapshotWindows{windows: map[string]window{}},
	}
}

func (s *Service) Installed(ctx context.Context) (bool, error) {
	_, err := s.driver.Locate(ctx)
	if errors.Is(err, ports.ErrComputerUseDriverNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) Close(ctx context.Context) error {
	s.stop()
	done := make(chan struct{})
	go func() {
		s.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("computeruse: drain workers: %w", ctx.Err())
	}
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	status := Status{Platform: s.goos, Daemon: DaemonUnknown, ImageDirectory: s.images.root}
	binary, err := s.driver.Locate(ctx)
	if errors.Is(err, ports.ErrComputerUseDriverNotFound) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Installed = true
	status.BinaryPath = binary

	var wg sync.WaitGroup
	var update updateCheck
	wg.Add(1)
	go func() {
		defer wg.Done()
		update = s.checkUpdate(ctx, binary)
	}()
	status.Version = s.version(ctx, binary)
	status.Daemon = s.daemonState(ctx, binary)
	if s.goos == "darwin" {
		status.Permissions = s.permissions(ctx, binary, status.Daemon)
	}
	wg.Wait()
	status.LatestVersion = update.LatestVersion
	status.UpdateAvailable = update.UpdateAvailable
	status.ReleaseNotesURL = update.ReleaseNotesURL
	return status, nil
}

func (s *Service) StartDaemon(ctx context.Context) (Status, error) {
	binary, err := s.locate(ctx)
	if err != nil {
		return Status{}, err
	}
	if err := s.ensureDaemon(ctx, binary); err != nil {
		return Status{}, err
	}
	return s.Status(ctx)
}

func (s *Service) GrantPermissions(ctx context.Context) (Status, error) {
	if s.goos != "darwin" {
		return Status{}, apierr.Invalid("COMPUTER_USE_PERMISSIONS_UNSUPPORTED", "Only macOS asks for computer-use permissions.", nil)
	}
	binary, err := s.locate(ctx)
	if err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	if !s.granting {
		s.granting = true
		s.grantError = ""
		s.workers.Add(1)
		go s.grant(binary)
	}
	s.mu.Unlock()
	return s.Status(ctx)
}

func (s *Service) grant(binary string) {
	defer s.workers.Done()
	ctx, cancel := context.WithTimeout(s.background, grantTimeout)
	defer cancel()
	stdout, stderr, err := s.driver.Run(ctx, binary, "permissions", "grant")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.granting = false
	if err != nil && s.background.Err() == nil {
		s.grantError = commandFailure(err, stdout, stderr)
	}
}

func (s *Service) Doctor(ctx context.Context) (string, error) {
	binary, err := s.locate(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	stdout, stderr, err := s.driver.Run(ctx, binary, "doctor")
	report := cleanText(string(stdout) + string(stderr))
	if err != nil && report == "" {
		return "", fmt.Errorf("cua-driver doctor: %w", err)
	}
	return report, nil
}

func (s *Service) Call(ctx context.Context, request CallRequest) (CallResult, error) {
	tool := strings.TrimSpace(request.Tool)
	if !toolNamePattern.MatchString(tool) {
		return CallResult{}, invalidTool(tool)
	}
	if !s.computerUseEnabled(ctx) {
		return CallResult{}, errDisabled
	}
	if !offeredTool(tool) {
		return CallResult{}, toolNotOffered(tool)
	}
	if err := checkActions(tool, request.Arguments); err != nil {
		return CallResult{}, err
	}
	arguments := make(map[string]any, len(request.Arguments)+1)
	for key, value := range request.Arguments {
		arguments[key] = value
	}
	scope, err := s.images.scope(request.SessionID)
	if err != nil {
		return CallResult{}, err
	}
	binary, err := s.locate(ctx)
	if err != nil {
		return CallResult{}, err
	}
	saved := &savedImages{store: s.images, scope: scope, tool: tool, images: []Image{}}
	screenshot := saved.requestScreenshotFile(tool, arguments)
	dropImageFiles(arguments)
	_, named := arguments["session"]
	injected := !named && !s.isSessionless(tool)
	if injected {
		arguments["session"] = driverSession(request.SessionID)
	}
	if tool == "run_steps" {
		if observed := s.prepareObserve(saved, arguments); observed != "" {
			screenshot = observed
		}
	}

	stdout, stderr, runErr := s.callWithDaemon(ctx, binary, tool, arguments)
	if runErr != nil && injected && rejectsSession(stdout) {
		s.markSessionless(tool)
		delete(arguments, "session")
		stdout, stderr, runErr = s.callWithDaemon(ctx, binary, tool, arguments)
	}
	if runErr != nil {
		stdout, stderr, runErr = s.reviveEndedSession(ctx, binary, tool, arguments, stdout, stderr, runErr)
	}
	if runErr != nil && tool == "run_steps" && batchReport(stdout) {
		runErr = nil
	}
	if runErr != nil {
		var apiErr *apierr.Error
		if errors.As(runErr, &apiErr) {
			return CallResult{}, runErr
		}
		if daemonNotRunning(stdout, stderr) {
			return CallResult{}, errDaemonStopped
		}
		if ctx.Err() != nil {
			return CallResult{}, ctx.Err()
		}
		message := commandFailure(runErr, stdout, stderr)
		if code, _ := refusalCode(stdout); driverRefusalHints[code] != "" {
			message += "\n" + driverRefusalHints[code]
		} else if strings.Contains(strings.ToLower(message), "foreground") {
			message += "\n" + foregroundAdvice
		}
		if needsWebTextAdvice(tool, stdout, stderr) {
			message += "\n" + webTextAdvice
		}
		return CallResult{}, apierr.New(driverFailureKind(stdout, stderr), "COMPUTER_USE_CALL_FAILED", message, map[string]any{"tool": tool})
	}

	var result any
	if err := json.Unmarshal(stdout, &result); err != nil {
		result = map[string]any{"text": cleanText(string(stdout))}
	}
	result = saved.externalize(result)
	if screenshot != "" {
		saved.adopt(screenshot)
	}
	s.rememberReads(arguments["session"], result)
	flagUnrendered(tool, arguments, result)
	noteForegroundAdvice(result)
	noteWebTextAdvice(tool, result)
	if observedTools[tool] {
		result = s.observeAfter(ctx, binary, saved, arguments, result)
	}
	s.images.prune(scope)
	return CallResult{Tool: tool, Result: result, Images: saved.images}, nil
}

func (s *Service) callInSession(ctx context.Context, binary, tool string, arguments map[string]any) ([]byte, []byte, error) {
	stdout, stderr, err := s.callWithDaemon(ctx, binary, tool, arguments)
	if err != nil {
		return s.reviveEndedSession(ctx, binary, tool, arguments, stdout, stderr, err)
	}
	return stdout, stderr, nil
}

func (s *Service) reviveEndedSession(ctx context.Context, binary, tool string, arguments map[string]any, stdout, stderr []byte, runErr error) ([]byte, []byte, error) {
	label, named := arguments["session"].(string)
	if !named || !sessionEnded(stderr) {
		return stdout, stderr, runErr
	}
	if _, _, err := s.callWithDaemon(ctx, binary, "start_session", map[string]any{"session": label}); err != nil {
		return stdout, stderr, runErr
	}
	return s.callWithDaemon(ctx, binary, tool, arguments)
}

func (s *Service) callWithDaemon(ctx context.Context, binary, tool string, arguments map[string]any) ([]byte, []byte, error) {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, nil, err
	}
	stdout, stderr, runErr := s.runCall(ctx, binary, tool, encoded)
	if runErr != nil && daemonNotRunning(stdout, stderr) {
		if err := s.ensureDaemon(ctx, binary); err != nil {
			return nil, nil, err
		}
		stdout, stderr, runErr = s.runCall(ctx, binary, tool, encoded)
	}
	return stdout, stderr, runErr
}

func batchReport(stdout []byte) bool {
	var report struct {
		Steps []json.RawMessage `json:"steps"`
	}
	return json.Unmarshal(stdout, &report) == nil && len(report.Steps) > 0
}

func sessionEnded(stderr []byte) bool {
	return endedSession.MatchString(cleanText(string(stderr)))
}

func (s *Service) isSessionless(tool string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionless[tool]
}

func (s *Service) markSessionless(tool string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionless[tool] = true
}

func driverSession(sessionID string) string {
	name := strings.TrimSpace(sessionID)
	if name == "" {
		name = sharedScope
	}
	return "ao-" + name
}

func rejectsSession(stdout []byte) bool {
	code, message := refusalCode(stdout)
	return code == "invalid_arguments" && unknownSessionArg.MatchString(message)
}

func (s *Service) runCall(ctx context.Context, binary, tool string, arguments []byte) ([]byte, []byte, error) {
	timeout := callTimeout
	if tool == "run_steps" {
		timeout = batchTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.driver.Run(ctx, binary, "call", tool, string(arguments))
}

func (s *Service) locate(ctx context.Context) (string, error) {
	binary, err := s.driver.Locate(ctx)
	if errors.Is(err, ports.ErrComputerUseDriverNotFound) {
		return "", errNotInstalled
	}
	return binary, err
}

func (s *Service) ensureDaemon(ctx context.Context, binary string) error {
	if s.daemonState(ctx, binary) == DaemonRunning {
		return nil
	}
	if err := s.driver.StartDaemon(ctx, binary); err != nil {
		return apierr.Unavailable("COMPUTER_USE_DAEMON_START_FAILED", fmt.Sprintf("Could not start Cua Driver's background service: %v", err))
	}
	deadline := time.NewTimer(daemonStartWait)
	defer deadline.Stop()
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errDaemonStopped
		case <-tick.C:
			if s.daemonState(ctx, binary) == DaemonRunning {
				return nil
			}
		}
	}
}

func (s *Service) version(ctx context.Context, binary string) string {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	stdout, _, err := s.driver.Run(ctx, binary, "--version")
	if err != nil {
		return ""
	}
	return parseVersion(string(stdout))
}

func (s *Service) daemonState(ctx context.Context, binary string) DaemonState {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	stdout, stderr, _ := s.driver.Run(ctx, binary, "status")
	output := strings.ToLower(string(stdout) + string(stderr))
	switch {
	case strings.Contains(output, "not running"):
		return DaemonStopped
	case strings.Contains(output, "is running"):
		return DaemonRunning
	default:
		return DaemonUnknown
	}
}

func (s *Service) permissions(ctx context.Context, binary string, daemon DaemonState) *Permissions {
	s.mu.Lock()
	permissions := &Permissions{Accessibility: PermissionUnknown, ScreenRecording: PermissionUnknown, Granting: s.granting, GrantError: s.grantError}
	s.mu.Unlock()
	if daemon != DaemonRunning {
		return permissions
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	stdout, _, err := s.driver.Run(ctx, binary, "permissions", "status", "--json")
	if err != nil {
		return permissions
	}
	var report struct {
		Accessibility   *bool `json:"accessibility"`
		ScreenRecording *bool `json:"screen_recording"`
	}
	if json.Unmarshal(stdout, &report) != nil {
		return permissions
	}
	permissions.Accessibility = permissionState(report.Accessibility)
	permissions.ScreenRecording = permissionState(report.ScreenRecording)
	return permissions
}

type updateCheck struct {
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseNotesURL string `json:"release_notes_url"`
}

func (s *Service) checkUpdate(ctx context.Context, binary string) updateCheck {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	stdout, _, err := s.driver.Run(ctx, binary, "check-update", "--json")
	var check updateCheck
	if err != nil || json.Unmarshal(stdout, &check) != nil {
		return updateCheck{}
	}
	return check
}

func permissionState(granted *bool) PermissionState {
	switch {
	case granted == nil:
		return PermissionUnknown
	case *granted:
		return PermissionGranted
	default:
		return PermissionMissing
	}
}

func invalidTool(tool string) error {
	return apierr.Invalid("COMPUTER_USE_INVALID_TOOL", fmt.Sprintf("%q is not a Cua Driver tool name.", tool), nil)
}

func daemonNotRunning(stdout, stderr []byte) bool {
	return bytes.Contains(bytes.ToLower(append(append([]byte(nil), stdout...), stderr...)), []byte("daemon is not running"))
}

func commandFailure(err error, stdout, stderr []byte) string {
	message := cleanText(string(stderr))
	if message == "" {
		message = cleanText(string(stdout))
	}
	if message == "" {
		return err.Error()
	}
	const limit = 2000
	if len(message) > limit {
		message = "…" + message[len(message)-limit:]
	}
	return message
}

func cleanText(text string) string {
	return strings.TrimSpace(terminalEscape.ReplaceAllString(strings.ReplaceAll(text, "\r\n", "\n"), ""))
}

func parseVersion(output string) string {
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimPrefix(fields[len(fields)-1], "v")
}
