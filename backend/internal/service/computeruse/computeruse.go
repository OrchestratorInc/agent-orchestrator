// Package computeruse is AO's integration with Cua Driver, the computer-use
// driver behind the agents' computer-use tools. It reports whether the driver is
// installed and ready, starts its background service, and runs driver tools
// on an agent's behalf.
//
// The one rule this package exists to enforce: images never come back inline.
// Cua Driver's CLI prints screenshots as base64, which is megabytes of text an
// agent would read into its context. Every call here asks the driver to write
// screenshots to files, decodes any image it still returns inline, and hands
// back file paths under AO's data directory instead.
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

// DaemonState is whether Cua Driver's background service answers.
type DaemonState string

// The DaemonState values.
const (
	DaemonRunning DaemonState = "running"
	DaemonStopped DaemonState = "stopped"
	DaemonUnknown DaemonState = "unknown"
)

// PermissionState is one macOS privacy permission as CuaDriver.app holds it.
type PermissionState string

// The PermissionState values.
const (
	PermissionGranted PermissionState = "granted"
	PermissionMissing PermissionState = "missing"
	PermissionUnknown PermissionState = "unknown"
)

// Permissions are the macOS privacy grants Cua Driver needs. They belong to
// CuaDriver.app, not AO, and can only be read while its service runs.
type Permissions struct {
	Accessibility   PermissionState `json:"accessibility" enum:"granted,missing,unknown" description:"Accessibility, needed to read and operate app controls."`
	ScreenRecording PermissionState `json:"screenRecording" enum:"granted,missing,unknown" description:"Screen Recording, needed for screenshots."`
	Granting        bool            `json:"granting" description:"A permission request is waiting on the user in System Settings."`
	GrantError      string          `json:"grantError,omitempty" description:"Why the last permission request failed."`
}

// Status is everything the settings page shows about Cua Driver.
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

// CallRequest runs one driver tool.
type CallRequest struct {
	Tool string `json:"tool" description:"Driver tool name, e.g. list_windows or get_window_state."`
	// Arguments is the tool's JSON object input; empty means {}.
	Arguments map[string]any `json:"arguments,omitempty" description:"Tool arguments as a JSON object."`
	// SessionID groups saved screenshots by AO session and names the driver
	// session the call runs in; empty uses a shared folder and session.
	SessionID string `json:"sessionId,omitempty" description:"AO session the call is made for. Screenshots are saved under it, and calls for the same AO session share one Cua Driver session unless arguments.session names another."`
}

// Image is a screenshot a call saved to disk.
type Image struct {
	Path     string `json:"path"`
	MimeType string `json:"mimeType"`
}

// CallResult is a tool's output with every image replaced by a file path.
type CallResult struct {
	Tool   string  `json:"tool"`
	Result any     `json:"result" description:"The tool's JSON output. Inline images are replaced by *_file_path fields. After an action, observation holds AO's read of the window: the tree diff and a screenshot file."`
	Images []Image `json:"images" description:"Screenshots this call saved. Open them with an image viewer."`
}

const (
	probeTimeout  = 10 * time.Second
	callTimeout   = 45 * time.Second
	doctorTimeout = 30 * time.Second
	// batchTimeout is the driver's 60 s limit for a whole run_steps batch,
	// plus time for its step report and closing window read.
	batchTimeout = 75 * time.Second
	// grantTimeout bounds a permission request left waiting on the user.
	grantTimeout = 10 * time.Minute
	// daemonStartWait is how long Start waits for the service to answer.
	daemonStartWait = 8 * time.Second
)

var (
	toolNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	unknownSessionArg = regexp.MustCompile(`\bunknown argument session\b`)
	// endedSession is the driver's refusal, on stderr, of a call that names a
	// session that ended: "session has ended; tool call 'click' was rejected.
	// Call start_session with session 'ao-x' to start it again, ...".
	endedSession     = regexp.MustCompile(`^session has ended; tool call '[a-z0-9_]+' was rejected\b`)
	terminalEscape   = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	errNotInstalled  = apierr.Conflict("COMPUTER_USE_NOT_INSTALLED", "Cua Driver is not installed. Install it in Settings > Computer use agents.", nil)
	errDaemonStopped = apierr.Conflict("COMPUTER_USE_DAEMON_STOPPED", "Cua Driver's background service is not running and could not be started. Start it from Settings > Computer use agents.", nil)
)

// errDisabled refuses calls while "Let agents use this computer" is off. A
// chat session started while it was on keeps its tools listed, so turning it
// off also cuts off sessions already running.
var errDisabled = apierr.Forbidden("COMPUTER_USE_DISABLED", "Computer use is turned off. Ask the user to turn on \"Let agents use this computer\" in Settings > Computer use agents.")

// toolNotOffered refuses a driver tool outside the ones AO offers agents. The
// route is loopback-only but open to any local process, such as a terminal
// agent with curl, so it runs no more than chat agents get: no browser tools,
// scripts, whole-screen capture, or driver configuration.
func toolNotOffered(tool string) error {
	return apierr.Forbidden("COMPUTER_USE_TOOL_NOT_OFFERED", fmt.Sprintf("%q is not one of the computer-use tools AO offers: %s.", tool, strings.Join(agentToolNames, ", ")))
}

// Driver locates and runs the locally installed Cua Driver CLI. Callers build
// every argument themselves; the adapter only finds the binary and executes
// it.
type Driver interface {
	// Locate returns the cua-driver executable, or
	// ports.ErrComputerUseDriverNotFound.
	Locate(ctx context.Context) (string, error)
	// Run executes binary with args and returns its captured output. A non-zero
	// exit is returned as err alongside whatever the command printed.
	Run(ctx context.Context, binary string, args ...string) (stdout, stderr []byte, err error)
	// StartDaemon starts Cua Driver's background service the way its own docs
	// prescribe for this platform and returns without waiting for it to exit.
	StartDaemon(ctx context.Context, binary string) error
}

// Service runs Cua Driver for the settings page and the computer-use tools.
type Service struct {
	driver Driver
	images imageStore
	goos   string

	background context.Context
	stop       context.CancelFunc
	workers    sync.WaitGroup

	mu         sync.Mutex
	granting   bool
	grantError string
	// sessionless are tools the driver refused a `session` argument for.
	sessionless map[string]bool
	snapshots   snapshotWindows
	agentTools  agentToolCache
	// enabled reports "Let agents use this computer"; nil is off.
	enabled func(context.Context) bool
}

// SetEnabled supplies the "Let agents use this computer" setting, which Call
// checks on every call.
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

// New returns a Service that saves screenshots under dataDir/computer-use.
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

// Installed reports whether Cua Driver is installed, without probing it.
func (s *Service) Installed(ctx context.Context) (bool, error) {
	_, err := s.driver.Locate(ctx)
	if errors.Is(err, ports.ErrComputerUseDriverNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Close cancels a pending permission request and waits for it to stop.
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

// Status reports install, service, update, and permission state. A driver
// that is missing is a normal answer, not an error.
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

	// The update check may reach GitHub; run it beside the local probes.
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

// StartDaemon starts Cua Driver's background service and waits briefly for
// it to answer.
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

// GrantPermissions asks macOS for the permissions Cua Driver needs. The
// request waits on the user in System Settings, so it runs in the background
// and Status reports its progress.
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
	// `permissions grant` launches CuaDriver.app through LaunchServices, so
	// macOS attributes the request to the driver rather than to AO.
	stdout, stderr, err := s.driver.Run(ctx, binary, "permissions", "grant")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.granting = false
	if err != nil && s.background.Err() == nil {
		s.grantError = commandFailure(err, stdout, stderr)
	}
}

// Doctor returns the driver's own environment report.
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

// Call runs one driver tool and returns its output with images saved to
// files. A stopped service is started once and the call retried.
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
	// Each `cua-driver call` is a new connection, and the driver gives each
	// one a disposable implicit session. A pixel click or zoom needs the
	// screenshot an earlier call took in the same session, so every call for
	// one AO session names the same driver session unless the caller chose one.
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
		// A refusal runs nothing, so the call can be sent again without it.
		s.markSessionless(tool)
		delete(arguments, "session")
		stdout, stderr, runErr = s.callWithDaemon(ctx, binary, tool, arguments)
	}
	if runErr != nil {
		stdout, stderr, runErr = s.reviveEndedSession(ctx, binary, tool, arguments, stdout, stderr, runErr)
	}
	if runErr != nil && tool == "run_steps" && batchReport(stdout) {
		// A step failed after the batch ran: its per-step report and
		// observation say which step and what the window shows now.
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

// callInSession runs one driver call in its named driver session, reviving
// the session once if it has ended.
func (s *Service) callInSession(ctx context.Context, binary, tool string, arguments map[string]any) ([]byte, []byte, error) {
	stdout, stderr, err := s.callWithDaemon(ctx, binary, tool, arguments)
	if err != nil {
		return s.reviveEndedSession(ctx, binary, tool, arguments, stdout, stderr, err)
	}
	return stdout, stderr, nil
}

// reviveEndedSession retries a failed call once when the driver refused it
// because its named session ended: a driver session ends after a few idle
// minutes, and the driver then refuses every call naming it until
// start_session revives the name. Any other failure is returned as it was.
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

// callWithDaemon runs one driver call, starting a stopped service once and
// retrying.
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

// batchReport reports whether run_steps output is its per-step report. A
// batch the driver refused before running has none.
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

// driverSession is the Cua Driver session label for an AO session. Calls made
// outside any AO session share one label, the way they share one screenshot
// folder.
func driverSession(sessionID string) string {
	name := strings.TrimSpace(sessionID)
	if name == "" {
		name = sharedScope
	}
	return "ao-" + name
}

// rejectsSession reports the driver's refusal of an unexpected `session`
// argument: {"status":"refused","refusal":{"code":"invalid_arguments",
// "message":"<tool>: unknown argument session; ..."}}.
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

// ensureDaemon starts the service when it is not answering and waits for it.
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
	// The answer comes from the running service; without it the driver can
	// only report the caller's grants, which are not the ones that matter.
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

// checkUpdate asks the driver about its update channel. The driver caches
// the answer, so this is cheap after the first call; any failure just leaves
// the update unknown.
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

// commandFailure is the driver's own explanation of a failure, falling back
// to the exit error.
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

// parseVersion reads "cua-driver 0.28.2" (or a bare version).
func parseVersion(output string) string {
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimPrefix(fields[len(fields)-1], "v")
}
