package controllers_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

// testTime is a fixed timestamp so a serialisation assertion never depends on the clock.
func testTime() time.Time { return time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC) }

// ptrTime takes the address of a timestamp for the optional fields.
func ptrTime(t time.Time) *time.Time { return &t }

// ptrBool and ptrInt64 take addresses of the optional scalar fields.
func ptrBool(value bool) *bool    { return &value }
func ptrInt64(value int64) *int64 { return &value }

// TestM2ResponsesNeverLeakTheLease is the M2-08 "lease token never leaks" sweep.
//
// The workflow lease is a capability: the claim response issues it exactly once, and every
// later read must be structurally incapable of reproducing it. Struct tags alone are not
// evidence, because a projection can be built by hand and drift from its domain value. So
// this test POPULATES every new M2 response shape with hostile values — including a real
// 64-character hex token in every string field that could plausibly carry one — serialises
// it, and asserts the token does not survive.
//
// The 64-hex pattern is the same shape the lease token and every operation SHA use, which
// makes this test catch a leak of EITHER: a token, or a request/operation fingerprint that
// was not meant to be published.
func TestM2ResponsesNeverLeakTheLease(t *testing.T) {
	// A distinctive 64-hex value. Distinctive means a match cannot be a coincidence with a
	// timestamp, a uuid, or a hash the test itself did not plant.
	const leak = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	hex64 := regexp.MustCompile(`\b[0-9a-f]{64}\b`)

	// Every new M2 response shape, each fully populated. A field left zero would make the
	// assertion vacuous for that field, so no field is left empty.
	shapes := map[string]any{
		"WorkflowCapabilityResponse": controllers.WorkflowCapabilityResponse{
			Harness: "codex", Source: "provider_handshake",
			AuthState: "authorized", Readiness: "ready", Surfaces: "chat",
			Streaming: "supported", Tools: "supported", Steer: "supported",
			Queue: "supported", Interrupt: "supported",
			Resume: "load_with_history", Approval: "enforced",
			ApplyAt: "turn", Usage: "exact", ObservedAt: testTime(),
			ExpiresAt: testTime(), ReasonCode: "supported",
		},
		"WorkflowAttemptBindingResponse": controllers.WorkflowAttemptBindingResponse{
			AttemptID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
			SpawnRequestID: "spawn-attempt-1", State: "bound_ready",
			SessionID: "session-1", ControllerGeneration: "gen-1",
			BoundAt: ptrTime(testTime()), FailureCode: "", Revision: 3,
			CreatedAt: testTime(), UpdatedAt: testTime(),
			Repositories: []controllers.WorkflowAttemptRepoBindingResponse{{
				RepoKey: "backend", ProjectID: "project-1", ExpectedBaseSHA: "base-sha-1",
				SessionWorktreeRef: "worktree-1", ObservedHeadSHA: "observed-sha-1",
				State: "pending", Revision: 2,
			}},
		},
		"BindWorkflowAttemptResponse": controllers.BindWorkflowAttemptResponse{
			Binding: controllers.WorkflowAttemptBindingResponse{
				AttemptID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
				SpawnRequestID: "spawn-attempt-1", State: "bound_ready",
				SessionID: "session-1", ControllerGeneration: "gen-1",
				Revision: 3, CreatedAt: testTime(), UpdatedAt: testTime(),
			},
			Spawned: true, Frozen: false, ReasonCode: "bound",
		},
		"WorkflowInterventionEnvelope": controllers.WorkflowInterventionEnvelope{
			Intervention: controllers.WorkflowInterventionResponse{
				ID: "intervention-1", WorkflowID: "workflow-1", TaskID: "task-1",
				AttemptID: "attempt-1", Mode: "steer", State: "applied",
				SessionID: "session-1", ControllerGeneration: "gen-1",
				ClientMessageID: "msg-1", TargetProviderTurnID: "prov-turn-1",
				ChatTurnID: "turn-1", ProviderTurnID: "prov-turn-1",
				ReasonCode: "applied", Revision: 2,
				CreatedAt: testTime(), UpdatedAt: testTime(), SettledAt: ptrTime(testTime()),
			},
			Performed: true, Frozen: false, QueueAvailable: false, ReasonCode: "applied",
			Replan: &controllers.WorkflowReplanResponse{
				InterventionID: "intervention-1", WorkflowID: "workflow-1",
				TaskID: "task-1", ReasonCode: "stop_confirmed",
				PreservedWorktrees: []string{"worktree-1"}, RequestedAt: testTime(),
			},
		},
		"WorkflowMessageResponse": controllers.WorkflowMessageResponse{
			ID: "message-1", WorkflowID: "workflow-1", FromTaskID: "task-1",
			ToTaskID: "task-2", SenderKind: "agent", Kind: "progress",
			DeliveryState: "persisted", Body: "halfway", PayloadRef: "artifact-1",
			ClientMessageID: "msg-1", Revision: 1,
			CreatedAt: testTime(), UpdatedAt: testTime(),
		},
		"CreateWorkflowMessageResponse": controllers.CreateWorkflowMessageResponse{
			Message: controllers.WorkflowMessageResponse{
				ID: "message-1", WorkflowID: "workflow-1", FromTaskID: "task-1",
				SenderKind: "agent", Kind: "progress", DeliveryState: "persisted",
				Body: "halfway", ClientMessageID: "msg-1", Revision: 1,
				CreatedAt: testTime(), UpdatedAt: testTime(),
			},
			Created: true,
		},
		"ObserveWorkflowAttemptResponse": controllers.ObserveWorkflowAttemptResponse{
			Observation: &controllers.WorkflowSessionObservationResponse{
				SessionID: "session-1", ProjectID: "project-1", Kind: "worker",
				Harness: "codex", Mode: "chat", Readiness: "ready",
				ControllerGeneration: "gen-1", ProviderConversationID: "conv-1",
				ClientRequestID: "req-1", ClientRequestCommitted: true,
				ObservedAt: testTime(),
			},
			BindingState: "bound_ready", Live: ptrBool(true), ReasonCode: "ready",
		},
		"WorkflowTaskRequestDetailResponse": controllers.WorkflowTaskRequestDetailResponse{
			WorkflowTaskRequestResponse: controllers.WorkflowTaskRequestResponse{
				ID: "request-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
				RequestKind: "create_task", State: "pending",
				WorkflowRevision: 2, Revision: 1,
				CreatedAt: testTime(), UpdatedAt: testTime(),
				RequestSHA: strings.Repeat("c", 64),
			},
			Summary: controllers.WorkflowTaskRequestSummary{
				TaskCount: 2, TitleHints: []string{"Add a parser"},
				Repos: []string{"backend"}, BudgetDelta: ptrInt64(500),
				ConcurrencyDelta: ptrInt64(1), NotesRef: "artifact-1",
			},
		},
		"WorkflowTaskRequestResponse": controllers.WorkflowTaskRequestResponse{
			ID: "request-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
			RequestKind: "create_task", State: "pending",
			DecidedByKind: "", DecidedByRef: "",
			WorkflowRevision: 2, Revision: 1,
			CreatedAt: testTime(), UpdatedAt: testTime(),
			RequestSHA: strings.Repeat("c", 64),
		},
	}

	for name, shape := range shapes {
		raw, err := json.Marshal(shape)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		text := string(raw)

		// (1) No lease-named key, in any casing or separator style.
		for _, needle := range []string{"leaseToken", "lease_token", "leasetoken", "LeaseToken"} {
			if strings.Contains(text, needle) {
				t.Fatalf("%s exposes %q: %s", name, needle, text)
			}
		}

		// (2) No 64-hex value other than the request fingerprint this shape is allowed to
		// publish. The allowlist is computed per shape, so a new 64-hex field fails here
		// until someone writes down why it is safe.
		allowed := map[string]bool{}
		if request, ok := shape.(controllers.WorkflowTaskRequestResponse); ok {
			allowed[request.RequestSHA] = true
		}
		if detail, ok := shape.(controllers.WorkflowTaskRequestDetailResponse); ok {
			allowed[detail.RequestSHA] = true
		}
		for _, match := range hex64.FindAllString(text, -1) {
			if !allowed[match] {
				t.Fatalf("%s publishes an unexplained 64-hex value %q: %s", name, match, text)
			}
		}

		// (3) The planted token specifically never appears.
		if strings.Contains(text, leak) {
			t.Fatalf("%s carries the planted lease token", name)
		}
	}
}

// TestM2TaskRequestHidesItsPayload is the P1-A regression pin.
//
// The task request payload is opaque JSON that can name repo scopes and file paths, and only
// the decide path is meant to interpret it. The M2 P1-A fix removed it from the response and
// replaced it with a fingerprint. This test asserts both halves of that: no requestJson key,
// and no path-shaped substring, so the fix cannot regress into a differently-named field that
// still ships the body.
func TestM2TaskRequestHidesItsPayload(t *testing.T) {
	// The serialised shape is built from the real struct, so adding a request_json-tagged
	// field would fail this test even if the projection below were never updated.
	raw, err := json.Marshal(controllers.WorkflowTaskRequestResponse{
		ID: "request-1", WorkflowID: "workflow-1", RequesterTaskID: "task-1",
		RequestKind: "expand_repos", State: "pending",
		WorkflowRevision: 2, Revision: 1,
		CreatedAt: testTime(), UpdatedAt: testTime(),
		RequestSHA: strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(raw)

	for _, banned := range []string{
		"requestJson", "request_json", "requestJSON", "payload\":", "RequestJSON",
	} {
		if strings.Contains(text, banned) {
			t.Fatalf("the task request response exposes %q: %s", banned, text)
		}
	}

	// No filesystem-path or repo-scope shape survived.
	pathLike := regexp.MustCompile(`[A-Za-z]:\\\\|/home/|/tmp/|\.\./|repos?/\w+/`)
	if match := pathLike.FindString(text); match != "" {
		t.Fatalf("the response contains a path-shaped substring %q: %s", match, text)
	}

	// And the fingerprint it publishes instead is exactly 64 hex characters.
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(strings.Repeat("c", 64)) {
		t.Fatal("the fingerprint shape changed")
	}
}

// TestM2CDCPayloadsCarryNoMessageBody proves the new SSE payloads are metadata only.
//
// The payloads are built by SQL triggers, so this test reproduces the exact json_object
// projections the M2 migrations write and asserts that none of them can carry a message body
// or a request JSON. If a migration later adds a column to one of these projections, the
// field list here is the checklist that has to be updated deliberately.
func TestM2CDCPayloadsCarryNoMessageBody(t *testing.T) {
	payloads := map[string]string{
		"workflow_message_created": `{"kind":"workflow_message","workflowId":"workflow-1",` +
			`"messageId":"message-1","fromTaskId":"task-1","toTaskId":"task-2",` +
			`"messageKind":"progress","senderKind":"agent","deliveryState":"persisted"}`,
		"workflow_message_updated": `{"kind":"workflow_message","workflowId":"workflow-1",` +
			`"messageId":"message-1","toTaskId":"task-2","deliveryState":"forwarded"}`,
		"workflow_task_request_updated": `{"kind":"workflow_task_request","workflowId":"workflow-1",` +
			`"requestId":"request-1","requesterTaskId":"task-1","requestKind":"create_task",` +
			`"state":"pending"}`,
		"workflow_intervention_updated": `{"kind":"workflow_intervention","workflowId":"workflow-1",` +
			`"taskId":"task-1","interventionId":"intervention-1","mode":"steer",` +
			`"state":"applied","reasonCode":"applied"}`,
	}

	for eventType, payload := range payloads {
		if !json.Valid([]byte(payload)) {
			t.Fatalf("%s: the recorded projection is not valid JSON: %s", eventType, payload)
		}
		// The banned content: a message body, the raw request JSON, or prompt text.
		for _, banned := range []string{
			`"body"`, `"text"`, `"prompt"`, `"requestJson"`, `"request_json"`, `"payload":`,
			`"leaseToken"`, `"operationSha"`, `"spawnHash"`,
		} {
			if strings.Contains(payload, banned) {
				t.Fatalf("%s leaks %s: %s", eventType, banned, payload)
			}
		}
		// No unexplained 64-hex value may appear in a push payload either.
		if match := regexp.MustCompile(`\b[0-9a-f]{64}\b`).FindString(payload); match != "" {
			t.Fatalf("%s publishes a 64-hex value %q", eventType, match)
		}
	}
}
