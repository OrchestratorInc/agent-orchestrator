package reconcile

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// buildFailureProvider is a provider whose workspace build failed, counting
// the transitions the reconciler asks for.
type buildFailureProvider struct {
	lifecycleProvider
	stops     int
	creates   int
	createErr error
}

func (p *buildFailureProvider) Stop(context.Context, sandbox.ID) error {
	p.stops++
	return nil
}

func (p *buildFailureProvider) FindBySession(context.Context, string) (sandbox.Environment, bool, error) {
	return sandbox.Environment{}, false, nil
}

func (p *buildFailureProvider) Create(context.Context, sandbox.Spec) (sandbox.Environment, error) {
	p.creates++
	if p.createErr != nil {
		return sandbox.Environment{}, p.createErr
	}
	return p.environment, nil
}

const (
	vcpuBuildMessage = "The workspace failed to build: VcpuLimitExceeded: You have requested more vCPU capacity than your current vCPU limit of 32 allows."
	failedBuildID    = "build-1"
)

func failedBuildEnvironment(buildID string) sandbox.Environment {
	return sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateFailed,
		Failure: &sandbox.Failure{
			ID:  buildID,
			Err: &sandbox.StartupError{Code: sandbox.StartupErrorWorkspaceBuildFailed, Message: vcpuBuildMessage},
		},
	}
}

// startingRecord is a session whose worker has not checked in, in a startup
// window opened a minute and a half ago.
func startingRecord(connectionID string) domain.Sandbox {
	startedAt := time.Now().Add(-90 * time.Second)
	return domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderCoder,
		ProviderEnvironmentID: "workspace-1",
		ProviderConnectionID:  connectionID,
		DesiredState:          domain.SandboxDesiredRunning,
		ObservedState:         domain.SandboxObservedProvisioning,
		StartupStartedAt:      &startedAt,
		UpdatedAt:             startedAt,
	}
}

func terminatedObservations(observations []string) int {
	count := 0
	for _, state := range observations {
		if state == domain.SandboxObservedTerminated {
			count++
		}
	}
	return count
}

// A workspace build that failed parks the session at once with Coder's
// reason, instead of leaving it "provisioning" for the whole startup budget,
// and remembers the failed build so it is reported once.
func TestFailedBuildParksSessionWithBuildError(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		connectionID string
		wantStops    int
	}{
		// AO's own compute is stopped so nothing half-built keeps billing.
		{name: "managed", wantStops: 1},
		// A customer's own workspace is left exactly as it is.
		{name: "bring your own", connectionID: "connection-1", wantStops: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &lifecycleStore{}
			provider := &buildFailureProvider{lifecycleProvider: lifecycleProvider{
				environment: failedBuildEnvironment(failedBuildID),
			}}
			if err := testReconciler(store, provider).reconcileSandbox(
				context.Background(), startingRecord(test.connectionID),
			); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if len(store.startupErrors) != 1 ||
				store.startupErrors[0] != (startupErrorRecord{code: sandbox.StartupErrorWorkspaceBuildFailed, message: vcpuBuildMessage}) {
				t.Fatalf("startup errors = %+v", store.startupErrors)
			}
			if len(store.observations) != 1 || store.observations[0] != domain.SandboxObservedTerminated {
				t.Fatalf("observations = %v, want terminated", store.observations)
			}
			if strings.Join(store.providerFailures, ",") != failedBuildID {
				t.Fatalf("provider failures = %v, want the failed build recorded", store.providerFailures)
			}
			if provider.starts != 0 || provider.stops != test.wantStops {
				t.Fatalf("starts = %d stops = %d, want 0 and %d", provider.starts, provider.stops, test.wantStops)
			}
		})
	}
}

// A reported failure that was not retried stays parked: no new build, no
// second report, no stop.
func TestReportedBuildFailureStaysParked(t *testing.T) {
	t.Parallel()
	store := &lifecycleStore{}
	provider := &buildFailureProvider{lifecycleProvider: lifecycleProvider{
		environment: failedBuildEnvironment(failedBuildID),
	}}
	record := startingRecord("connection-1")
	record.ObservedState = domain.SandboxObservedTerminated
	record.ProviderFailureID = failedBuildID
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.starts != 0 || provider.stops != 0 || len(store.startupErrors) != 0 || len(store.providerFailures) != 0 {
		t.Fatalf("starts = %d stops = %d startup errors = %+v marks = %v",
			provider.starts, provider.stops, store.startupErrors, store.providerFailures)
	}
	if strings.Join(store.observations, ",") != domain.SandboxObservedTerminated {
		t.Fatalf("observations = %v, want still terminated", store.observations)
	}
}

// A user retry re-arms a parked session (observed provisioning). The reported
// failed build is replaced by exactly one new build, even if a later pass
// still sees the old failed build.
func TestRetryAfterFailedBuildStartsOneNewBuild(t *testing.T) {
	t.Parallel()
	store := &lifecycleStore{}
	provider := &buildFailureProvider{lifecycleProvider: lifecycleProvider{
		environment: failedBuildEnvironment(failedBuildID),
	}}
	reconciler := testReconciler(store, provider)
	retried := startingRecord("connection-1")
	retried.ProviderFailureID = failedBuildID
	if err := reconciler.reconcileSandbox(context.Background(), retried); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.starts != 1 || provider.stops != 0 || len(store.startupErrors) != 0 {
		t.Fatalf("starts = %d stops = %d startup errors = %+v; want one new build and no error",
			provider.starts, provider.stops, store.startupErrors)
	}
	if strings.Join(store.providerFailures, ",") != failedBuildID+"#restarted" {
		t.Fatalf("provider failures = %v, want the restart recorded before Start", store.providerFailures)
	}
	if strings.Join(store.observations, ",") != domain.SandboxObservedProvisioning {
		t.Fatalf("observations = %v, want provisioning", store.observations)
	}

	// The same failed build seen again in this window starts nothing.
	retried.ProviderFailureID = failedBuildID + "#restarted"
	if err := reconciler.reconcileSandbox(context.Background(), retried); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if provider.starts != 1 {
		t.Fatalf("starts = %d after the restart was recorded, want 1", provider.starts)
	}

	// The new build fails too: a new failure, reported and parked.
	provider.environment = failedBuildEnvironment("build-2")
	if err := reconciler.reconcileSandbox(context.Background(), retried); err != nil {
		t.Fatalf("third reconcile: %v", err)
	}
	if provider.starts != 1 || len(store.startupErrors) != 1 ||
		store.startupErrors[0].code != sandbox.StartupErrorWorkspaceBuildFailed ||
		store.providerFailures[len(store.providerFailures)-1] != "build-2" {
		t.Fatalf("starts = %d startup errors = %+v marks = %v; want the new failure parked",
			provider.starts, store.startupErrors, store.providerFailures)
	}
}

// startFailingProvider refuses the new build a retry asks for.
type startFailingProvider struct{ buildFailureProvider }

func (p *startFailingProvider) Start(context.Context, sandbox.ID) error {
	p.starts++
	return errors.New("coder: API returned 500: internal error")
}

// A retry whose new build cannot be started parks the session again and lets
// the next retry try once more.
func TestRetryWhoseNewBuildCannotStartParksAgain(t *testing.T) {
	t.Parallel()
	store := &lifecycleStore{}
	provider := &startFailingProvider{buildFailureProvider{lifecycleProvider: lifecycleProvider{
		environment: failedBuildEnvironment(failedBuildID),
	}}}
	retried := startingRecord("connection-1")
	retried.ProviderFailureID = failedBuildID
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), retried); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.starts != 1 || terminatedObservations(store.observations) != 1 || len(store.startupErrors) != 1 ||
		strings.Contains(store.startupErrors[0].message, "internal error") {
		t.Fatalf("starts = %d observations = %v startup errors = %+v",
			provider.starts, store.observations, store.startupErrors)
	}
	if strings.Join(store.providerFailures, ",") != failedBuildID+"#restarted,"+failedBuildID {
		t.Fatalf("provider failures = %v, want the restart undone", store.providerFailures)
	}
}

// markingStore applies provider failure marks to the record the test feeds
// back, as the database would.
type markingStore struct {
	lifecycleStore
	record *domain.Sandbox
}

func (s *markingStore) RecordSandboxProviderFailure(ctx context.Context, owner, orgID, sessionID, failureID string) error {
	s.record.ProviderFailureID = failureID
	return s.lifecycleStore.RecordSandboxProviderFailure(ctx, owner, orgID, sessionID, failureID)
}

// While a worker is still checking in (an old workspace serving it), a build
// that keeps failing is left alone exactly as before: no build is started and
// the session is not parked, including on passes where the observation has
// cleared the worker's last check-in.
func TestFailedBuildWithConnectedWorkerNeverStartsABuild(t *testing.T) {
	t.Parallel()
	record := startingRecord("connection-1")
	store := &markingStore{record: &record}
	provider := &buildFailureProvider{lifecycleProvider: lifecycleProvider{
		environment: failedBuildEnvironment(failedBuildID),
	}}
	reconciler := testReconciler(store, provider)
	for pass := range 6 {
		if pass%2 == 0 {
			seen := time.Now()
			record.WorkerLastSeenAt = &seen
			record.StartupStartedAt = nil
			record.ObservedState = domain.SandboxObservedRunning
		} else {
			started := time.Now()
			record.WorkerLastSeenAt = nil
			record.StartupStartedAt = &started
			record.ObservedState = domain.SandboxObservedProvisioning
		}
		if err := reconciler.reconcileSandbox(context.Background(), record); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if provider.starts != 0 || terminatedObservations(store.observations) != 0 || len(store.startupErrors) != 0 {
		t.Fatalf("starts = %d parked = %d startup errors = %+v; want the failure left alone",
			provider.starts, terminatedObservations(store.observations), store.startupErrors)
	}
}

// A failed stop or delete build is not a startup failure. A customer's
// workspace in that state is never started by AO on its own.
func TestFailedStopBuildOfCustomerWorkspaceStartsNothing(t *testing.T) {
	t.Parallel()
	store := &lifecycleStore{}
	provider := &buildFailureProvider{lifecycleProvider: lifecycleProvider{
		environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateFailed},
	}}
	if err := testReconciler(store, provider).reconcileSandbox(
		context.Background(), startingRecord("connection-1"),
	); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.starts != 0 || provider.stops != 0 || terminatedObservations(store.observations) != 0 {
		t.Fatalf("starts = %d stops = %d observations = %v", provider.starts, provider.stops, store.observations)
	}
}

// A paused session whose stop build failed asks for the stop again; a failed
// build never restarts compute the user paused.
func TestFailedBuildOfPausedSessionIsStoppedAgain(t *testing.T) {
	t.Parallel()
	store := &lifecycleStore{}
	provider := &buildFailureProvider{lifecycleProvider: lifecycleProvider{
		environment: sandbox.Environment{ID: "workspace-1", State: sandbox.StateFailed},
	}}
	record := runningRecord(false)
	record.DesiredState = domain.SandboxDesiredPaused
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.stops != 1 || provider.starts != 0 || len(store.startupErrors) != 0 {
		t.Fatalf("stops = %d starts = %d startup errors = %+v", provider.stops, provider.starts, store.startupErrors)
	}
}

// uncreatedRecord is a session whose compute has never been created: Create
// has been failing since startedAt.
func uncreatedRecord(startedAt time.Time) domain.Sandbox {
	return domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		DesiredState:     domain.SandboxDesiredRunning,
		ObservedState:    domain.SandboxObservedFailed,
		StartupStartedAt: &startedAt,
		UpdatedAt:        time.Now(),
	}
}

// Compute that cannot be created is bounded like compute that never becomes
// ready: AO keeps retrying silently within the startup budget, explains why
// past it, and parks the session past the hard ceiling.
func TestCreateFailureIsBoundedByStartupBudget(t *testing.T) {
	t.Parallel()
	createErr := errors.New(`coder: API returned 400: {"message":"Unable to create workspace","detail":"token=abc123secretvalue rejected"}`)
	for _, test := range []struct {
		name          string
		age           time.Duration
		wantErrors    int
		wantParked    bool
		wantFailures  int
		wantProviders int
	}{
		{name: "within budget", age: time.Minute, wantErrors: 0, wantFailures: 1, wantProviders: 1},
		{name: "past budget", age: DefaultStartupTimeout + time.Minute, wantErrors: 1, wantFailures: 1, wantProviders: 1},
		{name: "past ceiling", age: DefaultTerminalStartupTimeout + time.Minute, wantErrors: 1, wantParked: true, wantProviders: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &lifecycleStore{}
			provider := &buildFailureProvider{createErr: createErr}
			err := testReconciler(store, provider).reconcileSandbox(
				context.Background(), uncreatedRecord(time.Now().Add(-test.age)),
			)
			if test.wantParked != (err == nil) {
				t.Fatalf("reconcile error = %v, parked = %t", err, test.wantParked)
			}
			if provider.creates != test.wantProviders || len(store.failures) != test.wantFailures {
				t.Fatalf("creates = %d failures = %v", provider.creates, store.failures)
			}
			if len(store.startupErrors) != test.wantErrors {
				t.Fatalf("startup errors = %+v, want %d", store.startupErrors, test.wantErrors)
			}
			if test.wantErrors > 0 {
				got := store.startupErrors[0]
				// The raw API response is for operators, never the message.
				if got.code != sandbox.StartupErrorWorkspaceCreateFailed ||
					got.message != "AO couldn't create this session's workspace. Try again shortly." {
					t.Fatalf("startup error = %+v", got)
				}
			}
			parked := len(store.observations) == 1 && store.observations[0] == domain.SandboxObservedTerminated
			if parked != test.wantParked {
				t.Fatalf("observations = %v, want parked = %t", store.observations, test.wantParked)
			}
		})
	}
}

// A provider with no capacity is bounded the same way.
func TestCapacityWaitIsBoundedByStartupBudget(t *testing.T) {
	t.Parallel()
	store := &lifecycleStore{}
	provider := &buildFailureProvider{createErr: sandbox.ErrAtCapacity}
	record := uncreatedRecord(time.Now().Add(-(DefaultStartupTimeout + time.Minute)))
	record.ObservedState = domain.SandboxObservedProvisioning
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(store.startupErrors) != 1 || store.startupErrors[0].code != sandbox.StartupErrorWorkspaceCreateFailed ||
		!strings.Contains(store.startupErrors[0].message, "no capacity") {
		t.Fatalf("startup errors = %+v", store.startupErrors)
	}
	if len(store.observations) != 1 || store.observations[0] != domain.SandboxObservedProvisioning {
		t.Fatalf("observations = %v, want still waiting for capacity", store.observations)
	}
}

// parkedRecord is a session parked by a startup failure recorded after its
// startup window opened.
func parkedRecord(environmentID, connectionID string) domain.Sandbox {
	startedAt := time.Now().Add(-3 * time.Minute)
	failedAt := startedAt.Add(time.Minute)
	return domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderCoder,
		ProviderEnvironmentID: environmentID,
		ProviderConnectionID:  connectionID,
		DesiredState:          domain.SandboxDesiredRunning,
		ObservedState:         domain.SandboxObservedTerminated,
		StartupStartedAt:      &startedAt,
		StartupErrorCode:      sandbox.StartupErrorWorkspaceBuildFailed,
		StartupErrorAt:        &failedAt,
		ProviderFailureID:     failedBuildID,
		UpdatedAt:             failedAt,
	}
}

// AO stopped a managed workspace when it parked the session. Any wake that
// is not a retry (opening the session, a terminal, Chat) must leave it
// stopped; a retry, or a resume or restore that opens a new startup window,
// starts it.
func TestParkedManagedSessionIsNotRestartedByAWake(t *testing.T) {
	t.Parallel()
	stopped := sandbox.Environment{ID: "workspace-1", State: sandbox.StateStopped}

	store := &lifecycleStore{}
	provider := &buildFailureProvider{lifecycleProvider: lifecycleProvider{environment: stopped}}
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), parkedRecord("workspace-1", "")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.starts != 0 || strings.Join(store.observations, ",") != domain.SandboxObservedTerminated {
		t.Fatalf("woken parked session: starts = %d observations = %v, want still parked",
			provider.starts, store.observations)
	}

	// A user retry clears the error and re-arms the sandbox.
	retried := parkedRecord("workspace-1", "")
	retried.ObservedState = domain.SandboxObservedProvisioning
	retried.StartupErrorCode, retried.StartupErrorAt = "", nil
	now := time.Now()
	retried.StartupStartedAt = &now
	store = &lifecycleStore{}
	provider = &buildFailureProvider{lifecycleProvider: lifecycleProvider{environment: stopped}}
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), retried); err != nil {
		t.Fatalf("reconcile after retry: %v", err)
	}
	if provider.starts != 1 {
		t.Fatalf("retried session: starts = %d, want 1", provider.starts)
	}

	// A restore keeps the error and observed state but opens a new window.
	restored := parkedRecord("workspace-1", "")
	restored.StartupStartedAt = &now
	store = &lifecycleStore{}
	provider = &buildFailureProvider{lifecycleProvider: lifecycleProvider{environment: stopped}}
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), restored); err != nil {
		t.Fatalf("reconcile after restore: %v", err)
	}
	if provider.starts != 1 {
		t.Fatalf("restored session: starts = %d, want 1", provider.starts)
	}
}

// A session parked because its compute could not be created stays parked on
// a wake: it is not bounced back to "waiting for provider capacity", and no
// create is attempted until the user retries.
func TestParkedUncreatedSessionStaysParkedOnWake(t *testing.T) {
	t.Parallel()
	store := &lifecycleStore{}
	provider := &buildFailureProvider{createErr: sandbox.ErrAtCapacity}
	record := parkedRecord("", "")
	record.Provider = sandbox.ProviderFreestyle
	record.StartupErrorCode = sandbox.StartupErrorWorkspaceCreateFailed
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.creates != 0 || strings.Join(store.observations, ",") != domain.SandboxObservedTerminated {
		t.Fatalf("creates = %d observations = %v, want still parked", provider.creates, store.observations)
	}
}

// A customer's own Coder is told to look at the details, which carry Coder's
// (redacted) response; a network error is still not the message.
func TestCreateFailureOnCustomerCoderPointsAtDetails(t *testing.T) {
	t.Parallel()
	got := createFailedError(domain.Sandbox{Provider: sandbox.ProviderCoder, ProviderConnectionID: "connection-1"},
		errors.New("dial tcp 10.0.3.7:443: connect: connection refused"))
	if got.Code != sandbox.StartupErrorWorkspaceCreateFailed || strings.Contains(got.Message, "10.0.3.7") ||
		!strings.Contains(got.Message, "your Coder deployment") || got.Err == nil {
		t.Fatalf("startup error = %+v", got)
	}
}

// describingProvider explains failures, recording how it was asked.
type describingProvider struct {
	buildFailureProvider
	describes     int
	customerOwned []bool
}

func (p *describingProvider) DescribeFailure(_ context.Context, _ sandbox.ID, failure sandbox.Failure, customerOwned bool) *sandbox.StartupError {
	p.describes++
	p.customerOwned = append(p.customerOwned, customerOwned)
	message := "managed reason"
	if customerOwned {
		message = "customer reason"
	}
	return &sandbox.StartupError{Code: failure.Err.Code, Message: message}
}

// The provider explains a failure only when it is reported (it may read
// logs), told whether the compute is the customer's own.
func TestFailedBuildIsDescribedOnceWhenReported(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		connectionID string
		want         string
	}{{"", "managed reason"}, {"connection-1", "customer reason"}} {
		record := startingRecord(test.connectionID)
		store := &markingStore{record: &record}
		provider := &describingProvider{buildFailureProvider: buildFailureProvider{lifecycleProvider: lifecycleProvider{
			environment: failedBuildEnvironment(failedBuildID),
		}}}
		reconciler := testReconciler(store, provider)
		for range 3 {
			if err := reconciler.reconcileSandbox(context.Background(), record); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			record.ObservedState = domain.SandboxObservedTerminated
		}
		if provider.describes != 1 || len(store.startupErrors) != 1 || store.startupErrors[0].message != test.want {
			t.Fatalf("%q: describes = %d startup errors = %+v", test.connectionID, provider.describes, store.startupErrors)
		}
	}
}

// Parking suppresses Starts, never Stops: AO-managed compute of a parked
// session that something restarted is stopped again; a customer's own is left
// alone, and already-stopped compute gets no new stop.
func TestParkedSessionStopsRestartedManagedCompute(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		connectionID string
		state        string
		wantStops    int
	}{
		{name: "managed running", state: sandbox.StateRunning, wantStops: 1},
		{name: "managed starting", state: sandbox.StateProvisioning, wantStops: 1},
		{name: "managed stopped", state: sandbox.StateStopped, wantStops: 0},
		{name: "bring your own running", connectionID: "connection-1", state: sandbox.StateRunning, wantStops: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &lifecycleStore{}
			provider := &buildFailureProvider{lifecycleProvider: lifecycleProvider{
				environment: sandbox.Environment{ID: "workspace-1", State: test.state},
			}}
			if err := testReconciler(store, provider).reconcileSandbox(
				context.Background(), parkedRecord("workspace-1", test.connectionID),
			); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if provider.stops != test.wantStops || provider.starts != 0 ||
				strings.Join(store.observations, ",") != domain.SandboxObservedTerminated {
				t.Fatalf("stops = %d starts = %d observations = %v; want %d stops, still parked",
					provider.stops, provider.starts, store.observations, test.wantStops)
			}
		})
	}
}
