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

type SessionSearchKind string

const (
	SessionSearchKindTitle     SessionSearchKind = "title"
	SessionSearchKindUser      SessionSearchKind = "user"
	SessionSearchKindAssistant SessionSearchKind = "assistant"
	SessionSearchKindTool      SessionSearchKind = "tool"
	SessionSearchKindCommand   SessionSearchKind = "command"
	SessionSearchKindPath      SessionSearchKind = "path"
)

type SessionSearchSourceState string

const (
	SessionSearchSourcePending      SessionSearchSourceState = "pending"
	SessionSearchSourceActive       SessionSearchSourceState = "active"
	SessionSearchSourceUnavailable  SessionSearchSourceState = "unavailable"
	SessionSearchSourceUnsupported  SessionSearchSourceState = "unsupported"
	SessionSearchSourceNoTranscript SessionSearchSourceState = "no_transcript"
)

type SessionSearchSourceKind string

const (
	SessionSearchSourceChat   SessionSearchSourceKind = "chat"
	SessionSearchSourceNative SessionSearchSourceKind = "native"
)

type SessionSearchSort string

const (
	SessionSearchSortRelevance SessionSearchSort = "relevance"
	SessionSearchSortRecent    SessionSearchSort = "recent"
)

type SessionSearchStatusFilter string

const (
	SessionSearchStatusAny        SessionSearchStatusFilter = ""
	SessionSearchStatusKilled     SessionSearchStatusFilter = "killed"
	SessionSearchStatusLive       SessionSearchStatusFilter = "live"
	SessionSearchStatusHibernated SessionSearchStatusFilter = "hibernated"
)

type SessionSearchDoc struct {
	SourceKey string
	DocKey    string
	SessionID string
	ProjectID string
	Harness   string
	Kind      SessionSearchKind
	MessageID string
	Seq       int64
	Title     string
	Body      string
	Paths     string
	CreatedAt time.Time
	IndexedAt time.Time
}

func (d SessionSearchDoc) Fingerprint() int64 {
	hasher := fnv.New64a()
	for _, part := range []string{
		d.SourceKey, d.DocKey, d.SessionID, d.ProjectID, d.Harness, string(d.Kind), d.MessageID,
		strconv.FormatInt(d.Seq, 10), d.Title, d.Body, d.Paths, strconv.FormatInt(d.CreatedAt.UnixMilli(), 10),
	} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return int64(hasher.Sum64() | 1) //nolint:gosec // a fingerprint; wrapping is fine
}

type SessionSearchDocRef struct {
	ID          int64
	DocKey      string
	Kind        SessionSearchKind
	Fingerprint int64
}

type SessionSearchSource struct {
	SourceKey     string
	Kind          SessionSearchSourceKind
	SessionID     string
	ProjectID     string
	Harness       string
	Path          string
	CursorValue   int64
	FileSize      int64
	FileMtime     int64
	Fingerprint   string
	Stamp         string
	DocCount      int64
	State         SessionSearchSourceState
	LastErrorCode string
	UpdatedAt     time.Time
}

type SessionSearchTarget struct {
	ID                   SessionID
	ProjectID            ProjectID
	Harness              AgentHarness
	Kind                 string
	DisplayName          string
	Prompt               string
	LatestPrompt         string
	WorkspacePath        string
	Branch               string
	SessionMode          SessionMode
	IsTerminated         bool
	Hibernated           bool
	CreatedAt            time.Time
	NativeSessionID      string
	NativeTranscriptPath string
}

type SessionSearchConversation struct {
	ID             string
	SessionID      SessionID
	ProjectID      ProjectID
	ProviderTitle  string
	DisplayName    string
	Harness        AgentHarness
	ActiveBranchID string
	LatestSequence int64
	ReviewID       string
	Branch         string
	CreatedAt      time.Time
}

func SessionSearchTitle(displayName, providerTitle string) string {
	if name := strings.TrimSpace(displayName); name != "" {
		return name
	}
	return strings.TrimSpace(providerTitle)
}

type SessionSearchConversationStamp struct {
	ConversationID string
	MessageCount   int64
	MessageSeqMax  int64
	ActivityCount  int64
	ActivitySeqMax int64
	UpdatedAt      int64
	Witness        string
}

type SessionSearchMessage struct {
	ID        string
	Sequence  int64
	Role      MessageRole
	Text      string
	CreatedAt time.Time
}

type SessionSearchActivity struct {
	ID              string
	Sequence        int64
	Kind            ActivityKind
	Status          ActivityStatus
	Summary         string
	Detail          string
	Output          string
	Streamed        string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	AnchorMessageID string
}

type SessionSearchParams struct {
	MatchQuery     string
	ExactQuery     string
	HighlightTerms []SessionSearchHighlightTerm
	ProjectID      string
	Harness        string
	Status         SessionSearchStatusFilter
	Path           string
	ExcludeQuery   string
	From           *time.Time
	To             *time.Time
	Sort           SessionSearchSort
	SessionID      string
	Group          SessionSearchGroup
	SkipSnippets   bool
	Limit          int
	Offset         int
}

type SessionSearchGroup string

const (
	SessionSearchGroupSession SessionSearchGroup = "session"
	SessionSearchGroupMessage SessionSearchGroup = "message"
)

const SessionSearchTailWeight = 0.25

const SessionSearchTailDepth = 5

var SessionSearchKindWeights = map[SessionSearchKind]float64{
	SessionSearchKindTitle:     1.0,
	SessionSearchKindUser:      1.25,
	SessionSearchKindPath:      1.0,
	SessionSearchKindAssistant: 1.0,
	SessionSearchKindTool:      0.8,
	SessionSearchKindCommand:   0.8,
}

type SessionSearchHit struct {
	DocID            int64
	SessionID        string
	ProjectID        string
	Harness          string
	Kind             SessionSearchKind
	MessageID        string
	Seq              int64
	Title            string
	Rank             float64
	Snippet          string
	CreatedAt        time.Time
	SessionMatches   int
	ReviewID         string
	MatchTotal       int64
	MatchTotalCapped bool
}

type SessionSearchHighlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type SessionSearchResult struct {
	SessionID      string                   `json:"sessionId"`
	ProjectID      string                   `json:"projectId"`
	Harness        string                   `json:"harness"`
	Kind           SessionSearchKind        `json:"kind"`
	MessageID      string                   `json:"messageId,omitempty"`
	Seq            int64                    `json:"seq"`
	Title          string                   `json:"title,omitempty"`
	Snippet        string                   `json:"snippet"`
	Highlights     []SessionSearchHighlight `json:"highlights,omitempty"`
	Rank           float64                  `json:"rank"`
	CreatedAt      time.Time                `json:"createdAt"`
	SessionName    string                   `json:"sessionName,omitempty"`
	ProjectName    string                   `json:"projectName,omitempty"`
	SessionState   string                   `json:"sessionState,omitempty"`
	ResumeCommand  string                   `json:"resumeCommand,omitempty"`
	SessionMatches int                      `json:"sessionMatches,omitempty"`
	ReviewID       string                   `json:"reviewId,omitempty"`
}

type SessionSearchCoverage struct {
	State              string    `json:"state"`
	IndexedSources     int64     `json:"indexedSources"`
	PendingSources     int64     `json:"pendingSources"`
	UnsupportedSources int64     `json:"unsupportedSources"`
	UnavailableSources int64     `json:"unavailableSources"`
	IndexedDocs        int64     `json:"indexedDocs"`
	UnindexedHarnesses []string  `json:"unindexedHarnesses,omitempty"`
	BackfilledAt       time.Time `json:"backfilledAt,omitzero"`
}

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

const SessionSearchMaxLimit = 100

func NormalizeSessionSearchLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > SessionSearchMaxLimit {
		return SessionSearchMaxLimit
	}
	return limit
}

type SessionSearchPRRef struct {
	URL string
}

type SessionSearchParsedQuery struct {
	Text             string
	MatchQuery       string
	ExactQuery       string
	HighlightTerms   []SessionSearchHighlightTerm
	ExcludeQuery     string
	Project          string
	Harness          string
	Path             string
	Status           SessionSearchStatusFilter
	Sort             SessionSearchSort
	From             *time.Time
	To               *time.Time
	PRRef            *SessionSearchPRRef
	HasOr            bool
	InvalidOperators []string
}

var SessionSearchOperatorPrefixes = []string{
	"harness:", "project:", "path:", "status:", "before:", "after:", "sort:",
	"created:",
}

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
				if !applyValueOperator(&parsed, name, value) {
					parsed.InvalidOperators = append(parsed.InvalidOperators, name+":"+value)
					textParts = append(textParts, token)
					continue
				}
			case "created", "date":
				from, to, ok := parseQueryDateRange(value)
				if !ok {
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
	default:
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

func SessionSearchPathMatch(fragment string) string {
	parts := strings.FieldsFunc(strings.ToLower(fragment), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(parts) == 0 {
		return ""
	}
	return `paths : "` + strings.Join(parts, " ") + `" *`
}

func parsePRURL(token string) (SessionSearchPRRef, bool) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(token), "/")
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return SessionSearchPRRef{}, false
	}
	if end := strings.IndexAny(trimmed, "?#"); end >= 0 {
		trimmed = trimmed[:end]
	}
	parts := strings.Split(trimmed, "/")
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
		add(&required, phrase, !strings.ContainsAny(phrase, " \t"))
	}
	for _, anchor := range prefixes {
		add(&required, anchor, true)
	}

	ranked := rankedWords(words)
	optional := make([]string, 0, len(ranked))
	asTyped := make([]string, 0, len(ranked))
	typedSeen := map[string]struct{}{}
	widened := false
	for index, word := range ranked {
		prefix := index == len(ranked)-1
		if tinyWord(word) {
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
		return strings.Join(append(required, optional...), " AND "), exact, highlights
	}
	anyWord := "(" + strings.Join(optional, " OR ") + ")"
	if len(required) > 0 {
		return strings.Join(required, " AND ") + " AND " + anyWord, exact, highlights
	}
	return anyWord, exact, highlights
}

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

const sessionSearchMinStemRoot = 4

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

const maxRankedWords = 24

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

func isSessionSearchStopword(word string) bool {
	lower := strings.ToLower(strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
	if head, _, found := strings.Cut(strings.ReplaceAll(lower, "’", "'"), "'"); found {
		lower = head
	}
	_, stop := sessionSearchStopwords[lower]
	return stop
}

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

func quoteFTS(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
}

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
			builder.WriteString("\n" + SessionSearchExpansionMarker)
		}
		builder.WriteByte(' ')
		builder.WriteString(strings.Join(parts, " "))
		added++
	}
	return builder.String()
}

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
			boundary = true
		case upper(current) && upper(previous) && index+1 < len(runes) && lower(runes[index+1]):
			boundary = true
		case digit(current) && !digit(previous) && upper(previous):
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
	if len(parts) < 2 {
		return nil
	}
	return parts
}

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
	unique := parts[:0]
	for _, part := range parts {
		if len(unique) > 0 && unique[len(unique)-1] == part {
			continue
		}
		unique = append(unique, part)
	}
	return unique
}

func SessionSearchResumeCommand(sessionID, state string) string {
	if sessionID == "" {
		return ""
	}
	if state == "terminated" {
		return "ao session restore " + sessionID
	}
	return "ao session resume-agent " + sessionID
}

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
