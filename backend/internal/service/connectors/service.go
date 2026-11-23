package connectors

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const (
	maxNameBytes   = 64
	maxURLBytes    = 2048
	maxValueBytes  = 8192
	maxArgs        = 64
	maxSecretPairs = 32
	maxErrorBytes  = 500
)

var (
	keyPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	envPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerPattern = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_` + "`" + `|~-]+$`)
	slugRun       = regexp.MustCompile(`[^a-z0-9]+`)
	reservedKeys  = map[string]bool{"ao": true, "import": true, "proposals": true}
)

// Service owns the connector list and its secret values.
type Service struct {
	mu      sync.Mutex
	path    string
	baseURL atomic.Value
	http    *http.Client
	now     func() time.Time
	loaded  bool
	records []record
	signIns map[string]*signIn
	sources map[string]oauth2.TokenSource

	sessions       map[string]*sessionUse
	sessionLookup  func(ctx context.Context, sessionID string) (projectID string, open bool)
	importProjects func(ctx context.Context) []ImportProject
	proposals      map[string]*Proposal
	tokenKey       []byte
}

// New returns a Service that keeps its file in dataDir. baseURL is the loopback address of the daemon.
func New(dataDir, baseURL string) *Service {
	s := &Service{
		path: filepath.Join(dataDir, fileName),
		http: &http.Client{Timeout: 30 * time.Second}, now: time.Now,
		signIns: map[string]*signIn{}, sources: map[string]oauth2.TokenSource{}, sessions: map[string]*sessionUse{},
	}
	s.SetBaseURL(baseURL)
	return s
}

// SetBaseURL sets the loopback address the daemon answers on. The daemon calls
// it with the bound address, which differs from the configured one when that
// port was taken.
func (s *Service) SetBaseURL(baseURL string) {
	s.baseURL.Store(strings.TrimRight(baseURL, "/"))
}

func (s *Service) base() string {
	base, _ := s.baseURL.Load().(string)
	return base
}

// List returns every connector in name order.
func (s *Service) List(_ context.Context) ([]Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	out := make([]Connector, 0, len(s.records))
	for i := range s.records {
		out = append(out, s.records[i].public())
	}
	slices.SortFunc(out, func(a, b Connector) int { return strings.Compare(a.Name+a.Key, b.Name+b.Key) })
	return out, nil
}

// Get returns one connector by ID or key.
func (s *Service) Get(_ context.Context, ref string) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Connector{}, err
	}
	i := s.index(ref)
	if i < 0 {
		return Connector{}, errNotFound()
	}
	return s.records[i].public(), nil
}

// Create validates and saves a new connector.
func (s *Service) Create(_ context.Context, in Input) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Connector{}, err
	}
	rec, err := normalize(in, record{})
	if err != nil {
		return Connector{}, err
	}
	if s.keyTaken(rec.Key, "") {
		return Connector{}, errKeyExists(rec.Key)
	}
	now := s.now().UTC()
	rec.ID = uuid.NewString()
	rec.Enabled = in.Enabled == nil || *in.Enabled
	rec.Status = StatusUnchecked
	rec.CreatedAt, rec.UpdatedAt = now, now
	next := append(slices.Clone(s.records), rec)
	if err := s.save(next); err != nil {
		return Connector{}, err
	}
	return rec.public(), nil
}

// Update replaces the definition of a connector and keeps secret values that the input leaves empty.
func (s *Service) Update(_ context.Context, ref string, in Input) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Connector{}, err
	}
	i := s.index(ref)
	if i < 0 {
		return Connector{}, errNotFound()
	}
	old := s.records[i]
	rec, err := normalize(in, old)
	if err != nil {
		return Connector{}, err
	}
	if s.keyTaken(rec.Key, old.ID) {
		return Connector{}, errKeyExists(rec.Key)
	}
	rec.ID, rec.CreatedAt, rec.UpdatedAt = old.ID, old.CreatedAt, s.now().UTC()
	rec.Enabled = old.Enabled
	if in.Enabled != nil {
		rec.Enabled = *in.Enabled
	}
	if sameTarget(old, rec) {
		rec.Status, rec.StatusStep, rec.StatusError = old.Status, old.StatusStep, old.StatusError
		rec.Tools, rec.CheckedAt, rec.OAuth = old.Tools, old.CheckedAt, old.OAuth
	} else {
		rec.Status = StatusUnchecked
		delete(s.sources, old.ID)
	}
	next := slices.Clone(s.records)
	next[i] = rec
	if err := s.save(next); err != nil {
		return Connector{}, err
	}
	return rec.public(), nil
}

// SetEnabled turns a connector on or off for new sessions.
func (s *Service) SetEnabled(_ context.Context, ref string, enabled bool) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Connector{}, err
	}
	i := s.index(ref)
	if i < 0 {
		return Connector{}, errNotFound()
	}
	next := slices.Clone(s.records)
	next[i].Enabled, next[i].UpdatedAt = enabled, s.now().UTC()
	if err := s.save(next); err != nil {
		return Connector{}, err
	}
	return next[i].public(), nil
}

// Delete removes a connector and its secret values.
func (s *Service) Delete(_ context.Context, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	i := s.index(ref)
	if i < 0 {
		return errNotFound()
	}
	id := s.records[i].ID
	if err := s.save(slices.Delete(slices.Clone(s.records), i, i+1)); err != nil {
		return err
	}
	delete(s.sources, id)
	for state, pending := range s.signIns {
		if pending.connectorID == id {
			pending.cancel()
			delete(s.signIns, state)
		}
	}
	return nil
}

func (s *Service) snapshot(ref string) (record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return record{}, err
	}
	i := s.index(ref)
	if i < 0 {
		return record{}, errNotFound()
	}
	return s.records[i], nil
}

func (s *Service) setStatus(id string, status Status, step, detail string, tools []string) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Connector{}, err
	}
	i := s.index(id)
	if i < 0 {
		return Connector{}, errNotFound()
	}
	if len(detail) > maxErrorBytes {
		detail = strings.ToValidUTF8(detail[:maxErrorBytes], "")
	}
	next := slices.Clone(s.records)
	rec := &next[i]
	rec.Status, rec.StatusStep, rec.StatusError, rec.CheckedAt = status, step, detail, s.now().UTC()
	if tools != nil {
		rec.Tools = tools
	}
	if err := s.save(next); err != nil {
		return Connector{}, err
	}
	return rec.public(), nil
}

func (s *Service) load() error {
	if s.loaded {
		return nil
	}
	records, err := readFile(s.path)
	if err != nil {
		return err
	}
	s.records, s.loaded = records, true
	return nil
}

func (s *Service) save(records []record) error {
	if err := writeFile(s.path, records); err != nil {
		return err
	}
	s.records = records
	return nil
}

func (s *Service) index(ref string) int {
	ref = strings.TrimSpace(ref)
	return slices.IndexFunc(s.records, func(r record) bool { return r.ID == ref || r.Key == ref })
}

func (s *Service) keyTaken(key, exceptID string) bool {
	return slices.ContainsFunc(s.records, func(r record) bool { return r.Key == key && r.ID != exceptID })
}

func (r *record) public() Connector {
	return Connector{
		ID: r.ID, Key: r.Key, Name: r.Name, PresetID: r.PresetID, Transport: r.Transport,
		URL: r.URL, Command: r.Command, Args: slices.Clone(r.Args),
		EnvKeys: slices.Sorted(maps.Keys(r.Env)), HeaderKeys: slices.Sorted(maps.Keys(r.Headers)),
		Auth: r.Auth, OwnClientID: r.OAuthClientID, SignedIn: r.OAuth != nil, ProjectID: r.ProjectID, Enabled: r.Enabled,
		Status: r.Status, StatusStep: r.StatusStep, StatusError: r.StatusError, Tools: slices.Clone(r.Tools),
		CheckedAt: r.CheckedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func sameTarget(a, b record) bool {
	return a.Transport == b.Transport && a.URL == b.URL && a.Command == b.Command && slices.Equal(a.Args, b.Args) &&
		a.Auth == b.Auth && maps.Equal(a.Env, b.Env) && maps.Equal(a.Headers, b.Headers) &&
		a.OAuthClientID == b.OAuthClientID && a.OAuthClientSecret == b.OAuthClientSecret
}

func normalize(in Input, old record) (record, error) {
	rec := record{
		Name: strings.TrimSpace(in.Name), PresetID: strings.TrimSpace(in.PresetID),
		Transport: in.Transport, Auth: in.Auth, ProjectID: strings.TrimSpace(in.ProjectID),
	}
	rec.Key = strings.TrimSpace(in.Key)
	if rec.Key == "" {
		rec.Key = strings.Trim(slugRun.ReplaceAllString(strings.ToLower(rec.Name), "-"), "-")
	}
	if rec.Name == "" {
		rec.Name = rec.Key
	}
	if !keyPattern.MatchString(rec.Key) || reservedKeys[rec.Key] {
		return record{}, invalid("INVALID_CONNECTOR_KEY", "The key must be 1 to 32 lowercase letters, digits, hyphens or underscores, and it cannot be \"ao\", \"import\" or \"proposals\"")
	}
	if rec.Name == "" || len(rec.Name) > maxNameBytes {
		return record{}, invalid("INVALID_CONNECTOR_NAME", "The name is required and must be at most 64 bytes")
	}
	switch rec.Transport {
	case TransportHTTP:
		return normalizeHTTP(in, old, rec)
	case TransportStdio:
		return normalizeStdio(in, old, rec)
	default:
		return record{}, invalid("INVALID_CONNECTOR_TRANSPORT", "The transport must be http or stdio")
	}
}

func normalizeHTTP(in Input, old, rec record) (record, error) {
	raw := strings.TrimSpace(in.URL)
	target, err := url.Parse(raw)
	if err != nil || len(raw) > maxURLBytes || target.Host == "" || target.User != nil ||
		(target.Scheme != "https" && (target.Scheme != "http" || !loopbackHost(target.Hostname()))) {
		return record{}, invalid("INVALID_CONNECTOR_URL", "The URL must be an https address, or an http address on this computer")
	}
	target.Fragment = ""
	rec.URL = target.String()
	if strings.TrimSpace(in.Command) != "" || len(in.Args) > 0 || len(in.Env) > 0 {
		return record{}, invalid("INVALID_CONNECTOR", "A server URL cannot have a command, arguments or environment values")
	}
	if rec.Auth == "" {
		rec.Auth = AuthNone
	}
	if rec.Auth != AuthNone && rec.Auth != AuthOAuth && rec.Auth != AuthHeader {
		return record{}, invalid("INVALID_CONNECTOR_AUTH", "The sign-in must be none, oauth or header")
	}
	rec.Headers, err = mergeSecrets(old.Headers, in.Headers, headerPattern)
	if err != nil {
		return record{}, err
	}
	if rec.Auth == AuthHeader && len(rec.Headers) == 0 {
		return record{}, invalid("INVALID_CONNECTOR_HEADERS", "Header sign-in needs at least one header with a value")
	}
	rec.OAuthClientID = strings.TrimSpace(in.OAuthClientID)
	if rec.OAuthClientID != "" {
		if rec.Auth != AuthOAuth || len(rec.OAuthClientID) > maxValueBytes || len(in.OAuthClientSecret) > maxValueBytes {
			return record{}, invalid("INVALID_CONNECTOR_CLIENT", "Your own OAuth app needs automatic sign-in and a valid client ID")
		}
		rec.OAuthClientSecret = in.OAuthClientSecret
		if rec.OAuthClientSecret == "" && rec.OAuthClientID == old.OAuthClientID {
			rec.OAuthClientSecret = old.OAuthClientSecret
		}
	}
	return rec, nil
}

func normalizeStdio(in Input, old, rec record) (record, error) {
	rec.Command = strings.TrimSpace(in.Command)
	if rec.Command == "" || len(rec.Command) > maxValueBytes {
		return record{}, invalid("INVALID_CONNECTOR_COMMAND", "A local command is required")
	}
	if strings.TrimSpace(in.URL) != "" || len(in.Headers) > 0 || (in.Auth != "" && in.Auth != AuthNone) {
		return record{}, invalid("INVALID_CONNECTOR", "A local command cannot have a URL, headers or a sign-in")
	}
	if len(in.Args) > maxArgs || slices.ContainsFunc(in.Args, func(arg string) bool { return len(arg) > maxValueBytes }) {
		return record{}, invalid("INVALID_CONNECTOR_ARGS", "The command has too many or too long arguments")
	}
	rec.Args, rec.Auth = slices.Clone(in.Args), AuthNone
	var err error
	rec.Env, err = mergeSecrets(old.Env, in.Env, envPattern)
	return rec, err
}

func mergeSecrets(old, in map[string]string, name *regexp.Regexp) (map[string]string, error) {
	if in == nil {
		return maps.Clone(old), nil
	}
	if len(in) > maxSecretPairs {
		return nil, invalid("INVALID_CONNECTOR_VALUES", "A connector can have at most 32 headers or environment values")
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if !name.MatchString(key) || len(value) > maxValueBytes || strings.ContainsAny(value, "\r\n") {
			return nil, invalid("INVALID_CONNECTOR_VALUES", "A header or environment name or value is not valid: "+key)
		}
		if value == "" {
			value = old[key]
		}
		if value != "" {
			out[key] = value
		}
	}
	return out, nil
}

func loopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func invalid(code, message string) error { return apierr.Invalid(code, message, nil) }

func errNotFound() error { return apierr.NotFound("CONNECTOR_NOT_FOUND", "Unknown connector") }

func errKeyExists(key string) error {
	return apierr.Conflict("CONNECTOR_KEY_EXISTS", "A connector with the key "+key+" already exists", nil)
}
