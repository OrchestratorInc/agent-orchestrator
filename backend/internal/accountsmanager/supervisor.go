// Package accountsmanager supervises AO's private, loopback-only Accounts
// Manager runner without making it part of daemon readiness.
package accountsmanager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

const (
	serviceName               = "ao-accounts-manager"
	leaseInterval             = 5 * time.Second
	healthTimeout             = 10 * time.Second
	stableRunReset            = 60 * time.Second
	maxRestartDelay           = 30 * time.Second
	privateHTTPDelay          = 100 * time.Millisecond
	credentialProtocolVersion = 4
)

// State describes runner availability without affecting daemon readiness.
type State string

// StateStarting and the other states describe the supervision lifecycle.
const (
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateDegraded State = "degraded"
)

// Reason identifies a safe failure category without private process details.
type Reason string

// ReasonBinaryMissing and the other reasons are public-safe degradation codes.
const (
	ReasonBinaryMissing        Reason = "binary_missing"
	ReasonConfigurationInvalid Reason = "configuration_invalid"
	ReasonStartFailed          Reason = "start_failed"
	ReasonHealthTimeout        Reason = "health_timeout"
	ReasonProcessExited        Reason = "process_exited"
)

// Status is safe for public projection. It deliberately excludes process and
// transport details.
type Status struct {
	State         State
	Reason        Reason
	EngineVersion string
}

// Endpoint is private daemon-only connection material.
type Endpoint struct {
	BaseURL         string
	ClientToken     string
	ManagementToken string
}

// Config supplies the private state root, runner executable, and supervision dependencies.
type Config struct {
	StateDir     string
	Binary       string
	HTTPClient   *http.Client
	Logger       *slog.Logger
	launch       launchProcess
	processAlive func(int) bool
}

// Supervisor attaches to or restarts the isolated runner without blocking daemon startup.
type Supervisor struct {
	cfg    Config
	client *http.Client
	log    *slog.Logger
	launch launchProcess
	alive  func(int) bool
	once   sync.Once

	mu       sync.RWMutex
	status   Status
	endpoint Endpoint
}

type controlIdentity struct {
	Service            string `json:"service"`
	InstanceID         string `json:"instanceId"`
	RunnerVersion      string `json:"runnerVersion"`
	EngineVersion      string `json:"engineVersion"`
	CredentialProtocol int    `json:"credentialProtocol"`
}

type managedProcess interface {
	Kill() error
}

type launchProcess func(binary, stateRoot string) (managedProcess, <-chan error, error)

// New creates a supervisor without starting a runner or writing state.
func New(cfg Config) *Supervisor {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	bounded := *client
	if bounded.Timeout <= 0 || bounded.Timeout > 2*time.Second {
		bounded.Timeout = 2 * time.Second
	}
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	launch := cfg.launch
	if launch == nil {
		launch = launchRunnerProcess
	}
	alive := cfg.processAlive
	if alive == nil {
		alive = processalive.Alive
	}
	return &Supervisor{cfg: cfg, client: &bounded, log: log, launch: launch, alive: alive, status: Status{State: StateStarting}}
}

// Start begins supervision once and stops monitoring when ctx is cancelled.
func (s *Supervisor) Start(ctx context.Context) {
	s.once.Do(func() { go s.run(ctx) })
}

// Status returns a redacted, concurrency-safe health snapshot.
func (s *Supervisor) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// Endpoint returns private connection material with a readiness flag callers must check.
func (s *Supervisor) Endpoint() (Endpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.endpoint, s.status.State == StateReady
}

func (s *Supervisor) run(ctx context.Context) {
	state, err := ensureState(s.cfg.StateDir)
	if err != nil {
		s.setDegraded(ReasonConfigurationInvalid)
		s.log.Warn("accounts manager unavailable", "reason", ReasonConfigurationInvalid)
		return
	}
	if runtimeRecord, readErr := readRuntimeRecord(state.Root); readErr == nil {
		if endpoint, ok := s.tryAttach(ctx, runtimeRecord, state.ControlKey, state.ClientKey, state.ManagementKey); ok {
			s.setReady(endpoint, runtimeRecord.UpstreamVersion)
			s.log.Info("accounts manager attached")
			s.maintainAttached(ctx, runtimeRecord, state)
			if ctx.Err() != nil {
				return
			}
		}
	}

	if !usableBinary(s.cfg.Binary) {
		s.setDegraded(ReasonBinaryMissing)
		s.log.Warn("accounts manager unavailable", "reason", ReasonBinaryMissing)
		return
	}

	for attempt := 0; ctx.Err() == nil; attempt++ {
		port, portErr := selectAvailablePort(state.Port)
		if portErr != nil {
			s.setDegraded(ReasonStartFailed)
			if !waitContext(ctx, restartBackoff(attempt)) {
				return
			}
			continue
		}
		if port != state.Port {
			if err = updateConfigPort(state.ConfigPath, port); err != nil {
				s.setDegraded(ReasonConfigurationInvalid)
				return
			}
			state.Port = port
		}

		startedAt := time.Now()
		proc, wait, startErr := s.launch(s.cfg.Binary, state.Root)
		if startErr != nil {
			s.setDegraded(ReasonStartFailed)
		} else {
			record, endpoint, readyErr := s.awaitReady(ctx, state, wait)
			if readyErr == nil {
				s.setReady(endpoint, record.UpstreamVersion)
				s.log.Info("accounts manager ready")
				runErr := s.maintainSpawned(ctx, record, state, proc, wait)
				if ctx.Err() != nil {
					s.log.Debug("accounts manager supervisor stopped; runner left available for reattach")
					return
				}
				if time.Since(startedAt) >= stableRunReset {
					attempt = -1
				}
				_ = runErr
				s.setDegraded(ReasonProcessExited)
			} else {
				if errors.Is(readyErr, context.DeadlineExceeded) {
					s.setDegraded(ReasonHealthTimeout)
				} else {
					s.setDegraded(ReasonProcessExited)
				}
				_ = proc.Kill()
			}
		}
		if !waitContext(ctx, restartBackoff(attempt)) {
			return
		}
	}
}

func launchRunnerProcess(binary, stateRoot string) (managedProcess, <-chan error, error) {
	cmd := exec.Command(binary, "serve", "--state-dir", stateRoot) //nolint:gosec // explicit packaged binary path and fixed argv.
	// Electron stops the daemon by signalling its process group and a dev
	// terminal may close the daemon's controlling session. Give the runner an
	// independent session so a replacement daemon can reattach during the
	// lease window instead of the desktop restart killing both processes.
	configureRunnerProcess(cmd)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	return cmd.Process, wait, nil
}

func (s *Supervisor) awaitReady(ctx context.Context, state privateState, wait <-chan error) (RuntimeRecord, Endpoint, error) {
	timer := time.NewTimer(healthTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(privateHTTPDelay)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return RuntimeRecord{}, Endpoint{}, ctx.Err()
		case err := <-wait:
			if err == nil {
				err = errors.New("runner exited")
			}
			return RuntimeRecord{}, Endpoint{}, err
		case <-timer.C:
			return RuntimeRecord{}, Endpoint{}, context.DeadlineExceeded
		case <-ticker.C:
			record, err := readRuntimeRecord(state.Root)
			if err != nil {
				continue
			}
			if endpoint, ok := s.tryAttach(ctx, record, state.ControlKey, state.ClientKey, state.ManagementKey); ok {
				return record, endpoint, nil
			}
		}
	}
}

func (s *Supervisor) maintainAttached(ctx context.Context, record RuntimeRecord, state privateState) {
	ticker := time.NewTicker(leaseInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, ok := s.tryAttach(ctx, record, state.ControlKey, state.ClientKey, state.ManagementKey); !ok {
				s.setDegraded(ReasonProcessExited)
				return
			}
		}
	}
}

func (s *Supervisor) maintainSpawned(ctx context.Context, record RuntimeRecord, state privateState, proc managedProcess, wait <-chan error) error {
	ticker := time.NewTicker(leaseInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-wait:
			if err == nil {
				return errors.New("runner exited")
			}
			return err
		case <-ticker.C:
			if _, ok := s.tryAttach(ctx, record, state.ControlKey, state.ClientKey, state.ManagementKey); !ok {
				// A daemon replacement cancels ctx while an in-flight lease request
				// may still be completing. That cancellation is not evidence that the
				// runner failed: leave it alive for the replacement daemon to attach.
				if ctx.Err() != nil {
					return nil
				}
				s.log.Warn("accounts manager control check failed; stopping runner")
				_ = proc.Kill()
				return errors.New("runner control check failed")
			}
		}
	}
}

func (s *Supervisor) tryAttach(ctx context.Context, record RuntimeRecord, controlKey, clientKey, managementKey string) (Endpoint, bool) {
	if !s.alive(record.PID) || record.Port < 1 || record.Port > 65535 || record.InstanceID == "" || controlKey == "" || clientKey == "" || managementKey == "" {
		return Endpoint{}, false
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(record.Port)
	var identity controlIdentity
	if !s.requestJSON(ctx, http.MethodGet, baseURL+"/ao/internal/identity", controlKey, &identity) {
		return Endpoint{}, false
	}
	if identity.Service != serviceName || identity.InstanceID != record.InstanceID || identity.CredentialProtocol != credentialProtocolVersion {
		return Endpoint{}, false
	}
	if !s.requestOK(ctx, http.MethodGet, baseURL+"/healthz", "", http.StatusOK) {
		return Endpoint{}, false
	}
	if !s.requestOK(ctx, http.MethodPost, baseURL+"/ao/internal/lease", controlKey, http.StatusNoContent) {
		return Endpoint{}, false
	}
	return Endpoint{BaseURL: baseURL, ClientToken: clientKey, ManagementToken: managementKey}, true
}

func (s *Supervisor) requestJSON(ctx context.Context, method, url, key string, dst any) bool {
	req, err := http.NewRequestWithContext(ctx, method, url, http.NoBody)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := s.client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(dst) == nil
}

func (s *Supervisor) requestOK(ctx context.Context, method, url, key string, want int) bool {
	req, err := http.NewRequestWithContext(ctx, method, url, http.NoBody)
	if err != nil {
		return false
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	return res.StatusCode == want
}

func (s *Supervisor) setReady(endpoint Endpoint, engineVersion string) {
	s.mu.Lock()
	s.endpoint = endpoint
	s.status = Status{State: StateReady, EngineVersion: strings.TrimSpace(engineVersion)}
	s.mu.Unlock()
}

func (s *Supervisor) setDegraded(reason Reason) {
	s.mu.Lock()
	s.endpoint = Endpoint{}
	s.status = Status{State: StateDegraded, Reason: reason}
	s.mu.Unlock()
}

func usableBinary(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func restartBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := time.Second << min(attempt, 5)
	if delay > maxRestartDelay {
		return maxRestartDelay
	}
	return delay
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
