package sessionsearch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// The search benchmark measures ranking quality with standard retrieval metrics
// and fails when the shipped ranking regresses below its recorded baseline.
//
// Three suites:
//
//   - agentic: hand-written AO sessions with commands, error output, identifiers
//     and touched paths, queried the way people and agents search for them.
//     It needs no download, so it runs in every `go test`.
//   - longmemeval: LongMemEval-S (Wu et al., ICLR 2025), 470 answerable
//     questions, each over its own haystack of ~48 user/assistant sessions.
//     https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned
//   - locomo: LoCoMo (Maharana et al., ACL 2024), 10 long two-person
//     conversations, 1,536 questions with turn-level evidence (category 5,
//     adversarial, has none and is excluded as in every published number).
//     https://github.com/snap-research/locomo
//
// The public suites are retrieval-over-past-sessions tasks with published BM25
// numbers to compare against. They are large and not vendored, so they skip
// unless pointed at a local copy. scripts/search-benchmark.sh downloads,
// verifies and runs them:
//
//	scripts/search-benchmark.sh            # quick: agentic + locomo + 100 longmemeval
//	scripts/search-benchmark.sh full       # every question
//	scripts/search-benchmark.sh ablate     # every variant, side by side
//	scripts/search-benchmark.sh --update   # record the current numbers as the baseline
//
// Everything runs through the shipped path: sessions are indexed by
// Service.IndexChat into a real migrated SQLite store, queries are parsed by
// domain.ParseSessionSearchQueryWith and answered by Service.Search. Each corpus
// is indexed once and every variant queries it, so an ablation costs about as
// much as a single run.

// benchVariant is one ranking configuration. "shipped" is the zero value: the
// options exist so the alternatives are measured through the real code.
type benchVariant struct {
	name    string
	match   domain.SessionSearchMatchOptions
	ranking domain.SessionSearchRanking
}

func userWeight(weight float64) map[domain.SessionSearchKind]float64 {
	weights := make(map[domain.SessionSearchKind]float64, len(domain.DefaultSessionSearchKindWeights))
	for kind, value := range domain.DefaultSessionSearchKindWeights {
		weights[kind] = value
	}
	weights[domain.SessionSearchKindUser] = weight
	return weights
}

var benchVariants = []benchVariant{
	{name: "shipped"},
	// The ranking before the benchmark existed: every word required, a hard
	// title > user > path > reply > tool tier, bm25 only inside a tier.
	{name: "v1-strict-and-tiers", match: domain.SessionSearchMatchOptions{RequireAllWords: true, NoStemming: true},
		ranking: domain.SessionSearchRanking{KindTierFirst: true, AllTermsTier: true, CombMax: true}},
	// The first OR ranking: no stemming, all-terms first, CombMAX sessions.
	{name: "v2-or-all-terms-first", match: domain.SessionSearchMatchOptions{NoStemming: true},
		ranking: domain.SessionSearchRanking{AllTermsTier: true, CombMax: true}},
	{name: "strict-and", match: domain.SessionSearchMatchOptions{RequireAllWords: true}},
	{name: "no-stemming", match: domain.SessionSearchMatchOptions{NoStemming: true}},
	{name: "all-terms-first", ranking: domain.SessionSearchRanking{AllTermsTier: true}},
	{name: "kind-tier-first", ranking: domain.SessionSearchRanking{KindTierFirst: true}},
	{name: "flat-kind-weights", ranking: domain.SessionSearchRanking{KindWeights: map[domain.SessionSearchKind]float64{
		domain.SessionSearchKindTitle: 1, domain.SessionSearchKindUser: 1, domain.SessionSearchKindPath: 1,
		domain.SessionSearchKindAssistant: 1, domain.SessionSearchKindTool: 1, domain.SessionSearchKindCommand: 1,
	}}},
	{name: "user-weight-1.5", ranking: domain.SessionSearchRanking{KindWeights: userWeight(1.5)}},
	{name: "session-combmax", ranking: domain.SessionSearchRanking{CombMax: true}},
	{name: "session-combsum", ranking: domain.SessionSearchRanking{SessionTailWeight: 1}},
	// How many of a session's other matches its tail adds up (shipped: 5).
	{name: "tail-depth-1", ranking: domain.SessionSearchRanking{SessionTailDepth: 1}},
	{name: "tail-depth-3", ranking: domain.SessionSearchRanking{SessionTailDepth: 3}},
	{name: "tail-depth-10", ranking: domain.SessionSearchRanking{SessionTailDepth: 10}},
	{name: "tail-unbounded", ranking: domain.SessionSearchRanking{SessionTailDepth: -1}},
	{name: "prefix-all", match: domain.SessionSearchMatchOptions{Prefix: domain.SessionSearchPrefixAll}},
	{name: "prefix-none", match: domain.SessionSearchMatchOptions{Prefix: domain.SessionSearchPrefixNone}},
	{name: "keep-stopwords", match: domain.SessionSearchMatchOptions{KeepStopwords: true}},
}

// selectedVariants is "shipped" unless AO_SEARCH_BENCH_VARIANTS names others
// ("all", or a comma-separated list).
func selectedVariants(t *testing.T) []benchVariant {
	raw := strings.TrimSpace(os.Getenv("AO_SEARCH_BENCH_VARIANTS"))
	if raw == "" {
		return benchVariants[:1]
	}
	if raw == "all" {
		return benchVariants
	}
	var selected []benchVariant
	for _, name := range strings.Split(raw, ",") {
		found := false
		for _, variant := range benchVariants {
			if variant.name == strings.TrimSpace(name) {
				selected = append(selected, variant)
				found = true
			}
		}
		if !found {
			t.Fatalf("unknown variant %q", name)
		}
	}
	return selected
}

// benchQuestion is one query with its graded targets.
type benchQuestion struct {
	category string
	text     string
	sessions map[string]bool // sessions that hold the evidence
	turns    map[string]bool // turns (message ids) that hold the evidence
}

// benchMetrics accumulates the standard retrieval metrics: recall@k (a question
// is a hit if any relevant item is in the top k, i.e. recall_any as LongMemEval
// reports it), nDCG@10 with binary relevance, and MRR. Session metrics read the
// grouped result list, one row per session, as the product shows it; turn
// metrics read the per-message list.
type benchMetrics struct {
	N             int     `json:"n"`
	SessionR1     float64 `json:"session_r1"`
	SessionR5     float64 `json:"session_r5"`
	SessionR10    float64 `json:"session_r10"`
	SessionNDCG10 float64 `json:"session_ndcg10"`
	SessionMRR    float64 `json:"session_mrr"`
	TurnN         int     `json:"turn_n"`
	TurnR5        float64 `json:"turn_r5"`
	TurnR10       float64 `json:"turn_r10"`
	TurnMRR       float64 `json:"turn_mrr"`
	Empty         float64 `json:"empty"`

	categories map[string]*benchMetrics
	finished   bool
}

func (m *benchMetrics) add(q benchQuestion, sessions, turns []string) {
	m.addOne(q, sessions, turns)
	if m.categories == nil {
		m.categories = map[string]*benchMetrics{}
	}
	if q.category == "" {
		return
	}
	sub := m.categories[q.category]
	if sub == nil {
		sub = &benchMetrics{}
		m.categories[q.category] = sub
	}
	sub.addOne(q, sessions, turns)
}

func (m *benchMetrics) addOne(q benchQuestion, sessions, turns []string) {
	m.N++
	if len(sessions) == 0 {
		m.Empty++
	}
	first, dcg := -1, 0.0
	for index, id := range sessions {
		if !q.sessions[id] {
			continue
		}
		if first < 0 {
			first = index
		}
		if index < 10 {
			dcg += 1 / math.Log2(float64(index)+2)
		}
	}
	ideal := 0.0
	for index := 0; index < min(len(q.sessions), 10); index++ {
		ideal += 1 / math.Log2(float64(index)+2)
	}
	if ideal > 0 {
		m.SessionNDCG10 += dcg / ideal
	}
	if first >= 0 {
		m.SessionMRR += 1 / float64(first+1)
		m.SessionR1 += boolScore(first < 1)
		m.SessionR5 += boolScore(first < 5)
		m.SessionR10 += boolScore(first < 10)
	}
	if len(q.turns) == 0 {
		return
	}
	m.TurnN++
	for index, id := range turns {
		if q.turns[id] {
			m.TurnMRR += 1 / float64(index+1)
			m.TurnR5 += boolScore(index < 5)
			m.TurnR10 += boolScore(index < 10)
			break
		}
	}
}

func boolScore(ok bool) float64 {
	if ok {
		return 1
	}
	return 0
}

// finish turns sums into percentages (and MRR into a mean), rounded so the
// baseline file diffs cleanly.
func (m *benchMetrics) finish() {
	if m.finished {
		return
	}
	m.finished = true
	pct := func(value float64, n int) float64 {
		if n == 0 {
			return 0
		}
		return math.Round(1000*value/float64(n)) / 10
	}
	mean := func(value float64, n int) float64 {
		if n == 0 {
			return 0
		}
		return math.Round(1000*value/float64(n)) / 1000
	}
	m.SessionR1, m.SessionR5, m.SessionR10 = pct(m.SessionR1, m.N), pct(m.SessionR5, m.N), pct(m.SessionR10, m.N)
	m.SessionNDCG10, m.SessionMRR = pct(m.SessionNDCG10, m.N), mean(m.SessionMRR, m.N)
	m.TurnR5, m.TurnR10, m.TurnMRR = pct(m.TurnR5, m.TurnN), pct(m.TurnR10, m.TurnN), mean(m.TurnMRR, m.TurnN)
	m.Empty = pct(m.Empty, m.N)
	for _, sub := range m.categories {
		sub.finish()
	}
}

func (m *benchMetrics) line() string {
	turn := "turn       n/a"
	if m.TurnN > 0 {
		turn = fmt.Sprintf("turn R@5 %5.1f R@10 %5.1f MRR %.3f", m.TurnR5, m.TurnR10, m.TurnMRR)
	}
	return fmt.Sprintf("n=%-4d session R@1 %5.1f R@5 %5.1f R@10 %5.1f nDCG@10 %5.1f MRR %.3f | %s | empty %4.1f",
		m.N, m.SessionR1, m.SessionR5, m.SessionR10, m.SessionNDCG10, m.SessionMRR, turn, m.Empty)
}

// benchRun is one suite's results for every selected variant.
type benchRun struct {
	suite    string
	variants []benchVariant
	metrics  map[string]*benchMetrics
}

func newBenchRun(suite string, variants []benchVariant) *benchRun {
	run := &benchRun{suite: suite, variants: variants, metrics: map[string]*benchMetrics{}}
	for _, variant := range variants {
		run.metrics[variant.name] = &benchMetrics{}
	}
	return run
}

// ask runs one question through every variant. prefix strips the per-corpus
// session-id prefix so results compare against the dataset's own ids.
func (r *benchRun) ask(t *testing.T, svc *sessionsearch.Service, prefix string, q benchQuestion, params domain.SessionSearchParams) {
	for _, variant := range r.variants {
		parsed := domain.ParseSessionSearchQueryWith(q.text, variant.match)
		grouped := params
		grouped.Limit, grouped.Ranking, grouped.Group = domain.SessionSearchMaxLimit, variant.ranking, domain.SessionSearchGroupSession
		grouped.SkipSnippets = true
		sessionResponse, err := svc.Search(context.Background(), parsed, grouped)
		if err != nil {
			t.Fatalf("[%s] search %q: %v", variant.name, q.text, err)
		}
		sessions := make([]string, 0, len(sessionResponse.Results))
		for _, result := range sessionResponse.Results {
			sessions = append(sessions, strings.TrimPrefix(result.SessionID, prefix))
		}
		var turns []string
		if len(q.turns) > 0 {
			perMessage := grouped
			perMessage.Group = domain.SessionSearchGroupMessage
			messageResponse, err := svc.Search(context.Background(), parsed, perMessage)
			if err != nil {
				t.Fatalf("[%s] search %q: %v", variant.name, q.text, err)
			}
			for _, result := range messageResponse.Results {
				if result.MessageID != "" {
					turns = append(turns, result.MessageID)
				}
			}
		}
		r.metrics[variant.name].add(q, sessions, turns)
	}
}

// report logs every variant, then checks the shipped variant against the
// baseline (or records it when AO_SEARCH_BENCH_UPDATE=1).
func (r *benchRun) report(t *testing.T, key string) {
	for _, variant := range r.variants {
		m := r.metrics[variant.name]
		m.finish()
		t.Logf("%-12s %-20s ALL %-26s %s", r.suite, variant.name, "", m.line())
		if variant.name != "shipped" && len(r.variants) > 1 {
			continue
		}
		categories := make([]string, 0, len(m.categories))
		for category := range m.categories {
			categories = append(categories, category)
		}
		sort.Strings(categories)
		for _, category := range categories {
			t.Logf("%-12s %-20s  .  %-26s %s", r.suite, variant.name, category, m.categories[category].line())
		}
	}
	if shipped, ok := r.metrics["shipped"]; ok {
		checkBaseline(t, key, shipped)
	}
}

// The baseline is committed next to this file. A metric may not fall more than
// its tolerance below the recorded value; improvements pass and are recorded by
// re-running with AO_SEARCH_BENCH_UPDATE=1.
const benchBaselinePath = "testdata/search_benchmark_baseline.json"

type benchBaseline struct {
	Note   string                   `json:"note"`
	Suites map[string]*benchMetrics `json:"suites"`
}

func checkBaseline(t *testing.T, key string, current *benchMetrics) {
	t.Helper()
	raw, err := os.ReadFile(benchBaselinePath)
	baseline := benchBaseline{Suites: map[string]*benchMetrics{}}
	if err == nil {
		if err := json.Unmarshal(raw, &baseline); err != nil {
			t.Fatalf("read %s: %v", benchBaselinePath, err)
		}
	}
	if os.Getenv("AO_SEARCH_BENCH_UPDATE") == "1" {
		baseline.Note = "Recorded by the search benchmark (search_benchmark_test.go). " +
			"Regenerate with scripts/search-benchmark.sh --update after an intentional ranking change."
		if baseline.Suites == nil {
			baseline.Suites = map[string]*benchMetrics{}
		}
		baseline.Suites[key] = current
		encoded, err := json.MarshalIndent(baseline, "", "  ")
		if err != nil {
			t.Fatalf("encode baseline: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(benchBaselinePath), 0o755); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(benchBaselinePath, append(encoded, '\n'), 0o644); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		t.Logf("recorded baseline %s", key)
		return
	}
	want, ok := baseline.Suites[key]
	if !ok {
		t.Fatalf("no baseline for %s; record one with AO_SEARCH_BENCH_UPDATE=1", key)
	}
	if want.N != current.N {
		t.Fatalf("%s: baseline has n=%d, this run n=%d; the dataset or filter changed", key, want.N, current.N)
	}
	// Recall and nDCG are percentages; half a point is one LoCoMo question in
	// 300 and leaves room for nothing but noise. MRR moves on a 0-1 scale.
	checks := []struct {
		name          string
		got, baseline float64
		tolerance     float64
	}{
		{"session R@1", current.SessionR1, want.SessionR1, 0.5},
		{"session R@5", current.SessionR5, want.SessionR5, 0.5},
		{"session R@10", current.SessionR10, want.SessionR10, 0.5},
		{"session nDCG@10", current.SessionNDCG10, want.SessionNDCG10, 0.5},
		{"session MRR", current.SessionMRR, want.SessionMRR, 0.005},
		{"turn R@5", current.TurnR5, want.TurnR5, 0.5},
		{"turn R@10", current.TurnR10, want.TurnR10, 0.5},
		{"turn MRR", current.TurnMRR, want.TurnMRR, 0.005},
	}
	for _, check := range checks {
		if check.got < check.baseline-check.tolerance {
			t.Errorf("%s regressed: %s %.3f, baseline %.3f (tolerance %.3f)",
				key, check.name, check.got, check.baseline, check.tolerance)
		}
	}
	if current.Empty > want.Empty+0.5 {
		t.Errorf("%s regressed: %.1f%% of queries returned nothing, baseline %.1f%%", key, current.Empty, want.Empty)
	}
}

// ---------------------------------------------------------------------------
// Public suites: a corpus is loaded per question (LongMemEval) or per
// conversation (LoCoMo) into one store that is cleared between corpora, so
// bm25's collection statistics are those of that corpus alone, as the
// benchmarks define them.

// corpusStore is the real SQLite store with the Chat timeline served from
// memory. Every index write and every query goes to SQLite unchanged.
type corpusStore struct {
	*sqlite.Store
	messages map[string][]domain.SessionSearchMessage
}

func (s *corpusStore) SessionSearchVisibleMessages(_ context.Context, conversationID string) ([]domain.SessionSearchMessage, error) {
	return s.messages[conversationID], nil
}

func (s *corpusStore) SessionSearchActivities(context.Context, string) ([]domain.SessionSearchActivity, error) {
	return nil, nil
}

func (s *corpusStore) SessionSearchTouchedPaths(context.Context, string) ([]string, error) {
	return nil, nil
}

type corpusSessionText struct {
	id    string
	at    time.Time
	turns []corpusTurn
}

type corpusTurn struct {
	id   string
	user bool
	text string
}

type corpusLoader struct {
	store   *corpusStore
	svc     *sessionsearch.Service
	indexed []string
	run     int
}

func newCorpusLoader(t *testing.T) *corpusLoader {
	store := &corpusStore{Store: sqlitetest.MustOpen(t), messages: map[string][]domain.SessionSearchMessage{}}
	return &corpusLoader{store: store, svc: sessionsearch.New(sessionsearch.Options{
		Store: store, Machine: "bench", Now: func() time.Time { return testNow },
	})}
}

// load replaces the index with one corpus and returns the session-id prefix.
// Ids are made unique per corpus because LongMemEval haystacks share sessions,
// and an unchanged fingerprint would (correctly) skip re-indexing them.
func (l *corpusLoader) load(t *testing.T, sessions []corpusSessionText) string {
	ctx := context.Background()
	for _, key := range l.indexed {
		if err := l.store.DeleteSessionSearchDocsForSource(ctx, key); err != nil {
			t.Fatalf("clear %s: %v", key, err)
		}
	}
	l.indexed = l.indexed[:0]
	clear(l.store.messages)
	l.run++
	prefix := fmt.Sprintf("c%d~", l.run)
	for _, session := range sessions {
		sessionID := prefix + session.id
		messages := make([]domain.SessionSearchMessage, 0, len(session.turns))
		for index, turn := range session.turns {
			role := domain.MessageRoleAssistant
			if turn.user {
				role = domain.MessageRoleUser
			}
			messages = append(messages, domain.SessionSearchMessage{
				ID: turn.id, Sequence: int64(index + 1), Role: role, Text: turn.text,
				CreatedAt: session.at.Add(time.Duration(index) * time.Second),
			})
		}
		l.store.messages[sessionID] = messages
		if _, err := l.svc.IndexChat(ctx, domain.SessionSearchConversation{
			ID: sessionID, SessionID: domain.SessionID(sessionID), ProjectID: "bench", Harness: domain.HarnessClaudeCode,
		}); err != nil {
			t.Fatalf("index %s: %v", session.id, err)
		}
		l.indexed = append(l.indexed, sessionsearch.ChatSourceKey(sessionID))
	}
	return prefix
}

func benchDataPath(t *testing.T, env, name string) string {
	path := os.Getenv(env)
	if path == "" {
		t.Skipf("set %s to a local %s (scripts/search-benchmark.sh fetches it)", env, name)
	}
	return path
}

type longMemEvalItem struct {
	QuestionID         string   `json:"question_id"`
	QuestionType       string   `json:"question_type"`
	Question           string   `json:"question"`
	HaystackDates      []string `json:"haystack_dates"`
	HaystackSessionIDs []string `json:"haystack_session_ids"`
	HaystackSessions   [][]struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		HasAnswer bool   `json:"has_answer"`
	} `json:"haystack_sessions"`
	AnswerSessionIDs []string `json:"answer_session_ids"`
}

// longMemEvalQuickQuestions is the quick profile's sample: the first questions
// in file order, which span both of the dataset's leading question types.
const longMemEvalQuickQuestions = 100

func TestSearchBenchmarkLongMemEval(t *testing.T) {
	path := benchDataPath(t, "AO_SEARCH_BENCH_LONGMEMEVAL", "longmemeval_s_cleaned.json")
	full := os.Getenv("AO_SEARCH_BENCH_PROFILE") == "full"
	limit, key := longMemEvalQuickQuestions, "longmemeval-s/quick"
	if full {
		limit, key = math.MaxInt, "longmemeval-s/full"
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open dataset: %v", err)
	}
	defer func() { _ = file.Close() }()
	// Stream one question at a time: the file is 264 MB and this has to run on
	// an 8 GB machine shared with other work.
	decoder := json.NewDecoder(file)
	if _, err := decoder.Token(); err != nil {
		t.Fatalf("read dataset start: %v", err)
	}
	loader := newCorpusLoader(t)
	run := newBenchRun("longmemeval", selectedVariants(t))
	asked, started := 0, time.Now()
	for decoder.More() && asked < limit {
		var item longMemEvalItem
		if err := decoder.Decode(&item); err != nil {
			t.Fatalf("decode question: %v", err)
		}
		// Abstention questions have no answer session by design; every published
		// retrieval number excludes them.
		if strings.HasSuffix(item.QuestionID, "_abs") || len(item.AnswerSessionIDs) == 0 {
			continue
		}
		question := benchQuestion{category: item.QuestionType, text: item.Question,
			sessions: map[string]bool{}, turns: map[string]bool{}}
		for _, id := range item.AnswerSessionIDs {
			question.sessions[id] = true
		}
		sessions := make([]corpusSessionText, 0, len(item.HaystackSessions))
		for index, turns := range item.HaystackSessions {
			session := corpusSessionText{id: item.HaystackSessionIDs[index], at: longMemEvalDate(item.HaystackDates, index)}
			for turnIndex, turn := range turns {
				id := fmt.Sprintf("%s#%d", session.id, turnIndex)
				if turn.HasAnswer {
					question.turns[id] = true
				}
				session.turns = append(session.turns, corpusTurn{id: id, user: turn.Role == "user", text: turn.Content})
			}
			sessions = append(sessions, session)
		}
		prefix := loader.load(t, sessions)
		run.ask(t, loader.svc, prefix, question, domain.SessionSearchParams{})
		asked++
		if asked%50 == 0 {
			t.Logf("longmemeval: %d questions, %s", asked, time.Since(started).Round(time.Second))
		}
	}
	t.Logf("longmemeval: %d questions in %s", asked, time.Since(started).Round(time.Second))
	run.report(t, key)
}

func longMemEvalDate(dates []string, index int) time.Time {
	if index < len(dates) {
		// "2023/05/20 (Sat) 02:21"
		if at, err := time.Parse("2006/01/02 (Mon) 15:04", dates[index]); err == nil {
			return at.UTC()
		}
	}
	return testNow.Add(-time.Duration(index) * time.Hour)
}

type locomoSample struct {
	QA []struct {
		Question string          `json:"question"`
		Evidence []string        `json:"evidence"`
		Category json.RawMessage `json:"category"`
	} `json:"qa"`
	Conversation map[string]json.RawMessage `json:"conversation"`
}

// locomoCategories are the paper's category numbers.
var locomoCategories = map[string]string{
	"1": "multi-hop", "2": "temporal", "3": "open-domain", "4": "single-hop", "5": "adversarial",
}

func TestSearchBenchmarkLoCoMo(t *testing.T) {
	path := benchDataPath(t, "AO_SEARCH_BENCH_LOCOMO", "locomo10.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dataset: %v", err)
	}
	var samples []locomoSample
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatalf("decode dataset: %v", err)
	}
	loader := newCorpusLoader(t)
	run := newBenchRun("locomo", selectedVariants(t))
	started := time.Now()
	for sampleIndex, sample := range samples {
		var speakerA string
		_ = json.Unmarshal(sample.Conversation["speaker_a"], &speakerA)
		sessions := make([]corpusSessionText, 0, 32)
		for number := 1; ; number++ {
			key := fmt.Sprintf("session_%d", number)
			body, ok := sample.Conversation[key]
			if !ok {
				break
			}
			var turns []struct {
				Speaker string `json:"speaker"`
				DiaID   string `json:"dia_id"`
				Text    string `json:"text"`
			}
			if err := json.Unmarshal(body, &turns); err != nil {
				t.Fatalf("decode %s: %v", key, err)
			}
			session := corpusSessionText{id: fmt.Sprintf("D%d", number), at: testNow.Add(time.Duration(number-200) * 24 * time.Hour)}
			for _, turn := range turns {
				// Two people talk; speaker A stands in for the user so both message
				// kinds are exercised. The mapping is arbitrary, which is why
				// LoCoMo penalizes any kind weighting that is too strong.
				session.turns = append(session.turns, corpusTurn{id: turn.DiaID, user: turn.Speaker == speakerA, text: turn.Text})
			}
			sessions = append(sessions, session)
		}
		prefix := loader.load(t, sessions)
		for _, qa := range sample.QA {
			category := locomoCategories[strings.Trim(string(qa.Category), `"`)]
			if category == "adversarial" || len(qa.Evidence) == 0 {
				continue
			}
			question := benchQuestion{category: category, text: qa.Question, sessions: map[string]bool{}, turns: map[string]bool{}}
			for _, evidence := range qa.Evidence {
				// Evidence is "D1:3", and in a few rows "D1:3; D2:5".
				for _, part := range strings.FieldsFunc(evidence, func(r rune) bool { return r == ';' || r == ',' || r == ' ' }) {
					sessionID, _, _ := strings.Cut(part, ":")
					question.sessions[sessionID] = true
					question.turns[part] = true
				}
			}
			run.ask(t, loader.svc, prefix, question, domain.SessionSearchParams{})
		}
		t.Logf("locomo: conversation %d of %d, %s", sampleIndex+1, len(samples), time.Since(started).Round(time.Second))
	}
	run.report(t, "locomo/full")
}

// ---------------------------------------------------------------------------
// Agentic suite: what AO sessions actually contain. The public suites are
// conversations; AO sessions are mostly commands, their output, identifiers and
// diffs, searched by error strings, symbol names, paths, half-typed words and
// natural-language questions from agents. Each query is graded by the sessions
// that answer it. Distractor sessions share vocabulary on purpose (two login
// sessions, two rate-limit sessions), so a query has to be ranked, not merely
// matched.

type agenticCommand struct{ command, output string }

type agenticTurn struct {
	user, assistant string
	commands        []agenticCommand
	files           []string
}

type agenticSession struct {
	name, title string
	turns       []agenticTurn
}

var agenticSessions = []agenticSession{
	{name: "flaky-login", title: "Fix the flaky login e2e test", turns: []agenticTurn{{
		user:      "the login e2e test is flaky on CI, it fails about 1 in 5 runs",
		assistant: "The race is in waitForSession: it resolves before the auth cookie is written. I await the token refresh before asserting.",
		commands: []agenticCommand{{"npx playwright test tests/login.e2e.ts --repeat-each 20",
			"FAIL tests/login.e2e.ts › signs in\n  Error: expect(received).toBe(expected)\n  Timeout 5000ms exceeded while waiting for session"}},
		files: []string{"tests/login.e2e.ts", "src/auth/waitForSession.ts"},
	}}},
	{name: "login-copy", title: "Login button copy", turns: []agenticTurn{{
		user:      "change the login button text to Sign in and update the snapshot",
		assistant: "Renamed the label in LoginButton.tsx and refreshed the snapshot test.",
		commands:  []agenticCommand{{"npm test -- -u LoginButton", "PASS src/components/LoginButton.test.tsx\n1 snapshot updated"}},
		files:     []string{"src/components/LoginButton.tsx"},
	}}},
	{name: "webhook-signature", title: "Stripe webhook verification", turns: []agenticTurn{{
		user:      "verify the stripe webhook signature before we parse the payload",
		assistant: "Added HMAC verification with crypto.timingSafeEqual so the comparison is constant-time, and reject events older than five minutes.",
		commands:  []agenticCommand{{"npm test -- webhook", "PASS src/webhooks/stripe.test.ts (4 tests)"}},
		files:     []string{"src/webhooks/stripe.ts"},
	}}},
	{name: "sqlite-busy", title: "Database locked errors under load", turns: []agenticTurn{{
		user:      "we keep getting database is locked errors when two workers write at once",
		assistant: "SQLite returns SQLITE_BUSY when a second writer arrives. I set PRAGMA busy_timeout=5000 and routed writes through one connection.",
		commands: []agenticCommand{{"go test ./internal/storage/sqlite/...",
			"--- FAIL: TestConcurrentWrites (0.31s)\n    store_test.go:88: database is locked (5) (SQLITE_BUSY)"}},
		files: []string{"internal/storage/sqlite/db.go"},
	}}},
	{name: "docker-oom", title: "Worker container keeps dying", turns: []agenticTurn{{
		user:      "the worker container restarts every few minutes in staging",
		assistant: "It is being OOMKilled: exit code 137 under the 512m limit while building the search index. I raised mem_limit to 2g in docker-compose.yml.",
		commands:  []agenticCommand{{"docker inspect worker --format '{{.State.OOMKilled}} {{.State.ExitCode}}'", "true 137"}},
		files:     []string{"deploy/docker-compose.yml"},
	}}},
	{name: "electron-upgrade", title: "Upgrade Electron 33 to 44", turns: []agenticTurn{{
		user:      "upgrade electron from 33 to 44 and keep the mac build signed",
		assistant: "Bumped electron to 44, enabled the fuses, and fixed notarization by passing the team id to electron-builder.",
		commands: []agenticCommand{{"npm run build:mac",
			"• signing file=dist/mac/Orchestrator.app\n• notarizing ... notarization successful"}},
		files: []string{"frontend/package.json", "frontend/electron-builder.yml"},
	}}},
	{name: "github-backoff", title: "GitHub API secondary rate limit", turns: []agenticTurn{{
		user:      "the PR poller gets 403 secondary rate limit responses from GitHub",
		assistant: "Wrapped the client in retryWithBackoff: exponential backoff with full jitter, and it honours the Retry-After header.",
		commands: []agenticCommand{{"go test ./internal/github/ -run Backoff",
			"ok  github.com/aoagents/agent-orchestrator/internal/github 0.42s"}},
		files: []string{"internal/github/retryWithBackoff.go"},
	}}},
	{name: "rate-limit-banner", title: "Show a usage limit banner", turns: []agenticTurn{{
		user:      "when the model usage limit is reached show a banner with the reset time",
		assistant: "Added UsageLimitBanner that reads the reset time from the account limits endpoint.",
		files:     []string{"src/components/UsageLimitBanner.tsx"},
	}}},
	{name: "i18n-plurals", title: "Missing German plural forms", turns: []agenticTurn{{
		user:      "the German translation shows 1 Sitzungen instead of 1 Sitzung",
		assistant: "de.json had only the _other key. Added the _one plural forms that i18next expects for every count string.",
		files:     []string{"frontend/src/renderer/i18n/de.json"},
	}}},
	{name: "go-race", title: "Data race in session manager", turns: []agenticTurn{{
		user:      "go test -race fails intermittently in the session manager",
		assistant: "Close and Spawn both touched the sessions map. sessionManager now guards it with a sync.RWMutex.",
		commands: []agenticCommand{{"go test -race ./internal/session/...",
			"WARNING: DATA RACE\nWrite at 0x00c000182030 by goroutine 42:\n  sessionManager.Close()"}},
		files: []string{"internal/session/manager.go"},
	}}},
	{name: "chat-scroll", title: "Chat scroll jitter", turns: []agenticTurn{{
		user:      "the chat jumps when you scroll up while the agent is streaming",
		assistant: "The browser's scroll anchoring fought our snap-to-bottom. Setting overflow-anchor: none on the timeline fixed the jitter.",
		files:     []string{"frontend/src/renderer/components/chat/Timeline.css"},
	}}},
	{name: "goose-migration", title: "Migration fails on upgrade", turns: []agenticTurn{{
		user:      "upgrading from 2.3 the daemon fails to start with a migration error",
		assistant: "The goose migration re-added hibernated_at on databases that already had it. The migration is now idempotent.",
		commands: []agenticCommand{{"ao daemon start",
			"migrate: goose up: ERROR 20260901_hibernation.sql: duplicate column name: hibernated_at"}},
		files: []string{"internal/storage/sqlite/migrations/20260901_hibernation.sql"},
	}}},
	{name: "ingress-502", title: "Intermittent 502 from the API", turns: []agenticTurn{{
		user:      "long requests to the API return 502 Bad Gateway after 60 seconds",
		assistant: "The nginx ingress proxy-read-timeout defaulted to 60s. Raised it to 300 with an annotation on the api ingress.",
		files:     []string{"deploy/k8s/api-ingress.yaml"},
	}}},
	{name: "python-yaml", title: "Eval script import error", turns: []agenticTurn{{
		user:      "the eval script crashes on import",
		assistant: "The venv was missing PyYAML. Added it to requirements.txt and reinstalled.",
		commands: []agenticCommand{{"python scripts/eval.py",
			"Traceback (most recent call last):\nModuleNotFoundError: No module named 'yaml'"}},
		files: []string{"scripts/requirements.txt"},
	}}},
	// Distractors: each shares vocabulary with a session above, so the queries
	// that target either one have to be ranked rather than merely matched.
	{name: "sqlite-fts", title: "Full-text search over messages", turns: []agenticTurn{{
		user:      "add full text search over chat messages using sqlite",
		assistant: "Created an FTS5 virtual table with external content and triggers to keep it in sync with the messages table.",
		files:     []string{"internal/storage/sqlite/migrations/20261001_fts.sql"},
	}}},
	{name: "docker-build-cache", title: "Slow docker builds", turns: []agenticTurn{{
		user:      "docker build takes eight minutes on every push",
		assistant: "Reordered the Dockerfile so dependency layers are cached and added a BuildKit cache mount for the Go module cache.",
		commands:  []agenticCommand{{"docker build -t worker .", "[+] Building 41.2s (14/14) FINISHED\n => CACHED [3/9] RUN go mod download"}},
		files:     []string{"Dockerfile"},
	}}},
	{name: "login-sso", title: "Google sign-in", turns: []agenticTurn{{
		user:      "add Google SSO to the login page",
		assistant: "Added the OAuth callback route and a Continue with Google button; tokens are exchanged server-side.",
		files:     []string{"src/auth/oauthCallback.ts", "src/pages/login.tsx"},
	}}},
	{name: "flaky-timezone", title: "Date tests fail after midnight UTC", turns: []agenticTurn{{
		user:      "the formatDate unit tests are flaky, they only fail late in the evening",
		assistant: "The tests used the machine timezone. I pinned TZ=UTC in the test setup so the expected dates are stable.",
		commands:  []agenticCommand{{"TZ=America/Los_Angeles npm test -- formatDate", "FAIL src/lib/formatDate.test.ts\n  Expected: \"Oct 22\"\n  Received: \"Oct 21\""}},
		files:     []string{"src/lib/formatDate.test.ts"},
	}}},
	{name: "electron-crash", title: "App crashes on launch", turns: []agenticTurn{{
		user:      "after updating, the desktop app crashes on startup with a white screen",
		assistant: "The preload bridge was stale: the renderer called window.ao.version, which the new preload no longer exposed. Rebuilt the preload bundle.",
		commands:  []agenticCommand{{"npm run dev", "Error: Cannot read properties of undefined (reading 'version')\n    at preload.js:12"}},
		files:     []string{"frontend/src/preload.ts"},
	}}},
	{name: "migration-rollback", title: "Down migration for sessions", turns: []agenticTurn{{
		user:      "write a down migration so we can roll back the sessions table change",
		assistant: "Added the goose Down section that drops the new columns in reverse order.",
		files:     []string{"internal/storage/sqlite/migrations/20261005_sessions.sql"},
	}}},
	{name: "webhook-retries", title: "Retry failed outbound webhooks", turns: []agenticTurn{{
		user:      "outbound webhooks are dropped when the receiver is down",
		assistant: "Failed deliveries now go to a retry queue with exponential backoff, capped at 24 hours.",
		files:     []string{"internal/webhooks/deliveryQueue.go"},
	}}},
}

// agenticQueries are graded by session name. The category says what kind of
// search the query stands for, so a regression names the kind of search that
// broke.
var agenticQueries = []struct {
	category string
	query    string
	want     []string
}{
	// Questions an agent asks `ao search` in its own words.
	{"natural-language", "how did we fix the flaky login test", []string{"flaky-login"}},
	{"natural-language", "what did we do about the database locked errors", []string{"sqlite-busy"}},
	{"natural-language", "why was the worker container killed", []string{"docker-oom"}},
	{"natural-language", "how do we handle github rate limits", []string{"github-backoff"}},
	{"natural-language", "which session upgraded electron", []string{"electron-upgrade"}},
	{"natural-language", "where did we fix the german plural translations", []string{"i18n-plurals"}},
	// Error strings pasted from a terminal.
	{"error-string", "SQLITE_BUSY", []string{"sqlite-busy"}},
	{"error-string", "exit code 137", []string{"docker-oom"}},
	{"error-string", "DATA RACE", []string{"go-race"}},
	{"error-string", "duplicate column name", []string{"goose-migration"}},
	{"error-string", "502 Bad Gateway", []string{"ingress-502"}},
	{"error-string", "No module named yaml", []string{"python-yaml"}},
	{"error-string", "Timeout 5000ms exceeded", []string{"flaky-login"}},
	// Symbols, whole and by their parts.
	{"identifier", "timingSafeEqual", []string{"webhook-signature"}},
	{"identifier", "retryWithBackoff", []string{"github-backoff"}},
	{"identifier", "busy_timeout", []string{"sqlite-busy"}},
	{"identifier", "waitForSession", []string{"flaky-login"}},
	{"identifier", "sessionManager", []string{"go-race"}},
	{"identifier-part", "safe equal", []string{"webhook-signature"}},
	{"identifier-part", "backoff jitter", []string{"github-backoff"}},
	{"identifier-part", "OOMKilled", []string{"docker-oom"}},
	// Paths and file names.
	{"path", "docker-compose.yml", []string{"docker-oom"}},
	{"path", "de.json", []string{"i18n-plurals"}},
	{"path", "LoginButton.tsx", []string{"login-copy"}},
	// Half-typed words, as in the palette.
	{"typeahead", "notariz", []string{"electron-upgrade"}},
	{"typeahead", "hibernat", []string{"goose-migration"}},
	{"typeahead", "overflow-anch", []string{"chat-scroll"}},
	// Different word forms from the text: the case stemming would help.
	{"word-form", "fixing flaky tests", []string{"flaky-login"}},
	{"word-form", "migrations failing", []string{"goose-migration"}},
	{"word-form", "signatures verified", []string{"webhook-signature"}},
	{"word-form", "speed up docker builds", []string{"docker-build-cache"}},
	// Near-duplicates: the words appear in several sessions, and only one is
	// the session meant.
	{"disambiguation", "flaky test timezone", []string{"flaky-timezone"}},
	{"disambiguation", "electron app crashes on startup", []string{"electron-crash"}},
	{"disambiguation", "google sign in", []string{"login-sso"}},
	{"disambiguation", "outbound webhook retries", []string{"webhook-retries"}},
	{"disambiguation", "full text search sqlite", []string{"sqlite-fts"}},
	{"disambiguation", "roll back migration", []string{"migration-rollback"}},
	{"disambiguation", "constant time comparison", []string{"webhook-signature"}},
	// Several right answers: graded by nDCG, so all of them should rank high.
	{"multi-answer", "login", []string{"flaky-login", "login-copy", "login-sso"}},
	{"multi-answer", "retry backoff", []string{"github-backoff", "webhook-retries"}},
	{"multi-answer", "flaky", []string{"flaky-login", "flaky-timezone"}},
	// Synonyms with no shared word. Lexical search cannot find these; they are
	// here so the limit is measured rather than assumed.
	{"vocabulary-gap", "out of memory", []string{"docker-oom"}},
	{"vocabulary-gap", "concurrency bug", []string{"go-race"}},
}

func TestSearchBenchmarkAgentic(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ids := make(map[string]string, len(agenticSessions))
	for sessionIndex, entry := range agenticSessions {
		conversationID := "conv-" + entry.name
		session := seedChatSession(t, f.store, "bench", entry.name, conversationID)
		ids[string(session.ID)] = entry.name
		record, found, err := f.store.GetSession(context.Background(), session.ID)
		if err != nil || !found {
			t.Fatalf("GetSession %s: found=%v err=%v", entry.name, found, err)
		}
		record.DisplayName = entry.title
		if err := f.store.UpdateSession(context.Background(), record); err != nil {
			t.Fatalf("UpdateSession %s: %v", entry.name, err)
		}
		// UpdateSession no longer writes the title; renames have their own narrow writer.
		if _, err := f.store.RenameSession(context.Background(), record.ID, entry.title, time.Now()); err != nil {
			t.Fatalf("RenameSession %s: %v", entry.name, err)
		}
		for turnIndex, turn := range entry.turns {
			turnID := fmt.Sprintf("turn-%d-%d", sessionIndex, turnIndex)
			appendUserMessage(t, f.store, session, conversationID, "user-"+turnID, turnID, turn.user)
			settleAssistantMessage(t, f.store, conversationID, "assistant-"+turnID, turnID, turn.assistant)
			for commandIndex, command := range turn.commands {
				addCommandActivity(t, f.store, conversationID, turnID, fmt.Sprintf("cmd-%s-%d", turnID, commandIndex),
					command.command, command.output, domain.ActivityStatusCompleted)
			}
			if len(turn.files) > 0 {
				files := make([]string, 0, len(turn.files))
				for _, path := range turn.files {
					files = append(files, fmt.Sprintf(`{"path":%q}`, path))
				}
				addActivity(t, f.store, conversationID, turnID, domain.ConversationActivity{
					ID: "files-" + turnID, Kind: domain.ActivityKindFileChange, Status: domain.ActivityStatusCompleted,
					Detail: []byte(`{"files":[` + strings.Join(files, ",") + `]}`),
				})
			}
		}
	}
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	run := newBenchRun("agentic", selectedVariants(t))
	for _, query := range agenticQueries {
		question := benchQuestion{category: query.category, text: query.query, sessions: map[string]bool{}}
		for _, name := range query.want {
			question.sessions[name] = true
		}
		// Session ids are the fixture names already; map defensively in case the
		// store ever mints its own.
		run.askMapped(t, f.svc, ids, question)
	}
	run.report(t, "agentic")
}

// askMapped is ask for a corpus whose session ids map to fixture names.
func (r *benchRun) askMapped(t *testing.T, svc *sessionsearch.Service, ids map[string]string, q benchQuestion) {
	for _, variant := range r.variants {
		parsed := domain.ParseSessionSearchQueryWith(q.text, variant.match)
		response, err := svc.Search(context.Background(), parsed, domain.SessionSearchParams{
			Limit: domain.SessionSearchMaxLimit, Ranking: variant.ranking, Group: domain.SessionSearchGroupSession,
			SkipSnippets: true,
		})
		if err != nil {
			t.Fatalf("[%s] search %q: %v", variant.name, q.text, err)
		}
		sessions := make([]string, 0, len(response.Results))
		for _, result := range response.Results {
			name, ok := ids[result.SessionID]
			if !ok {
				name = result.SessionID
			}
			sessions = append(sessions, name)
		}
		if variant.name == "shipped" && os.Getenv("AO_SEARCH_BENCH_VERBOSE") == "1" {
			t.Logf("agentic %-16s %-48q -> %v", q.category, q.text, sessions[:min(len(sessions), 3)])
		}
		r.metrics[variant.name].add(q, sessions, nil)
	}
}
