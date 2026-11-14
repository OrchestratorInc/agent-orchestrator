package domain

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kljensen/snowball/english"
)

// Session search vocabulary. The index is a derived read model: it is rebuilt
// from durable session facts and provider transcripts and never writes them.

// SessionSearchKind is what one indexed document represents. The ranking gives
// each kind its own tier, so the order here is also the display precedence:
// title, then a human message, then an agent reply, then everything operational.
type SessionSearchKind string

const (
	// SessionSearchKindTitle makes a session findable by its name.
	SessionSearchKindTitle SessionSearchKind = "title"
	// SessionSearchKindUser is a human message.
	SessionSearchKindUser SessionSearchKind = "user"
	// SessionSearchKindAssistant is an agent reply (including its reasoning).
	SessionSearchKindAssistant SessionSearchKind = "assistant"
	// SessionSearchKindTool is a tool call, its arguments, and its output.
	SessionSearchKindTool SessionSearchKind = "tool"
	// SessionSearchKindCommand is a shell command and its terminal output.
	SessionSearchKindCommand SessionSearchKind = "command"
	// SessionSearchKindPath makes a touched file path findable.
	SessionSearchKindPath SessionSearchKind = "path"
)

// SessionSearchSourceState is the health of one indexed source.
type SessionSearchSourceState string

const (
	// SessionSearchSourcePending has not been read yet.
	SessionSearchSourcePending SessionSearchSourceState = "pending"
	// SessionSearchSourceActive is fully indexed and current.
	SessionSearchSourceActive SessionSearchSourceState = "active"
	// SessionSearchSourceUnavailable is currently missing or unreadable and is
	// retried later. It never removes already-indexed documents.
	SessionSearchSourceUnavailable SessionSearchSourceState = "unavailable"
	// SessionSearchSourceUnsupported is a harness whose transcript AO does not
	// parse. Its session stays findable by title, and coverage says so.
	SessionSearchSourceUnsupported SessionSearchSourceState = "unsupported"
	// SessionSearchSourceNoTranscript is a session of an indexed harness that has
	// no resolvable transcript: a Chat session, or a TUI session whose workspace
	// was cleaned up. It is distinct from unsupported on purpose, because
	// reporting it as unsupported would claim the whole harness is unindexed and
	// make the coverage note lie. Recording it also stops every sweep from
	// re-globbing the provider tree for a session with nothing to read.
	SessionSearchSourceNoTranscript SessionSearchSourceState = "no_transcript"
)

// SessionSearchSourceKind distinguishes the two cursor models.
type SessionSearchSourceKind string

const (
	// SessionSearchSourceChat advances by conversation sequence.
	SessionSearchSourceChat SessionSearchSourceKind = "chat"
	// SessionSearchSourceNative advances by byte offset into a transcript.
	SessionSearchSourceNative SessionSearchSourceKind = "native"
)

// SessionSearchSort is how results are ordered.
type SessionSearchSort string

const (
	// SessionSearchSortRelevance ranks by kind tier then bm25: what typing does.
	SessionSearchSortRelevance SessionSearchSort = "relevance"
	// SessionSearchSortRecent orders by time: what browsing does.
	SessionSearchSortRecent SessionSearchSort = "recent"
)

// SessionSearchStatusFilter is the session lifecycle a query may restrict to.
type SessionSearchStatusFilter string

const (
	// SessionSearchStatusAny applies no lifecycle filter.
	SessionSearchStatusAny SessionSearchStatusFilter = ""
	// SessionSearchStatusKilled is a terminated (killed or archived) session.
	SessionSearchStatusKilled SessionSearchStatusFilter = "killed"
	// SessionSearchStatusLive is a non-terminated, non-hibernated session.
	SessionSearchStatusLive SessionSearchStatusFilter = "live"
	// SessionSearchStatusHibernated is a session whose provider host was stopped.
	SessionSearchStatusHibernated SessionSearchStatusFilter = "hibernated"
)

// SessionSearchDoc is one indexable unit of session content.
type SessionSearchDoc struct {
	SourceKey string
	DocKey    string
	SessionID string
	ProjectID string
	Harness   string
	Kind      SessionSearchKind
	// MessageID is the Chat timeline anchor a result opens at. Empty for native
	// transcript documents and for activities with no message bubble.
	MessageID string
	// Seq is the conversation sequence (Chat) or the transcript byte offset
	// (native).
	Seq       int64
	Title     string
	Body      string
	Paths     string
	CreatedAt time.Time
	IndexedAt time.Time
}

// Fingerprint identifies everything a document stores except when it was
// indexed. A pass compares it with the stored one and writes only documents
// that changed, so an active session's new message does not rewrite the rest.
func (d SessionSearchDoc) Fingerprint() int64 {
	hasher := fnv.New64a()
	for _, part := range []string{
		d.SourceKey, d.DocKey, d.SessionID, d.ProjectID, d.Harness, string(d.Kind), d.MessageID,
		strconv.FormatInt(d.Seq, 10), d.Title, d.Body, d.Paths, strconv.FormatInt(d.CreatedAt.UnixMilli(), 10),
	} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	// 0 is the stored default of a row written before fingerprints.
	return int64(hasher.Sum64() | 1) //nolint:gosec // a fingerprint; wrapping is fine
}

// SessionSearchDocRef is a stored document as a pass compares it: its row id,
// its key within the source, and its fingerprint.
type SessionSearchDocRef struct {
	ID          int64
	DocKey      string
	Kind        SessionSearchKind
	Fingerprint int64
}

// SessionSearchSource is the incremental cursor and health of one source.
type SessionSearchSource struct {
	SourceKey string
	Kind      SessionSearchSourceKind
	SessionID string
	ProjectID string
	Harness   string
	// Path is the transcript path for native sources; empty for Chat.
	Path string
	// CursorValue is the conversation sequence (Chat) or byte offset (native).
	CursorValue int64
	// FileSize and FileMtime detect a rewritten or truncated transcript, which
	// resets the offset instead of reading from a stale position.
	FileSize  int64
	FileMtime int64
	// Fingerprint identifies the exact source content the last pass saw. Chat
	// hashes its visible message ids and activity revisions, which is what makes
	// a rollback or an edit re-index even though the sequence head did not move.
	Fingerprint string
	// Stamp is the cheap aggregate a Chat sweep compared before deciding to read
	// this conversation. Native sources leave it empty and use the byte offset.
	Stamp         string
	DocCount      int64
	State         SessionSearchSourceState
	LastErrorCode string
	UpdatedAt     time.Time
}

// SessionSearchTarget is one AO session as the indexer sees it.
type SessionSearchTarget struct {
	ID            SessionID
	ProjectID     ProjectID
	Harness       AgentHarness
	Kind          string
	DisplayName   string
	Prompt        string
	LatestPrompt  string
	WorkspacePath string
	// Branch is the session's git branch: metadata a person remembers a session
	// by, searchable alongside the title at a lower weight.
	Branch       string
	SessionMode  SessionMode
	IsTerminated bool
	Hibernated   bool
	CreatedAt    time.Time
	// NativeSessionID is the provider's own session id (agent_session_id): the
	// id a Claude transcript or Codex rollout is named by. The AO session id
	// names no file. Empty for a Chat session, whose history AO stores itself.
	NativeSessionID string
	// NativeTranscriptPath is the transcript path the provider's hooks reported
	// for the session. It also finds a transcript under an AO-managed account's
	// own config directory, which the default provider roots do not cover.
	NativeTranscriptPath string
}

// SessionSearchConversation is one Chat conversation and its active branch.
//
// It carries the session's own name and harness, not just the conversation's:
// the name is what a person sees in the sidebar and therefore what a title match
// must rank against, and the harness is what the harness: operator filters on.
type SessionSearchConversation struct {
	ID             string
	SessionID      SessionID
	ProjectID      ProjectID
	ProviderTitle  string
	DisplayName    string
	Harness        AgentHarness
	ActiveBranchID string
	LatestSequence int64
	// ReviewID is set for a reviewer chat: the conversation a session's review
	// holds with its reviewer. It belongs to the session (SessionID) but opens
	// in the reviewer chat, not the session's own transcript.
	ReviewID string
	// Branch is the session's git branch, indexed as title metadata.
	Branch string
	// CreatedAt is when the session started (the conversation's own time when
	// it has no session). It dates the title document, so a name match counts
	// as old as the session for recency sorting and date filters.
	CreatedAt time.Time
}

// SessionSearchTitle is the name a session is findable by: the name the user
// sees, falling back to the provider's own thread title.
func SessionSearchTitle(displayName, providerTitle string) string {
	if name := strings.TrimSpace(displayName); name != "" {
		return name
	}
	return strings.TrimSpace(providerTitle)
}

// SessionSearchConversationStamp is a cheap, aggregate witness of a
// conversation's content: how many rows it has, how far its sequence reaches,
// and when it last changed.
//
// The sweep compares this stamp before reading any message or activity text.
// Without it, every sweep would load every conversation's full timeline just to
// discover that nothing changed, which is exactly the kind of idle work that
// must not happen on a small machine.
type SessionSearchConversationStamp struct {
	ConversationID string
	MessageCount   int64
	MessageSeqMax  int64
	ActivityCount  int64
	ActivitySeqMax int64
	// UpdatedAt is the newest row timestamp in milliseconds.
	UpdatedAt int64
	// Witness covers what counts and whole-second times miss: the active
	// branch, rolled-back and cancelled turns, the conversation's title, the
	// session's name and branch, and every row's revision with the newest
	// update time at full precision. Without it the sweep kept serving
	// discarded text, old names, and text edited in the same second.
	Witness string
}

// SessionSearchMessage is the visible slice of one Chat message.
type SessionSearchMessage struct {
	ID        string
	Sequence  int64
	Role      MessageRole
	Text      string
	CreatedAt time.Time
}

// SessionSearchActivity is the searchable slice of one non-prose timeline row:
// a command, a file change, a tool call, a plan, a reasoning summary.
type SessionSearchActivity struct {
	ID        string
	Sequence  int64
	Kind      ActivityKind
	Status    ActivityStatus
	Summary   string
	Detail    string
	Output    string
	Streamed  string
	CreatedAt time.Time
	UpdatedAt time.Time
	// AnchorMessageID is the message an activity is shown with: the last message
	// of its turn at or before it (the prompt it answered), else the turn's
	// first. A tool-output or command match opens there, so it has a place to
	// scroll to like a message match does.
	AnchorMessageID string
}

// SessionSearchParams is one ranked full-text query.
type SessionSearchParams struct {
	// MatchQuery is an FTS5 MATCH expression, already built by the query parser.
	MatchQuery string
	// ExactQuery, when set, marks the candidates that hold every ranked word as
	// typed (see SessionSearchParsedQuery.ExactQuery); they rank ahead of
	// candidates that match only a widened form.
	ExactQuery string
	// HighlightTerms are the tokens the result snippets mark.
	HighlightTerms []SessionSearchHighlightTerm
	ProjectID      string
	Harness        string
	Status         SessionSearchStatusFilter
	// Path restricts to sessions that touched a path containing this text.
	Path string
	// ExcludeQuery is an FTS5 expression for terms a result must NOT contain.
	// It excludes whole sessions, which is what "!term" means to a person
	// searching for sessions rather than documents.
	ExcludeQuery string
	From         *time.Time
	To           *time.Time
	Sort         SessionSearchSort
	// SessionID restricts to one session: the one a pasted pull-request link
	// belongs to.
	SessionID string
	// Group chooses one result per session (the default) or one per document.
	Group SessionSearchGroup
	// SkipSnippets returns results without snippets, for a pass that only needs
	// the ranking. A snippet re-tokenizes its document to place the highlights,
	// which costs about half a millisecond per row.
	SkipSnippets bool
	Limit        int
	Offset       int
}

// SessionSearchGroup is the unit a result list is made of.
type SessionSearchGroup string

const (
	// SessionSearchGroupSession returns each session once, at its best match,
	// with a count of its other matches. It is the default: people and agents
	// searching past sessions are asking "which session was it?".
	SessionSearchGroupSession SessionSearchGroup = "session"
	// SessionSearchGroupMessage returns every matching document.
	SessionSearchGroupMessage SessionSearchGroup = "message"
)

// SessionSearchTailWeight scores a grouped session as its best document plus
// this fraction of its other matching documents. 0 would rank a session by its
// single best match and 1 by the sum of all of them, which lets a long session
// win on volume. A quarter helps a session whose evidence is spread over
// several messages without letting length decide.
const SessionSearchTailWeight = 0.25

// SessionSearchTailDepth is how many of a session's other matches, best first,
// its tail adds up. With every match counted, a session that relays other
// sessions' messages, or a very long review, gathers hundreds of weak matches
// on common words and outranks the session named exactly what was typed.
const SessionSearchTailDepth = 5

// SessionSearchKindWeights multiply bm25 by document kind: a field boost. A
// kind is worth more or less evidence, but a strong match in a weaker kind can
// still beat a weak match in a stronger one, which a hard order by kind cannot
// do. The title column also carries its own bm25 column weight, so a title
// needs no extra boost here to lead.
var SessionSearchKindWeights = map[SessionSearchKind]float64{
	SessionSearchKindTitle:     1.0,
	SessionSearchKindUser:      1.25,
	SessionSearchKindPath:      1.0,
	SessionSearchKindAssistant: 1.0,
	SessionSearchKindTool:      0.8,
	SessionSearchKindCommand:   0.8,
}

// SessionSearchHit is one ranked index row plus its snippet.
type SessionSearchHit struct {
	// DocID is the indexed document's row id. Paging past the candidate cap
	// uses it to return each result exactly once.
	DocID     int64
	SessionID string
	ProjectID string
	Harness   string
	Kind      SessionSearchKind
	MessageID string
	Seq       int64
	Title     string
	Rank      float64
	// Snippet carries sentinel markers around matched terms; the service turns
	// them into ranges so no markup from the index reaches the renderer.
	Snippet   string
	CreatedAt time.Time
	// SessionMatches is how many messages in this hit's session matched (a
	// message and the tool output shown with it count once; the title does not
	// count): the set find in chat steps through.
	SessionMatches int
	// ReviewID is set when the hit is in one of the session's reviewer chats.
	ReviewID string
	// MatchTotal is how many results the whole query has (the same on every
	// hit of a page), counted by the ranking query itself so a page does not
	// need a second full-text pass to know its total.
	MatchTotal int64
	// MatchTotalCapped reports that the ranking stage hit its candidate cap,
	// so MatchTotal is a floor and the exact total needs a count.
	MatchTotalCapped bool
}

// SessionSearchHighlight is one matched span in a snippet, in UTF-16 code units:
// the unit JavaScript strings index by, which is where the spans are applied.
// Byte offsets would put every highlight after a non-ASCII character (an accent,
// CJK text, an emoji) on the wrong letters.
type SessionSearchHighlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SessionSearchResult is one result the UI or CLI renders.
type SessionSearchResult struct {
	SessionID   string                   `json:"sessionId"`
	ProjectID   string                   `json:"projectId"`
	Harness     string                   `json:"harness"`
	Kind        SessionSearchKind        `json:"kind"`
	MessageID   string                   `json:"messageId,omitempty"`
	Seq         int64                    `json:"seq"`
	Title       string                   `json:"title,omitempty"`
	Snippet     string                   `json:"snippet"`
	Highlights  []SessionSearchHighlight `json:"highlights,omitempty"`
	Rank        float64                  `json:"rank"`
	CreatedAt   time.Time                `json:"createdAt"`
	SessionName string                   `json:"sessionName,omitempty"`
	ProjectName string                   `json:"projectName,omitempty"`
	// SessionState is active, hibernated, terminated, or unknown, so the result
	// list can say what clicking will do before the click.
	SessionState string `json:"sessionState,omitempty"`
	// ResumeCommand is the command that reopens this session from a terminal.
	ResumeCommand string `json:"resumeCommand,omitempty"`
	// SessionMatches is how many messages in the session matched (a message and
	// the tool output shown with it count once; the title does not count). In a
	// grouped list the result is the session's best-matching message and this is
	// the "· 7 matches" the row shows and find in chat steps through.
	SessionMatches int `json:"sessionMatches,omitempty"`
	// ReviewID is set when the match is in one of the session's reviewer chats,
	// which is where opening the result goes.
	ReviewID string `json:"reviewId,omitempty"`
}

// SessionSearchCoverage says what the index can and cannot see.
type SessionSearchCoverage struct {
	// State is ready, backfilling, or unavailable.
	State              string `json:"state"`
	IndexedSources     int64  `json:"indexedSources"`
	PendingSources     int64  `json:"pendingSources"`
	UnsupportedSources int64  `json:"unsupportedSources"`
	UnavailableSources int64  `json:"unavailableSources"`
	IndexedDocs        int64  `json:"indexedDocs"`
	// UnindexedHarnesses names harnesses whose transcripts are not parsed.
	UnindexedHarnesses []string `json:"unindexedHarnesses,omitempty"`
	// BackfilledAt is when this process last finished a full pass; zero (and
	// omitted) until then.
	BackfilledAt time.Time `json:"backfilledAt,omitzero"`
}

// SessionSearchResponse is one page of results plus the coverage note for it.
type SessionSearchResponse struct {
	Query    string                `json:"query"`
	Results  []SessionSearchResult `json:"results"`
	Total    int                   `json:"total"`
	Limit    int                   `json:"limit"`
	Offset   int                   `json:"offset"`
	Sort     SessionSearchSort     `json:"sort"`
	Group    SessionSearchGroup    `json:"group"`
	Coverage SessionSearchCoverage `json:"coverage"`
}

// SessionSearchMaxLimit bounds one page. It is small on purpose: the index can
// hold hundreds of thousands of documents, and no surface renders more.
const SessionSearchMaxLimit = 100

// NormalizeSessionSearchLimit clamps a requested page size into the allowed
// range, defaulting to 20.
func NormalizeSessionSearchLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > SessionSearchMaxLimit {
		return SessionSearchMaxLimit
	}
	return limit
}

// SessionSearchPRRef is a pull-request URL a query may paste instead of words.
type SessionSearchPRRef struct {
	// URL is the normalized PR URL, without a trailing slash or fragment.
	URL string
}

// SessionSearchParsedQuery is the structured result of parsing one search box.
//
// One parser serves the Cmd+K palette and `ao search`, so the syntax a person
// learns in one place works in the other.
type SessionSearchParsedQuery struct {
	// Text is what the person typed, for echoing back.
	Text string
	// MatchQuery is the FTS5 MATCH expression that selects candidates; empty
	// when the query is only filters (a browse).
	MatchQuery string
	// ExactQuery matches the documents that hold every ranked word exactly as
	// typed: no stem, no prefix. MatchQuery widens each word to the prefix of
	// its stem so "verified" finds "verification", and that widening must not
	// let a longer word outrank the word itself; the store ranks this exact tier
	// first. Empty when no word was widened, or for an explicit OR.
	ExactQuery string
	// HighlightTerms are the folded tokens a snippet marks: every token the
	// match expression can match, with its prefix flag.
	HighlightTerms []SessionSearchHighlightTerm
	// ExcludeQuery is the FTS5 expression for !terms. The store applies it at
	// the session level, so excluding a term removes the whole session rather
	// than leaving its other rows visible.
	ExcludeQuery string
	Project      string
	Harness      string
	Path         string
	Status       SessionSearchStatusFilter
	// Sort is set only by a typed sort: operator. Empty leaves the caller's
	// choice (`ao search --sort`, the palette's recents browse)
	// in force.
	Sort SessionSearchSort
	From *time.Time
	To   *time.Time
	// PRRef is set when the query is a pull-request URL.
	PRRef *SessionSearchPRRef
	// HasOr records an explicit OR between terms, which joins the positive
	// expression with OR instead of AND.
	HasOr bool
	// InvalidOperators names operators whose value could not be understood. The
	// API rejects the query rather than ignoring the filter, because silently
	// dropping a filter returns results the person did not ask for.
	InvalidOperators []string
}

// SessionSearchOperatorPrefixes are the filters a query may carry. They are
// exported so the UI can offer completion without duplicating the list.
var SessionSearchOperatorPrefixes = []string{
	"harness:", "project:", "path:", "status:", "before:", "after:", "sort:",
	"created:",
}

// ParseSessionSearchQuery turns a search box into a structured query.
//
// Operators are recognized only at a word boundary and only with a value, so
// a bare "project:" stays a search term instead of filtering everything out.
// A PR URL is recognized anywhere in the input. Token syntax follows fzf:
// "quoted phrase", 'exact token (no prefix expansion), ^prefix and !exclude.
func ParseSessionSearchQuery(raw string) SessionSearchParsedQuery {
	parsed := SessionSearchParsedQuery{}
	var phrases, prefixes, exacts, excluded, words []string
	textParts := make([]string, 0, 8)

	for _, token := range splitQueryTokens(raw) {
		lower := strings.ToLower(token)
		if ref, ok := parsePRURL(token); ok {
			parsed.PRRef = &ref
			textParts = append(textParts, token)
			continue
		}
		if name, value, ok := splitOperator(lower, token); ok {
			switch name {
			case "harness":
				parsed.Harness = value
			case "project":
				parsed.Project = value
			case "path":
				parsed.Path = value
			case "status", "before", "after", "sort":
				// A value that cannot be read is rejected, as created: is: dropping
				// the filter silently would search more than was asked for.
				if !applyValueOperator(&parsed, name, value) {
					parsed.InvalidOperators = append(parsed.InvalidOperators, name+":"+value)
					textParts = append(textParts, token)
					continue
				}
			case "created", "date":
				// GitHub-style range: "created:2026-01-01..2026-06-01", with "*" as
				// an open end. One operator for both bounds is shorter to type than
				// after:/before: and reads the way a person says the range.
				from, to, ok := parseQueryDateRange(value)
				if !ok {
					// A malformed range must not silently widen the search to
					// everything: that reads as "this filter does nothing".
					parsed.InvalidOperators = append(parsed.InvalidOperators, "created:"+value)
					textParts = append(textParts, token)
					continue
				}
				parsed.From = from
				parsed.To = to
			}
			textParts = append(textParts, token)
			continue
		}
		switch {
		case strings.HasPrefix(token, `"`) && strings.HasSuffix(token, `"`) && len(token) > 2:
			if phrase := strings.TrimSpace(strings.Trim(token, `"`)); phrase != "" {
				phrases = append(phrases, phrase)
			}
		case strings.HasPrefix(token, `'`) && len(token) > 1:
			if exact := strings.TrimSpace(token[1:]); exact != "" {
				exacts = append(exacts, exact)
			}
		case strings.HasPrefix(token, "^") && len(token) > 1:
			if anchor := strings.TrimSpace(token[1:]); anchor != "" {
				prefixes = append(prefixes, anchor)
			}
		case strings.HasPrefix(token, "!") && len(token) > 1:
			if term := strings.TrimSpace(token[1:]); term != "" {
				excluded = append(excluded, term)
			}
		case token == "|" || token == "OR":
			// An explicit OR marker between groups. Recorded so the expression can
			// be joined with OR rather than AND.
			parsed.HasOr = true
		default:
			words = append(words, token)
		}
		textParts = append(textParts, token)
	}

	parsed.Text = strings.TrimSpace(strings.Join(textParts, " "))
	parsed.MatchQuery, parsed.ExactQuery, parsed.HighlightTerms = buildMatchExpressions(phrases, prefixes, exacts, words, parsed.HasOr)
	parsed.ExcludeQuery = buildExcludeExpression(excluded)
	return parsed
}

// splitQueryTokens splits on whitespace but keeps a quoted phrase together.
func splitQueryTokens(raw string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	for _, character := range raw {
		switch {
		case character == '"':
			inQuote = !inQuote
			current.WriteRune(character)
		case (character == ' ' || character == '\t' || character == '\n') && !inQuote:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(character)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// splitOperator reports whether a token is "name:value" for a known operator.
func splitOperator(lower, original string) (name, value string, ok bool) {
	for _, prefix := range SessionSearchOperatorPrefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		value = strings.TrimSpace(original[len(prefix):])
		if value == "" {
			return "", "", false
		}
		return strings.TrimSuffix(prefix, ":"), value, true
	}
	return "", "", false
}

// applyValueOperator sets the filter a status:, before:, after: or sort:
// operator names, reporting false when its value cannot be read.
func applyValueOperator(parsed *SessionSearchParsedQuery, name, value string) bool {
	switch name {
	case "status":
		status, ok := normalizeStatusOperator(value)
		parsed.Status = status
		return ok
	case "before", "after":
		at, err := parseQueryDate(value)
		if err != nil {
			return false
		}
		if name == "before" {
			parsed.To = &at
		} else {
			parsed.From = &at
		}
		return true
	default: // sort
		switch {
		case strings.EqualFold(value, string(SessionSearchSortRecent)):
			parsed.Sort = SessionSearchSortRecent
		case strings.EqualFold(value, string(SessionSearchSortRelevance)):
			parsed.Sort = SessionSearchSortRelevance
		default:
			return false
		}
		return true
	}
}

func normalizeStatusOperator(value string) (SessionSearchStatusFilter, bool) {
	switch strings.ToLower(value) {
	case "killed", "terminated", "dead", "archived":
		return SessionSearchStatusKilled, true
	case "live", "running", "active":
		return SessionSearchStatusLive, true
	case "hibernated", "asleep":
		return SessionSearchStatusHibernated, true
	default:
		return SessionSearchStatusAny, false
	}
}

// parseQueryDateRange reads "A..B", "A..*" or "*..B". A malformed range is
// rejected rather than silently treated as one bound, so a typo cannot quietly
// widen a search.
func parseQueryDateRange(value string) (*time.Time, *time.Time, bool) {
	start, end, found := strings.Cut(value, "..")
	if !found {
		return nil, nil, false
	}
	var from, to *time.Time
	if strings.TrimSpace(start) != "" && strings.TrimSpace(start) != "*" {
		parsed, err := parseQueryDate(strings.TrimSpace(start))
		if err != nil {
			return nil, nil, false
		}
		from = &parsed
	}
	if strings.TrimSpace(end) != "" && strings.TrimSpace(end) != "*" {
		parsed, err := parseQueryDate(strings.TrimSpace(end))
		if err != nil {
			return nil, nil, false
		}
		to = &parsed
	}
	if from == nil && to == nil {
		return nil, nil, false
	}
	return from, to, true
}

func parseQueryDate(value string) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at.UTC(), nil
	}
	at, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return time.Time{}, err
	}
	return at.UTC(), nil
}

// Phrase is the FTS5 phrase that finds text mentioning this PR. A URL such as
// "github.com/acme/widgets/pull/6553" tokenizes as "... acme widgets pull 6553",
// so the phrase holds the repository too: "pull 6553" alone also matches PR
// 6553 of every other repository.
func (ref SessionSearchPRRef) Phrase() string {
	parts := strings.Split(ref.URL, "/")
	if len(parts) < 7 || parts[6] == "" {
		return ""
	}
	words := strings.FieldsFunc(strings.ToLower(parts[3]+" "+parts[4]), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return `"` + strings.Join(append(words, "pull", parts[6]), " ") + `"`
}

// SessionSearchPathMatch turns a path: fragment into an FTS5 expression over
// the paths column, which holds the files a session changed. The fragment's
// parts must appear in order ("src/routes" matches src/routes/status.js) and
// the last part may be a prefix ("src/rou"). It is empty when the fragment has
// no letters or digits, which leaves the filter off. Matching the paths
// column, not document bodies, is what keeps path: to sessions that touched a
// file rather than ones that merely printed its name (an ls or a grep).
func SessionSearchPathMatch(fragment string) string {
	parts := strings.FieldsFunc(strings.ToLower(fragment), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(parts) == 0 {
		return ""
	}
	return `paths : "` + strings.Join(parts, " ") + `" *`
}

// parsePRURL recognizes a pull-request URL so pasting one finds its session.
func parsePRURL(token string) (SessionSearchPRRef, bool) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(token), "/")
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return SessionSearchPRRef{}, false
	}
	if end := strings.IndexAny(trimmed, "?#"); end >= 0 {
		trimmed = trimmed[:end]
	}
	parts := strings.Split(trimmed, "/")
	// https: "" host owner repo pull N [files | commits | checks ...]: a tab
	// of the PR copied from the address bar is the same PR.
	if len(parts) < 7 || parts[5] != "pull" {
		return SessionSearchPRRef{}, false
	}
	number := parts[6]
	if number == "" {
		return SessionSearchPRRef{}, false
	}
	if _, err := parseQueryDate(number); err == nil {
		return SessionSearchPRRef{}, false
	}
	for _, character := range number {
		if character < '0' || character > '9' {
			return SessionSearchPRRef{}, false
		}
	}
	return SessionSearchPRRef{URL: strings.Join(parts[:7], "/")}, true
}

// buildMatchExpressions assembles the FTS5 expressions for one query. Every
// term is quoted, so input is never read as FTS5 syntax.
//
// A "quoted phrase", an 'exact token and a ^prefix are required. Plain words
// are ranked, not required: a question ("how did we fix the flaky login
// test?") almost never has all of its words in one message, so a strict AND
// returns nothing. They are joined with OR and bm25 puts a document holding
// more of them, or rarer ones, first. Two rules keep OR precise:
//
//   - Stopwords are dropped from the ranked words (unless every word is one):
//     they occur in nearly every message and would admit the whole index. A
//     phrase keeps its stopwords.
//   - Only the last word is prefix-matched, the one still being typed;
//     expanding every word floods the candidates.
//
// Each ranked word matches as a prefix of its Porter stem, so "signatures
// verified" also finds "signature verification". exact is the same words as
// typed, for the tier that keeps the word itself ahead of its longer forms.
func buildMatchExpressions(phrases, prefixes, exacts, words []string, useOr bool) (match, exact string, highlights []SessionSearchHighlightTerm) {
	required := make([]string, 0, len(phrases)+len(prefixes)+len(exacts))
	seen := map[string]struct{}{}
	add := func(list *[]string, term string, prefix bool) {
		quoted := quoteFTS(term)
		if prefix {
			quoted += "*"
		}
		if _, ok := seen[quoted]; ok {
			return
		}
		seen[quoted] = struct{}{}
		*list = append(*list, quoted)
		highlights = append(highlights, sessionSearchHighlightTerms(term, prefix)...)
	}
	for _, phrase := range exacts {
		add(&required, phrase, false)
	}
	for _, phrase := range phrases {
		// A phrase containing whitespace is matched whole, without expanding its
		// last token: "resets its backoff" must not match "resets its backoffs".
		add(&required, phrase, !strings.ContainsAny(phrase, " \t"))
	}
	for _, anchor := range prefixes {
		add(&required, anchor, true)
	}

	ranked := rankedWords(words)
	optional := make([]string, 0, len(ranked))
	// The words as typed, for the exact tier, and whether any was widened.
	asTyped := make([]string, 0, len(ranked))
	typedSeen := map[string]struct{}{}
	widened := false
	for index, word := range ranked {
		prefix := index == len(ranked)-1
		if tinyWord(word) {
			// "c*" expands to every term starting with c: seconds of work for
			// a match that means nothing.
			prefix = false
		}
		term := word
		if root, ok := stemRoot(word); ok {
			term, prefix = root, true
		}
		add(&optional, term, prefix)
		if prefix || term != word {
			widened = true
		}
		if quoted := quoteFTS(word); quoted != `""` {
			if _, ok := typedSeen[quoted]; !ok {
				typedSeen[quoted] = struct{}{}
				asTyped = append(asTyped, quoted)
			}
		}
	}
	if widened && !useOr && len(asTyped) > 0 {
		exact = strings.Join(append(append([]string{}, required...), asTyped...), " AND ")
	}

	if useOr {
		all := make([]string, 0, len(required)+len(optional))
		all = append(all, required...)
		all = append(all, optional...)
		return strings.Join(all, " OR "), "", highlights
	}
	switch {
	case len(required) == 0 && len(optional) == 0:
		return "", "", nil
	case len(optional) <= 1:
		// One word (or none) is not a ranking problem: every term is required.
		// It is still a widened one ("rebasing" also finds "rebased"), so the
		// exact tier still applies.
		return strings.Join(append(required, optional...), " AND "), exact, highlights
	}
	anyWord := "(" + strings.Join(optional, " OR ") + ")"
	if len(required) > 0 {
		return strings.Join(required, " AND ") + " AND " + anyWord, exact, highlights
	}
	return anyWord, exact, highlights
}

// stemRoot is the prefix a word's inflected forms share: its English (Porter2) stem, cut
// back to where the stem and the word still agree, minus a final "i" that the
// stemmer produces from "y" ("verified" stems to "verifi", but "verify" must
// match too). It reports false when the root is too short to be specific: a
// three-letter root such as "run" would also match "runtime".
func stemRoot(word string) (string, bool) {
	lower := strings.ToLower(strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
	stem := english.Stem(lower, true)
	if stem == lower && !strings.HasSuffix(stem, "i") {
		return "", false
	}
	common := 0
	for common < len(stem) && common < len(lower) && stem[common] == lower[common] {
		common++
	}
	root := stem[:common]
	if strings.HasSuffix(root, "i") && len(root) > sessionSearchMinStemRoot {
		root = root[:len(root)-1]
	}
	if len(root) < sessionSearchMinStemRoot {
		return "", false
	}
	return root, true
}

// sessionSearchMinStemRoot is the shortest root stem matching expands.
const sessionSearchMinStemRoot = 4

// searchableWords drops words that carry no letter or digit ("-", "?"), which
// the tokenizer would reduce to an empty phrase. If nothing is left, the input
// is returned so the query still says what was typed.
func searchableWords(words []string) []string {
	kept := make([]string, 0, len(words))
	for _, word := range words {
		if strings.IndexFunc(word, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			kept = append(kept, word)
		}
	}
	if len(kept) == 0 {
		return words
	}
	return kept
}

// maxRankedWords bounds how many distinct words one query ranks by. A pasted
// log or stack trace is mostly unique tokens (timestamps, ids, line numbers);
// OR-ing every one of them costs a posting-list pass each and ranks no better
// than its first few dozen words.
const maxRankedWords = 24

// rankedWords is the plain words minus stopwords, keeping them all when every
// word is a stopword (a search for "the" means it). A one-character word ("C"
// of "C++", the "n" of a pasted "\n") is dropped the same way: it matches
// nearly every document and says nothing about which one is wanted. A query of
// nothing but such words ranks by none of them. Repeats count once, and at most
// maxRankedWords are kept, in the order typed.
func rankedWords(words []string) []string {
	searchable := searchableWords(words)
	kept := make([]string, 0, len(searchable))
	stopwords := make([]string, 0, len(searchable))
	seen := make(map[string]struct{}, len(searchable))
	for _, word := range searchable {
		if tinyWord(word) {
			continue
		}
		key := strings.ToLower(word)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if isSessionSearchStopword(word) {
			stopwords = append(stopwords, word)
			continue
		}
		kept = append(kept, word)
	}
	if len(kept) == 0 {
		kept = stopwords
	}
	if len(kept) > maxRankedWords {
		kept = kept[:maxRankedWords]
	}
	return kept
}

// tinyWord reports a word with at most one letter or digit: what the index's
// tokenizer would keep of it is a single character.
func tinyWord(word string) bool {
	count := 0
	for _, r := range word {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			count++
			if count > 1 {
				return false
			}
		}
	}
	return true
}

// isSessionSearchStopword reports whether a typed word is a function word. A
// contraction is judged by its head ("I'm" -> "i", "don't" -> "don").
func isSessionSearchStopword(word string) bool {
	lower := strings.ToLower(strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
	if head, _, found := strings.Cut(strings.ReplaceAll(lower, "’", "'"), "'"); found {
		lower = head
	}
	_, stop := sessionSearchStopwords[lower]
	return stop
}

// sessionSearchStopwords is NLTK's English stopword list (179 words; Bird,
// Klein & Loper, "Natural Language Processing with Python"), the list most IR
// baselines use. It is applied to the ranked words of a query only; documents
// are indexed with every word, so a phrase or 'exact search can still find one.
var sessionSearchStopwords = func() map[string]struct{} {
	words := strings.Fields(`i me my myself we our ours ourselves you your yours yourself yourselves
he him his himself she her hers herself it its itself they them their theirs themselves
what which who whom this that these those am is are was were be been being have has had
having do does did doing a an the and but if or because as until while of at by for with
about against between into through during before after above below to from up down in out
on off over under again further then once here there when where why how all any both each
few more most other some such no nor not only own same so than too very s t can will just
don should now d ll m o re ve y ain aren couldn didn doesn hadn hasn haven isn ma mightn
mustn needn shan shouldn wasn weren won wouldn`)
	set := make(map[string]struct{}, len(words))
	for _, word := range words {
		set[word] = struct{}{}
	}
	return set
}()

// buildExcludeExpression assembles the expression for !terms. It is a plain
// OR of the excluded terms, because a document matching any of them puts its
// whole session out of scope.
func buildExcludeExpression(excluded []string) string {
	parts := make([]string, 0, len(excluded))
	seen := map[string]struct{}{}
	for _, term := range excluded {
		trimmed := strings.TrimSpace(term)
		if trimmed == "" {
			continue
		}
		quoted := quoteFTS(trimmed)
		if _, ok := seen[quoted]; ok {
			continue
		}
		seen[quoted] = struct{}{}
		parts = append(parts, quoted)
	}
	return strings.Join(parts, " OR ")
}

// quoteFTS wraps a term in an FTS5 string, escaping an embedded quote by
// doubling it.
func quoteFTS(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
}

// SessionSearchSplitIdentifiers expands a body for indexing so an identifier is
// findable by its parts: "handleSubmit" also indexes "handle" and "submit", and
// "src/auth/login_test.ts" also indexes "src", "auth", "login", "test" and "ts".
//
// The original text is always kept, so searching the whole identifier still
// matches. The expansion is bounded: only the first maxIdentifiers identifiers
// are split, which keeps a 64 KiB command output from tripling the index.
func SessionSearchSplitIdentifiers(body string, maxIdentifiers int) string {
	if body == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(body) + len(body)/2)
	builder.WriteString(body)
	added := 0
	for _, field := range strings.Fields(body) {
		if added >= maxIdentifiers {
			break
		}
		parts := identifierParts(field)
		if len(parts) < 2 {
			continue
		}
		if added == 0 {
			// Everything after the marker is for the index, not for reading.
			builder.WriteString("\n" + SessionSearchExpansionMarker)
		}
		builder.WriteByte(' ')
		builder.WriteString(strings.Join(parts, " "))
		added++
	}
	return builder.String()
}

// camelCaseParts splits one run of letters and digits at camel-case and
// acronym boundaries:
//
//	handleSubmit      -> handle | Submit
//	ParseHTTPResponse -> Parse | HTTP | Response
//
// A boundary sits before an uppercase rune that follows a lowercase one, and
// before the last uppercase rune of a run that a lowercase rune follows.
// Without the second rule "http" would not find parseHTTPResponse.
func camelCaseParts(chunk string) []string {
	runes := []rune(chunk)
	if len(runes) < 2 {
		return nil
	}
	upper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	lower := func(r rune) bool { return r >= 'a' && r <= 'z' }
	digit := func(r rune) bool { return r >= '0' && r <= '9' }

	var parts []string
	start := 0
	for index := 1; index < len(runes); index++ {
		previous, current := runes[index-1], runes[index]
		boundary := false
		switch {
		case upper(current) && lower(previous):
			// handleSubmit -> handle | Submit
			boundary = true
		case upper(current) && upper(previous) && index+1 < len(runes) && lower(runes[index+1]):
			// ParseHTTPResponse -> Parse | HTTP | Response: the boundary sits
			// before this uppercase rune, because the next rune starts a word.
			boundary = true
		case digit(current) && !digit(previous) && upper(previous):
			// HTTP2 -> HTTP | 2 is not worth splitting; keep it whole.
			boundary = false
		}
		if !boundary {
			continue
		}
		if index-start > 0 {
			parts = append(parts, strings.ToLower(string(runes[start:index])))
		}
		start = index
	}
	if start > 0 && len(runes)-start > 0 {
		parts = append(parts, strings.ToLower(string(runes[start:])))
	}
	// A single part means the chunk had no internal boundary after all.
	if len(parts) < 2 {
		return nil
	}
	return parts
}

// identifierParts splits one token on path separators, underscores, hyphens,
// dots, and camel-case boundaries. It returns nil when there is nothing to add.
func identifierParts(token string) []string {
	trimmed := strings.Trim(token, `"'`+"`"+`()[]{}<>,;:!?`)
	if trimmed == "" {
		return nil
	}
	separated := strings.FieldsFunc(trimmed, func(r rune) bool {
		switch r {
		case '/', '\\', '_', '-', '.', '@', '#', '$', '=', '+', '*', '&', '|', '~':
			return true
		}
		return false
	})
	parts := make([]string, 0, len(separated)+2)
	for _, chunk := range separated {
		if chunk == "" {
			continue
		}
		parts = append(parts, strings.ToLower(chunk))
		parts = append(parts, camelCaseParts(chunk)...)
	}
	if len(parts) < 2 {
		return nil
	}
	sort.SliceStable(parts, func(left, right int) bool { return parts[left] < parts[right] })
	// De-duplicate while keeping the sorted order deterministic.
	unique := parts[:0]
	for _, part := range parts {
		if len(unique) > 0 && unique[len(unique)-1] == part {
			continue
		}
		unique = append(unique, part)
	}
	return unique
}

// SessionSearchResumeCommand is the shell command that reopens a session from a
// terminal, offered as a secondary action on a result. A terminated session is
// restored; resume-agent refuses it.
func SessionSearchResumeCommand(sessionID, state string) string {
	if sessionID == "" {
		return ""
	}
	if state == "terminated" {
		return "ao session restore " + sessionID
	}
	return "ao session resume-agent " + sessionID
}

// Validate checks the parts of a parsed query that cannot be corrected by
// clamping, so the API can answer 400 instead of guessing.
func (q SessionSearchParsedQuery) Validate() error {
	if len(q.InvalidOperators) > 0 {
		return fmt.Errorf("could not read %s; use a date, a range like 2026-01-01..2026-06-01, or * for an open end",
			strings.Join(q.InvalidOperators, ", "))
	}
	if q.From != nil && q.To != nil && q.From.After(*q.To) {
		return fmt.Errorf("the date range starts after it ends")
	}
	return nil
}
