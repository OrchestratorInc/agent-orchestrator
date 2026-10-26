package zcodeproto

// Method names the driver sends or reads, in both directions. Each has a
// provenance tag in Methods; see doc.go for what PROVEN and STATIC mean.
const (
	// Client -> server requests.
	MethodSessionCreate    = "session/create"
	MethodSessionSend      = "session/send"
	MethodSessionSubscribe = "session/subscribe"
	MethodSessionStop      = "session/stop"
	MethodSessionResume    = "session/resume"
	MethodSessionRead      = "session/read"
	MethodSessionSetModel  = "session/setModel"
	MethodSessionSetMode   = "session/setMode"
	MethodSessionUsage     = "session/usage"

	// Server -> client requests (ids `server-<n>`), which the client must answer.
	MethodSessionRequestRuntimePreferences           = "session/requestRuntimePreferences"
	MethodInteractionRequestPermission               = "interaction/requestPermission"
	MethodInteractionRequestUserInput                = "interaction/requestUserInput"
	MethodInteractionRequestProviderRuntimeHeaders   = "interaction/requestProviderRuntimeHeaders"
	MethodInteractionRequestOfficialMcpAuthHeaders   = "interaction/requestOfficialMcpAuthHeaders"
	MethodSubagentsReadRuntimeConfig                 = "subagents/readRuntimeConfig"
	MethodInteractionProviderRuntimeHeadersCancelled = "interaction/providerRuntimeHeadersCancelled"

	// Server -> client notifications.
	MethodStateUpdated              = "state.updated"
	MethodComputerUseOperationEvent = "computer-use/operation-event"
	MethodSessionEvent              = "session/event"
	MethodStartupStorageState       = "startup/storageState"
	MethodProcessMcpTelemetry       = "process/mcpTelemetry"
	MethodV4TelemetryEvent          = "v4/telemetry/event"
	MethodProcessResourceSample     = "process/resourceSample"
	MethodProcessMcpResourceSamples = "process/mcpResourceSamples"
	MethodProcessToolExecResource   = "process/toolExecResource"
	MethodProcessNetworkRequests    = "process/networkRequests"
	MethodPluginsOperationProgress  = "plugins/operationProgress"
)

// Provenance says how a method's shape was established.
const (
	ProvenanceProven = "PROVEN"
	ProvenanceStatic = "STATIC"
)

// Directions mirror codexproto's vocabulary so the two conformance tables read
// alike.
const (
	DirectionClientRequest      = "ClientRequest"
	DirectionServerRequest      = "ServerRequest"
	DirectionServerNotification = "ServerNotification"
)

// MethodInfo describes one method's direction, payload types and provenance.
type MethodInfo struct {
	Name      string
	Direction string
	// Params and Result name the Go types in this package, empty when none.
	Params string
	Result string
	// Provenance is ProvenanceProven or ProvenanceStatic (doc.go).
	Provenance string
	// Symbol is the minified bundle symbol the shape was read from.
	Symbol string
	// Evidence names the testdata/*.ndjson fixture (derived from the redacted
	// Phase 1 capture) in which a PROVEN method appears. Empty for STATIC rows.
	Evidence string
}

// Methods is every method the driver depends on.
var Methods = []MethodInfo{
	// Outbound requests.
	{Name: MethodSessionCreate, Direction: DirectionClientRequest, Params: "SessionCreateParams", Result: "Snapshot", Provenance: ProvenanceProven, Symbol: "qXt", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodSessionSend, Direction: DirectionClientRequest, Params: "SessionSendParams", Result: "SessionSendResult", Provenance: ProvenanceProven, Symbol: "QXt/Lhc", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodSessionSubscribe, Direction: DirectionClientRequest, Params: "SessionSubscribeParams", Result: "SessionSubscribeResult", Provenance: ProvenanceProven, Symbol: "$Xt/_ci", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodSessionStop, Direction: DirectionClientRequest, Params: "SessionStopParams", Result: "", Provenance: ProvenanceProven, Symbol: "rQt/wci", Evidence: "p4-stop.ndjson"},
	{Name: MethodSessionResume, Direction: DirectionClientRequest, Params: "SessionResumeParams", Result: "Snapshot", Provenance: ProvenanceProven, Symbol: "WXt/pci", Evidence: "p8-resume.ndjson"},
	{Name: MethodSessionRead, Direction: DirectionClientRequest, Params: "SessionReadParams", Result: "Snapshot", Provenance: ProvenanceProven, Symbol: "mci", Evidence: "p9-usage-read-mcp.ndjson"},
	{Name: MethodSessionSetModel, Direction: DirectionClientRequest, Params: "SessionSetModelParams", Result: "Snapshot", Provenance: ProvenanceProven, Symbol: "iQt/xci", Evidence: "p7-models.ndjson"},
	{Name: MethodSessionSetMode, Direction: DirectionClientRequest, Params: "SessionSetModeParams", Result: "Snapshot", Provenance: ProvenanceStatic, Symbol: "session/setMode"},
	{Name: MethodSessionUsage, Direction: DirectionClientRequest, Params: "SessionUsageParams", Result: "SessionUsageResult", Provenance: ProvenanceProven, Symbol: "GYi", Evidence: "p9-usage-read-mcp.ndjson"},

	// Server -> client requests.
	{Name: MethodSessionRequestRuntimePreferences, Direction: DirectionServerRequest, Params: "RequestRuntimePreferencesParams", Result: "RequestRuntimePreferencesResult", Provenance: ProvenanceProven, Symbol: "KYi/XXt", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodInteractionRequestPermission, Direction: DirectionServerRequest, Params: "RequestPermissionParams", Result: "PermissionResponse", Provenance: ProvenanceProven, Symbol: "fXi/_F", Evidence: "p5-approval-allow.ndjson"},
	{Name: MethodInteractionRequestUserInput, Direction: DirectionServerRequest, Params: "RequestUserInputParams", Result: "UserInputResponse", Provenance: ProvenanceProven, Symbol: "mXi/oDe", Evidence: "p6-user-input.ndjson"},
	{Name: MethodInteractionRequestProviderRuntimeHeaders, Direction: DirectionServerRequest, Params: "RequestProviderRuntimeHeadersParams", Result: "RequestProviderRuntimeHeadersResult", Provenance: ProvenanceStatic, Symbol: "hXi"},
	{Name: MethodInteractionRequestOfficialMcpAuthHeaders, Direction: DirectionServerRequest, Params: "RequestOfficialMcpAuthHeadersParams", Result: "", Provenance: ProvenanceProven, Symbol: "_Xi/Koi", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodSubagentsReadRuntimeConfig, Direction: DirectionServerRequest, Params: "", Result: "SubagentRuntimeConfigResult", Provenance: ProvenanceStatic, Symbol: "Net"},

	// Server -> client notifications the timeline is built from.
	{Name: MethodStateUpdated, Direction: DirectionServerNotification, Params: "StateUpdatedParams", Result: "", Provenance: ProvenanceProven, Symbol: "Lhc/zFn/Xmc", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodComputerUseOperationEvent, Direction: DirectionServerNotification, Params: "OperationEventParams", Result: "", Provenance: ProvenanceProven, Symbol: "Oai/dhc", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodSessionEvent, Direction: DirectionServerNotification, Params: "SessionEventParams", Result: "", Provenance: ProvenanceProven, Symbol: "zXt/dhc", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodInteractionProviderRuntimeHeadersCancelled, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceStatic, Symbol: "sT"},

	// Noise the driver drops. The PROVEN rows were observed on the wire and
	// their payloads are not read; process/resourceSample was never observed
	// in any capture and stays STATIC (the Phase 0 PROVEN tag came from the
	// probe's filter regex, not from a frame).
	{Name: MethodStartupStorageState, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceProven, Symbol: "qNn", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodProcessMcpTelemetry, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceProven, Symbol: "sT", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodV4TelemetryEvent, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceProven, Symbol: "Sg", Evidence: "p1-create-send-complete.ndjson"},
	{Name: MethodProcessResourceSample, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceStatic, Symbol: "sT"},
	{Name: MethodProcessMcpResourceSamples, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceStatic, Symbol: "sT"},
	{Name: MethodProcessToolExecResource, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceStatic, Symbol: "sT"},
	{Name: MethodProcessNetworkRequests, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceStatic, Symbol: "sT"},
	{Name: MethodPluginsOperationProgress, Direction: DirectionServerNotification, Params: "", Result: "", Provenance: ProvenanceStatic, Symbol: "sT"},
}

// Declares reports whether the method is in the catalog. Mirrors
// codexproto.Declares so the conformance tests read alike.
func Declares(method string) bool {
	for _, m := range Methods {
		if m.Name == method {
			return true
		}
	}
	return false
}

// Info returns the catalog row for a method.
func Info(method string) (MethodInfo, bool) {
	for _, m := range Methods {
		if m.Name == method {
			return m, true
		}
	}
	return MethodInfo{}, false
}
