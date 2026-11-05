package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/collaboration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// m2Services is the M2 wiring bundle handed to httpd.
//
// Every field is nil-able and every coordinator is built over the durable store plus the
// PRODUCTION execution gateway. Two things are load-bearing here:
//
//   - The gateway is execution.LocalGateway over the real Session Manager and Chat
//     service. It composes owners that already existed before M2 and starts no provider
//     process of its own: a Spawn asks the Session Manager, a Send asks the Chat service.
//     ports.ExecutionGateway carries no unexported seal, so this is a behavioural
//     guarantee rather than a compile-time one — which is why no other implementation is
//     constructed anywhere under internal/daemon.
//   - A capability probe is registered per harness. Without one, every harness resolves
//     to its static profile, whose grade cannot authorize a control. With one, the
//     resolver has runtime evidence: install/auth facts from the readiness provider and
//     the driver's own handshake.
type m2Services struct {
	Capabilities *capability.Resolver
	Bindings     *sqlite.Store
	Binder       *execution.Coordinator
	Gateway      ports.ExecutionGateway
	// Ownership proves that a bind request's workflow, task, and attempt describe the
	// same work. It is the store's own read surface, so an HTTP controller can ask it
	// without importing the storage layer.
	Ownership     controllers.OwnershipResolver
	Interventions *intervention.Coordinator
	Collaboration *collaboration.Coordinator
	// CapabilityRecorder persists observed evidence so a restart answers from what was
	// actually seen. The daemon exposes it for the chat service's per-turn recording.
	CapabilityRecorder ports.CapabilityRecorder
	// probeCount is how many harnesses have a registered capability probe. It is
	// recorded as a number rather than as the harness names so the startup log stays a
	// shape, not an inventory.
	probeCount int
}

// m2SessionOwners is the Session Manager surface the gateway composes.
//
// It is an interface rather than *sessionmanager.Manager so a test can prove the wiring
// forwards to the owners it was handed. The production daemon passes the concrete manager,
// which satisfies it; nothing else in this file knows which implementation it holds.
type m2SessionOwners interface {
	execution.SessionSpawner
	execution.SessionResumer
}

// m2ChatOwners is the Chat service surface the gateway and the capability probe compose.
type m2ChatOwners interface {
	execution.ChatSender
	execution.ChatControllers
	// SteerPinned is the pinned steer entry point. It is listed here so a chat
	// implementation that drops it fails to compile at the wiring site instead of
	// silently degrading every steer to "unsupported".
	SteerPinned(
		ctx context.Context,
		id domain.SessionID,
		expectedGeneration, providerTurnID string,
		msg ports.ChatUserMessage,
	) (chatsvc.SteerResult, error)
}

// m2GatewayDeps are the owners the execution gateway composes. They are passed in rather
// than built here because they come from different moments in the boot sequence: the
// store exists first, the chat service next, and the Session Manager last.
type m2GatewayDeps struct {
	Store *sqlite.Store
	// Sessions and Chats are the real owners. A nil value makes the gateway refuse the
	// operations that need them rather than panic.
	Sessions m2SessionOwners
	Chats    m2ChatOwners
	// Gateway, when set, replaces the constructed LocalGateway. It exists ONLY so a test
	// can prove the coordinator and intervention coordinators were handed the gateway
	// this bundle built, using a gateway whose call counters it can read. Production code
	// never sets it; TestM2WiringConstructsTheRealGateway asserts the default.
	Gateway ports.ExecutionGateway
	// ChatDrivers and Readiness are the two facts the capability probe reads. A nil
	// value makes the probe report unknown rather than guess.
	ChatDrivers ports.ChatDriverRegistry
	Readiness   ports.AgentReadinessProvider
	// Recorder is the durable capability evidence store. The daemon builds it before the
	// chat service (which records confirmed runtime evidence per turn), so it is passed
	// in rather than constructed here; nil builds a fresh one over the store.
	Recorder *capability.Recorder
	Log      *slog.Logger
}

// newM2Services builds the M2 coordinators over the durable store.
//
// The gateway and the capability probes are wired from real owners. Nothing here starts
// a process: the gateway forwards to the Session Manager and the Chat service, and a
// probe runs only when a capability question is asked.
func newM2Services(store *sqlite.Store, log *slog.Logger) m2Services {
	return newM2ServicesWith(m2GatewayDeps{Store: store, Log: log})
}

// newM2ServicesWith builds the same bundle with the real gateway owners injected.
//
// It is one function with two call sites on purpose: the daemon calls it with the live
// Session Manager and Chat service, and the wiring test calls it with none. A test that
// built a different bundle would be testing a wiring nobody runs.
func newM2ServicesWith(deps m2GatewayDeps) m2Services {
	store := deps.Store
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}

	// Durable capability evidence: without a recorder, a restart re-probes blindly and an
	// expired snapshot loses the reason it expired. The recorder stores each observation
	// with the grade it actually had, so no gate downstream sees a raised grade.
	recorder := deps.Recorder
	if recorder == nil {
		recorder = capability.NewRecorder(store)
	}
	resolver := capability.NewResolver(
		capability.WithRecorder(recorder),
		capability.WithClock(func() time.Time { return time.Now().UTC() }),
	)
	resolver = registerCapabilityProbes(resolver, deps, log)

	gateway := deps.Gateway
	if gateway == nil {
		gateway = execution.NewLocalGateway(execution.LocalGatewayDeps{
			Sessions:  deps.Sessions,
			Resumer:   deps.Sessions,
			Records:   store,
			Commits:   store,
			Worktrees: store,
			// Turns lets a steer receipt carry both the provider turn and AO's own turn
			// row, which is what an "applied" intervention receipt requires. Without it
			// every steer would report the provider id only and the applied decision
			// would be unreachable.
			Turns:        store,
			Chats:        deps.Chats,
			Controllers:  deps.Chats,
			Clock:        func() time.Time { return time.Now().UTC() },
			Log:          log,
			Capabilities: recorder,
		})
	}

	binder := execution.New(execution.Deps{
		Store:    store,
		Gateway:  gateway,
		Resolver: resolver,
	})
	return m2Services{
		Capabilities:       resolver,
		Bindings:           store,
		Binder:             binder,
		Gateway:            gateway,
		Ownership:          ownershipAdapter{store: store},
		CapabilityRecorder: recorder,
		Interventions: intervention.New(intervention.Deps{
			Store:    store,
			Bindings: store,
			// The writer is the same coordinator, so a confirmed stop advances the
			// binding instead of leaving an intervention receipt nothing acts on.
			BindingWriter: binder,
			Gateway:       gateway,
			Resolver:      resolver,
		}),
		Collaboration: collaboration.New(collaboration.Deps{
			Store:    store,
			Bindings: store,
			Graph:    store,
		}),
		probeCount: len(domain.AllHarnesses),
	}
}

// registerCapabilityProbes installs one real probe per harness.
//
// One probe object serves every harness: it reads the harness from the request, so
// registering it per harness costs nothing and keeps zero provider knowledge in the
// daemon. A probe that carried a harness-specific table would be the hardcoding the
// capability boundary forbids.
func registerCapabilityProbes(
	resolver *capability.Resolver,
	deps m2GatewayDeps,
	log *slog.Logger,
) *capability.Resolver {
	probe := capability.NewChatDriverProbe(
		chatControllerProbe{svc: deps.Chats},
		deps.ChatDrivers,
		deps.Readiness,
		func() time.Time { return time.Now().UTC() },
		capability.DefaultTTL,
	)
	for _, harness := range domain.AllHarnesses {
		resolver.RegisterProbe(harness, probe)
	}
	return resolver
}

// logM2Wiring records the M2 wiring shape once at startup so an operator can see which
// execution path is live.
//
// It prints only enum values and booleans: no session id, no attempt or workflow id, no
// spawn hash, no prompt, no message body, and no path. A capability probe count is a
// number, so the line stays a shape rather than an inventory.
func logM2Wiring(log *slog.Logger, services m2Services) {
	if log == nil {
		return
	}
	log.Info("workflow execution wiring",
		"gateway", "local",
		"capabilityProbes", services.probeCount,
		"bind", services.Binder != nil,
		"interventions", services.Interventions != nil,
		"collaboration", services.Collaboration != nil,
		"maxOpenMessages", domain.MaxOpenWorkflowMessages)
}

// ownershipAdapter adapts execution.ResolveAttemptOwnership to the HTTP controller's
// OwnershipResolver, which takes no reader argument because it must not be able to choose
// its own evidence source.
//
// The translation is field-by-field rather than a type conversion: the two structs live in
// packages that do not import each other, so a field rename becomes a compile error here
// instead of a silently empty value at the HTTP boundary.
type ownershipAdapter struct {
	store *sqlite.Store
}

var _ controllers.OwnershipResolver = ownershipAdapter{}

func (a ownershipAdapter) ResolveAttemptOwnership(
	ctx context.Context,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
) (controllers.AttemptOwnership, error) {
	ownership, err := execution.ResolveAttemptOwnership(ctx, a.store, workflowID, attemptID)
	if err != nil {
		return controllers.AttemptOwnership{}, err
	}
	return controllers.AttemptOwnership{
		Attempt:   ownership.Attempt,
		Task:      ownership.Task,
		Repos:     ownership.Repos,
		ProjectID: ownership.ProjectID,
		Harness:   ownership.Harness,
	}, nil
}

// chatControllerProbe adapts the Chat service to capability.SessionProber.
type chatControllerProbe struct {
	svc execution.ChatControllers
}

// SessionGeneration reports the controller generation and whether one is live.
func (p chatControllerProbe) SessionGeneration(sessionID domain.SessionID) (string, bool) {
	if p.svc == nil {
		return "", false
	}
	controller, err := p.svc.Controller(sessionID)
	if err != nil || controller == nil {
		return "", false
	}
	return controller.Generation(), p.svc.HasLiveChatController(sessionID)
}

// ChatCapabilities reports what the live controller actually negotiated.
func (p chatControllerProbe) ChatCapabilities(
	sessionID domain.SessionID,
) (ports.ChatCapabilities, bool) {
	if p.svc == nil {
		return nil, false
	}
	controller, err := p.svc.Controller(sessionID)
	if err != nil || controller == nil {
		return nil, false
	}
	return controller.Capabilities(), true
}
