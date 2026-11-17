// Package chattraffic holds the data contract of the local chat-traffic
// benchmark: the run artifact a harness writes, the reviewed limits file, the
// comparison of a baseline run with a candidate run, and the Markdown report.
//
// The package imports only the standard library, so a run artifact from any
// revision can be validated and rendered without compiling product code, and
// every input is treated as data: nothing here derives a path, a command, or
// Markdown markup from an artifact field. The measurement harness that drives
// the daemon lives in backend/internal/httpd/chattraffic_harness_test.go.
package chattraffic

// Schema identifiers. A reader accepts exactly these values.
const (
	RunSchema     = "ao.chattraffic.run.v1"
	LimitsSchema  = "ao.chattraffic.limits.v1"
	ReportSchema  = "ao.chattraffic.report.v1"
	FixtureSchema = "ao.chattraffic.fixture.v1"
)

// MaxArtifactBytes bounds every JSON input before it is parsed.
const MaxArtifactBytes = 256 << 10

// MaxMetric bounds every metric and count. Real values are far smaller; the
// bound keeps integer arithmetic exact and rejects absurd artifacts.
const MaxMetric int64 = 1_000_000_000

// Allowlisted identifiers. Every identifier an artifact carries must be one of
// these, so a rendered report never contains artifact-chosen text.
var (
	Providers  = []string{"claude", "codex"}
	Phases     = []string{"open-chat", "scroll-to-top", "stream-reply", "expand-output"}
	Transports = []string{"local", "remote-lan"}
	Sides      = []string{"baseline", "candidate"}
)

// Run is one harness execution at one source revision.
type Run struct {
	SchemaVersion string `json:"schemaVersion"`
	// Side says which revision of the comparison this run measured.
	Side string `json:"side"`
	// SourceRef is the 40-hex commit whose product code the run compiled. For a
	// dirty candidate it is the HEAD the uncommitted changes sit on.
	SourceRef string `json:"sourceRef"`
	Dirty     bool   `json:"dirty"`
	// DirtyDiffSha256 is the sha256 of `git diff --binary HEAD` plus untracked
	// file digests when Dirty, and empty otherwise.
	DirtyDiffSha256 string `json:"dirtyDiffSha256"`
	GoVersion       string `json:"goVersion"`
	GOOS            string `json:"goos"`
	GOARCH          string `json:"goarch"`
	// HarnessSha256 digests the harness files (see HarnessFiles); both sides
	// must have run byte-identical harness code.
	HarnessSha256 string `json:"harnessSha256"`
	// FixturesSha256 digests the fixture files the harness replayed.
	FixturesSha256 string `json:"fixturesSha256"`
	// LimitsSha256 is the digest of the limits file the runner named. The
	// harness never reads limits; this binds the run to one reviewed file.
	LimitsSha256 string `json:"limitsSha256"`
	// ClientSourceSha256 digests the renderer files whose policy the Go client
	// simulator mirrors, read from this revision's tree.
	ClientSourceSha256 string `json:"clientSourceSha256"`
	// SimulatorClientSha256 is the reviewed client-source digest the simulator
	// matched this run's renderer to: the harness lists every renderer revision
	// its simulator was checked against. A run whose ClientSourceSha256 differs
	// measured a client the simulator does not model.
	SimulatorClientSha256 string       `json:"simulatorClientSha256"`
	ClientPolicy          ClientPolicy `json:"clientPolicy"`
	// Nonce is fresh per execution, so a cached test replay is detectable.
	Nonce     string     `json:"nonce"`
	Scenarios []Scenario `json:"scenarios"`
}

// ClientPolicy is the renderer paging policy read from this revision's
// frontend source and applied by the simulator.
type ClientPolicy struct {
	// LocalPageSize is a local chat's first, newest page; LocalOlderPageSize is
	// each older page read on scroll.
	LocalPageSize      int64 `json:"localPageSize"`
	LocalOlderPageSize int64 `json:"localOlderPageSize"`
	// LocalTailRefresh is set when an open local chat refreshes by reading only
	// the rows above its floor, rather than refetching every loaded page.
	LocalTailRefresh    bool  `json:"localTailRefresh"`
	RemoteLivePageSize  int64 `json:"remoteLivePageSize"`
	RemoteOlderPageSize int64 `json:"remoteOlderPageSize"`
	RemoteOutputLimit   int64 `json:"remoteOutputLimit"`
	InvalidateWindowMs  int64 `json:"invalidateWindowMs"`
}

// Scenario is one provider fixture replayed through one transport for one
// client phase.
type Scenario struct {
	Provider  string  `json:"provider"`
	Phase     string  `json:"phase"`
	Transport string  `json:"transport"`
	Metrics   Metrics `json:"metrics"`
	State     State   `json:"state"`
}

// Metrics are byte and message counts observed at the simulated client's TCP
// sockets. No TLS, TCP/IP, or tunnel framing is included.
type Metrics struct {
	// Requests counts conversation HTTP requests the client issued. The event
	// stream connection is not counted here.
	Requests int64 `json:"requests"`
	// CDCRows counts change_log rows the scenario's writes produced.
	CDCRows int64 `json:"cdcRows"`
	// SSEFrames counts event-stream frames the client received.
	SSEFrames int64 `json:"sseFrames"`
	// SSEBytes is the event-stream body after HTTP/1.1 de-chunking. The
	// daemon never compresses the stream, so this is also its decoded size.
	SSEBytes int64 `json:"sseBytes"`
	// HeaderBytes is every response status line and header block.
	HeaderBytes int64 `json:"headerBytes"`
	// ChunkBytes is HTTP/1.1 chunked framing on all responses.
	ChunkBytes int64 `json:"chunkBytes"`
	// BodyBytes is JSON response bodies as the client received them, after
	// de-chunking and before Content-Encoding is decoded.
	BodyBytes int64 `json:"bodyBytes"`
	// JSONBytes is the same bodies after Content-Encoding is decoded.
	JSONBytes int64 `json:"jsonBytes"`
	// DownloadBytes is every byte the client read from its sockets. It equals
	// HeaderBytes + ChunkBytes + BodyBytes + SSEBytes.
	DownloadBytes int64 `json:"downloadBytes"`
	// UploadBytes is every byte the client wrote: request lines and headers.
	UploadBytes int64 `json:"uploadBytes"`
}

// State is the semantic result a scenario must reproduce. Byte savings that
// lose content cannot pass: each digest covers ordered rows, final texts,
// statuses, and activity output sizes.
type State struct {
	// ExpectedDigest is committed with the fixtures (testdata/expected).
	ExpectedDigest string `json:"expectedDigest"`
	// ClientDigest is the simulated client's reconstructed view.
	ClientDigest string `json:"clientDigest"`
	// ServerDigest is a fresh unpaged server read at the end of the scenario.
	ServerDigest string `json:"serverDigest"`
	Messages     int64  `json:"messages"`
	Activities   int64  `json:"activities"`
	Turns        int64  `json:"turns"`
}

// Limits is the reviewed limits file. It is never written by a run and never
// derived from a baseline.
type Limits struct {
	SchemaVersion string `json:"schemaVersion"`
	// FixturesSha256 names the fixture set these limits were reviewed for.
	FixturesSha256 string `json:"fixturesSha256"`
	// MaxIncreasePermille is the largest allowed candidate increase over the
	// baseline for every metric, in thousandths, using integer arithmetic.
	MaxIncreasePermille int64      `json:"maxIncreasePermille"`
	Rows                []LimitRow `json:"rows"`
}

// LimitRow holds the absolute limit for every metric of one scenario.
type LimitRow struct {
	Provider  string  `json:"provider"`
	Phase     string  `json:"phase"`
	Transport string  `json:"transport"`
	Max       Metrics `json:"max"`
}

// ScenarioKey identifies a scenario across runs and limits.
type ScenarioKey struct{ Provider, Phase, Transport string }

// Key identifies the scenario a run measured.
func (s Scenario) Key() ScenarioKey { return ScenarioKey{s.Provider, s.Phase, s.Transport} }

// Key identifies the scenario a limit row covers.
func (c LimitRow) Key() ScenarioKey { return ScenarioKey{c.Provider, c.Phase, c.Transport} }

func (k ScenarioKey) String() string { return k.Provider + "/" + k.Phase + "/" + k.Transport }

// MetricNames lists metrics in report order.
var MetricNames = []string{
	"requests", "cdcRows", "sseFrames", "sseBytes", "headerBytes", "chunkBytes",
	"bodyBytes", "jsonBytes", "downloadBytes", "uploadBytes",
}

// Values returns metrics in MetricNames order.
func (m Metrics) Values() []int64 {
	return []int64{
		m.Requests, m.CDCRows, m.SSEFrames, m.SSEBytes, m.HeaderBytes, m.ChunkBytes,
		m.BodyBytes, m.JSONBytes, m.DownloadBytes, m.UploadBytes,
	}
}
