package connectors

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const (
	proposalLife       = 30 * time.Minute
	maxProposals       = 8
	maxProposalReason  = 300
	proposalValueStand = "value"
)

// ProposalState says where a proposal is.
type ProposalState string

// Proposal states.
const (
	ProposalPending  ProposalState = "pending"
	ProposalApproved ProposalState = "approved"
	ProposalDeclined ProposalState = "declined"
	ProposalExpired  ProposalState = "expired"
)

// Proposal is a connector an agent asked the user to add; it holds the names of values, never a value.
type Proposal struct {
	ID           string
	SessionID    string
	Key          string
	Name         string
	Transport    Transport
	URL          string
	Command      string
	Args         []string
	EnvKeys      []string
	HeaderKeys   []string
	Auth         Auth
	ProjectID    string
	Reason       string
	State        ProposalState
	ConnectorKey string
	CreatedAt    time.Time
}

// ProposalInput is what an agent gives when it asks for a connector.
type ProposalInput struct {
	SessionID  string
	Key        string
	Name       string
	Transport  Transport
	URL        string
	Command    string
	Args       []string
	EnvKeys    []string
	HeaderKeys []string
	Auth       Auth
	Reason     string
}

// Propose records a connector that the session's agent asks for. Nothing is added and nothing runs.
func (s *Service) Propose(ctx context.Context, in ProposalInput) (Proposal, error) {
	s.mu.Lock()
	lookup := s.sessionLookup
	s.mu.Unlock()
	projectID := ""
	if strings.TrimSpace(in.SessionID) == "" {
		return Proposal{}, invalid("CONNECTOR_PROPOSAL_SESSION", "A proposal needs the session that asks")
	}
	if lookup != nil {
		var open bool
		if projectID, open = lookup(ctx, in.SessionID); !open {
			return Proposal{}, apierr.NotFound("CONNECTOR_PROPOSAL_SESSION", "Unknown session")
		}
	}
	draft := Input{
		Key: in.Key, Name: in.Name, Transport: in.Transport, URL: in.URL, Command: in.Command, Args: in.Args,
		Env: standIns(in.EnvKeys), Headers: standIns(in.HeaderKeys), Auth: in.Auth,
	}
	if draft.Transport == TransportHTTP && draft.Auth == "" {
		draft.Auth = AuthOAuth
		if len(in.HeaderKeys) > 0 {
			draft.Auth = AuthHeader
		}
	}
	rec, err := normalize(draft, record{})
	if err != nil {
		return Proposal{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if len(reason) > maxProposalReason {
		return Proposal{}, invalid("CONNECTOR_PROPOSAL_REASON", "The reason must be at most 300 bytes")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Proposal{}, err
	}
	if s.keyTaken(rec.Key, "") {
		return Proposal{}, errKeyExists(rec.Key)
	}
	now := s.now()
	pending := 0
	for id, old := range s.proposals {
		if now.Sub(old.CreatedAt) > proposalLife {
			delete(s.proposals, id)
			continue
		}
		if old.State != ProposalPending || old.SessionID != in.SessionID {
			continue
		}
		if old.Key == rec.Key {
			if old.Transport == rec.Transport && old.URL == rec.URL && old.Command == rec.Command && slices.Equal(old.Args, rec.Args) &&
				old.Auth == rec.Auth && slices.Equal(old.EnvKeys, sortedKeys(rec.Env)) && slices.Equal(old.HeaderKeys, sortedKeys(rec.Headers)) {
				return *old, nil
			}
			old.State = ProposalExpired
			continue
		}
		pending++
	}
	if pending >= maxProposals {
		return Proposal{}, apierr.Conflict("CONNECTOR_PROPOSAL_LIMIT", "Too many proposals of this session wait for an answer", nil)
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return Proposal{}, err
	}
	out := &Proposal{
		ID: hex.EncodeToString(raw), SessionID: in.SessionID, Key: rec.Key, Name: rec.Name, Transport: rec.Transport,
		URL: rec.URL, Command: rec.Command, Args: slices.Clone(rec.Args), EnvKeys: sortedKeys(rec.Env), HeaderKeys: sortedKeys(rec.Headers),
		Auth: rec.Auth, ProjectID: projectID, Reason: reason, State: ProposalPending, CreatedAt: now,
	}
	if s.proposals == nil {
		s.proposals = map[string]*Proposal{}
	}
	s.proposals[out.ID] = out
	return *out, nil
}

// GetProposal returns one proposal. A proposal nobody answered in time reads as expired.
func (s *Service) GetProposal(_ context.Context, id string) (Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found, ok := s.proposals[id]
	if !ok {
		return Proposal{}, errNoProposal()
	}
	return s.aged(*found), nil
}

// SessionProposals returns the proposals of a session that wait for an answer, oldest first.
func (s *Service) SessionProposals(_ context.Context, sessionID string) []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Proposal{}
	for _, one := range s.proposals {
		if one.SessionID == sessionID && s.aged(*one).State == ProposalPending {
			out = append(out, *one)
		}
	}
	slices.SortFunc(out, func(a, b Proposal) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// ApproveProposal adds the proposed connector with the values the user typed, for projectID when given.
func (s *Service) ApproveProposal(ctx context.Context, id string, projectID *string, values map[string]string) (Proposal, Connector, error) {
	s.mu.Lock()
	found, ok := s.proposals[id]
	var pending Proposal
	if ok {
		pending = s.aged(*found)
	}
	s.mu.Unlock()
	if !ok {
		return Proposal{}, Connector{}, errNoProposal()
	}
	if pending.State != ProposalPending {
		return Proposal{}, Connector{}, apierr.Conflict("CONNECTOR_PROPOSAL_ANSWERED", "This proposal is "+string(pending.State), nil)
	}
	fill := func(names []string) (map[string]string, error) {
		out := make(map[string]string, len(names))
		for _, name := range names {
			if values[name] == "" {
				return nil, invalid("CONNECTOR_PROPOSAL_VALUE", pending.Name+" needs a value for "+name)
			}
			out[name] = values[name]
		}
		return out, nil
	}
	env, err := fill(pending.EnvKeys)
	if err != nil {
		return Proposal{}, Connector{}, err
	}
	headers, err := fill(pending.HeaderKeys)
	if err != nil {
		return Proposal{}, Connector{}, err
	}
	scope := pending.ProjectID
	if projectID != nil {
		scope = *projectID
	}
	created, err := s.Create(ctx, Input{
		Key: pending.Key, Name: pending.Name, Transport: pending.Transport, URL: pending.URL, Command: pending.Command,
		Args: pending.Args, Env: env, Headers: headers, Auth: pending.Auth, ProjectID: scope,
	})
	if err != nil {
		return Proposal{}, Connector{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found.State, found.ConnectorKey = ProposalApproved, created.Key
	return *found, created, nil
}

// DeclineProposal records that the user does not want the proposed connector.
func (s *Service) DeclineProposal(_ context.Context, id string) (Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found, ok := s.proposals[id]
	if !ok {
		return Proposal{}, errNoProposal()
	}
	if s.aged(*found).State == ProposalPending {
		found.State = ProposalDeclined
	}
	return s.aged(*found), nil
}

func (s *Service) aged(p Proposal) Proposal {
	if p.State == ProposalPending && s.now().Sub(p.CreatedAt) > proposalLife {
		p.State = ProposalExpired
	}
	return p
}

func standIns(names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = proposalValueStand
	}
	return out
}

func sortedKeys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for name := range values {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func errNoProposal() error {
	return apierr.NotFound("CONNECTOR_PROPOSAL_NOT_FOUND", "Unknown proposal")
}
