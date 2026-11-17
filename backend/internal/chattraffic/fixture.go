package chattraffic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Fixture is one provider's scripted conversation. Fixtures are reviewed,
// committed files: every path and text in them is dummy content, and their
// provenance field cites the first-party capture each shape follows.
//
// A fixture is a script of normalized provider events, the ports.ChatEvent
// stream a Chat driver hands the daemon. Raw provider wire frames are not
// replayed: the benchmark seam starts after the Codex app-server and ACP
// adapters normalize them.
type Fixture struct {
	SchemaVersion string `json:"schemaVersion"`
	Provider      string `json:"provider"`
	// Harness is the AO agent harness id the session is created with.
	Harness    string `json:"harness"`
	Provenance string `json:"provenance"`
	// ProviderConversationID is the dummy native thread id.
	ProviderConversationID string `json:"providerConversationId"`
	// SettledTurns is how many completed turns precede the reply turn. Settled
	// turn n uses SettledTemplates[(n-1) % len].
	SettledTurns     int            `json:"settledTurns"`
	SettledTemplates []TurnTemplate `json:"settledTemplates"`
	ReplyTurn        TurnTemplate   `json:"replyTurn"`
}

// TurnTemplate is one user message and the provider events that answer it.
// The token {n} in any string, including inside Detail, becomes the turn
// number so every expanded turn carries distinct ids and text. The token
// {streamed} in Text, or as a whole JSON string in Detail, becomes the text the
// same item's deltas streamed so far, so a settled event repeats exactly what
// streamed, as the Codex and ACP adapters do.
type TurnTemplate struct {
	User  string `json:"user"`
	Steps []Step `json:"steps"`
}

// Step is one normalized provider event. Kind is a ports.ChatEventKind value.
type Step struct {
	Kind         string          `json:"kind"`
	Item         string          `json:"item,omitempty"`
	Delta        string          `json:"delta,omitempty"`
	Text         string          `json:"text,omitempty"`
	ActivityKind string          `json:"activityKind,omitempty"`
	Status       string          `json:"status,omitempty"`
	Summary      string          `json:"summary,omitempty"`
	Detail       json.RawMessage `json:"detail,omitempty"`
	TurnState    string          `json:"turnState,omitempty"`
	// Repeat replays a delta step this many times, with {r} in Delta replaced
	// by the repetition number. Zero means once.
	Repeat int `json:"repeat,omitempty"`
	// Join sends the repeated deltas as one event, as a provider does when a
	// fast command finishes before its output is flushed in pieces.
	Join bool `json:"join,omitempty"`
}

// StepKinds are the event kinds a fixture may script.
var StepKinds = []string{
	"turn.started", "turn.completed", "message.delta", "message.completed",
	"reasoning.delta", "activity.started", "activity.completed", "command.output.delta",
}

// Turn is an expanded template.
type Turn struct {
	Number int
	User   string
	Steps  []Step
}

// DecodeFixture decodes a committed fixture, rejecting unknown fields.
func DecodeFixture(data []byte) (Fixture, error) {
	var fixture Fixture
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fixture); err != nil {
		return Fixture{}, err
	}
	if dec.More() {
		return Fixture{}, errors.New("fixture has data after its JSON value")
	}
	if fixture.SchemaVersion != FixtureSchema {
		return Fixture{}, fmt.Errorf("fixture schemaVersion must be %q", FixtureSchema)
	}
	if !slices.Contains(Providers, fixture.Provider) {
		return Fixture{}, errors.New("fixture provider is not allowlisted")
	}
	if fixture.SettledTurns < 1 || len(fixture.SettledTemplates) == 0 {
		return Fixture{}, errors.New("fixture needs settled turns and templates")
	}
	for _, template := range append(slices.Clone(fixture.SettledTemplates), fixture.ReplyTurn) {
		if err := template.validate(); err != nil {
			return Fixture{}, err
		}
	}
	return fixture, nil
}

func (t TurnTemplate) validate() error {
	if strings.TrimSpace(t.User) == "" || len(t.Steps) < 2 {
		return errors.New("a turn template needs a user message and steps")
	}
	if t.Steps[0].Kind != "turn.started" || t.Steps[len(t.Steps)-1].Kind != "turn.completed" {
		return errors.New("a turn template must start with turn.started and end with turn.completed")
	}
	for _, step := range t.Steps {
		if !slices.Contains(StepKinds, step.Kind) {
			return fmt.Errorf("step kind %q is not supported", step.Kind)
		}
		if step.Repeat < 0 || ((step.Repeat > 0 || step.Join) && !strings.HasSuffix(step.Kind, ".delta")) {
			return errors.New("only delta steps repeat")
		}
		if step.Join && step.Repeat < 2 {
			return errors.New("join needs repeat of at least 2")
		}
		if len(step.Detail) > 0 && !json.Valid(step.Detail) {
			return errors.New("step detail is not valid JSON")
		}
	}
	return nil
}

// SettledTurnList expands the settled turns in order.
func (f Fixture) SettledTurnList() []Turn {
	turns := make([]Turn, 0, f.SettledTurns)
	for n := 1; n <= f.SettledTurns; n++ {
		turns = append(turns, expand(f.SettledTemplates[(n-1)%len(f.SettledTemplates)], n))
	}
	return turns
}

// Reply expands the reply turn, numbered after the settled turns.
func (f Fixture) Reply() Turn { return expand(f.ReplyTurn, f.SettledTurns+1) }

func expand(t TurnTemplate, n int) Turn {
	number := strconv.Itoa(n)
	sub := func(s string) string { return strings.ReplaceAll(s, "{n}", number) }
	turn := Turn{Number: n, User: sub(t.User)}
	streamed := map[string]string{}
	for _, step := range t.Steps {
		item := sub(step.Item)
		deltas := make([]string, 0, max(step.Repeat, 1))
		for r := 1; r <= max(step.Repeat, 1); r++ {
			deltas = append(deltas, strings.ReplaceAll(sub(step.Delta), "{r}", strconv.Itoa(r)))
		}
		if step.Join {
			deltas = []string{strings.Join(deltas, "")}
		}
		for _, delta := range deltas {
			expanded := step
			expanded.Repeat, expanded.Join = 0, false
			expanded.Item = item
			expanded.Delta = delta
			expanded.Text = strings.ReplaceAll(sub(step.Text), "{streamed}", streamed[item])
			expanded.Summary = sub(step.Summary)
			if len(step.Detail) > 0 {
				quoted, _ := json.Marshal(streamed[item])
				expanded.Detail = json.RawMessage(strings.ReplaceAll(sub(string(step.Detail)), `"{streamed}"`, string(quoted)))
			}
			if strings.HasSuffix(step.Kind, ".delta") {
				streamed[item] += delta
			}
			turn.Steps = append(turn.Steps, expanded)
		}
	}
	return turn
}

// CanonicalRow is one timeline row reduced to what a reader sees: the turn it
// belongs to, its kind, role or activity kind, status, a digest of its text,
// the full size of any command output, and for activities digests of the tool
// payload and of the output the reader holds.
type CanonicalRow struct {
	// Turn is the index of the row's turn in the state's turn list, or -1 for a
	// row outside any turn, so a row moved to another turn changes the state.
	Turn         int
	Kind         string // "message" or "activity"
	Role         string
	ActivityKind string
	Status       string
	Text         string // message text, or activity summary plus settled detail text
	OutputBytes  int64  // the full output size, even when the reader holds a preview
	// Payload is the activity's detail without output, outputBytes and text,
	// as CanonicalPayload renders it: file changes and patches, content
	// blocks, inputs, arguments and results.
	Payload string
	// Output is the output the reader holds: the whole output, or the head and
	// tail a page kept of a cut one.
	Output string
}

// CanonicalState renders rows (in sequence order) and turn states (in request
// order) as reviewable text. A state digest is the sha256 of this text.
func CanonicalState(rows []CanonicalRow, turnStates []string) string {
	var b strings.Builder
	for _, state := range turnStates {
		b.WriteString("turn\t" + state + "\n")
	}
	for _, row := range rows {
		switch row.Kind {
		case "message":
			fmt.Fprintf(&b, "message\t%d\t%s\t%s\t%d\n", row.Turn, row.Role, SHA256Hex([]byte(row.Text)), len(row.Text))
		default:
			fmt.Fprintf(&b, "activity\t%d\t%s\t%s\t%s\t%d\t%s\t%s\n", row.Turn, row.ActivityKind, row.Status, SHA256Hex([]byte(row.Text)), row.OutputBytes,
				SHA256Hex([]byte(row.Payload)), SHA256Hex([]byte(row.Output)))
		}
	}
	return b.String()
}

// CanonicalPayload renders an activity detail without output, outputBytes and
// text as compact JSON with sorted keys and no HTML escaping, or "" when
// nothing else is left. Numbers are decoded float64s, so an integral value has
// no fraction. scripts/chat-traffic-oracle.py renders the same bytes from
// the fixtures and accepts only integers within 2^53, where the two agree.
func CanonicalPayload(detail map[string]any) string {
	rest := make(map[string]any, len(detail))
	for key, value := range detail {
		switch key {
		case "output", "outputBytes", "text":
		default:
			rest[key] = value
		}
	}
	if len(rest) == 0 {
		return ""
	}
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(rest); err != nil {
		// Decoded JSON always encodes; an error still must not match anything.
		return "unencodable payload: " + err.Error()
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// OutputPreview is what a page with this output limit keeps of an output: the
// whole output when it fits, else its first limit/2 bytes and its last
// limit-limit/2 bytes, both moved to UTF-8 boundaries, joined by an elision
// line. It follows the documented outputLimit contract independently of the
// daemon's code, so the benchmark can hold a cut preview to it. A zero limit
// keeps everything.
func OutputPreview(output string, limit int64) string {
	if limit <= 0 || int64(len(output)) <= limit {
		return output
	}
	head := int(limit / 2)
	for head > 0 && !utf8.RuneStart(output[head]) {
		head--
	}
	tail := len(output) - (int(limit) - head)
	for tail < len(output) && !utf8.RuneStart(output[tail]) {
		tail++
	}
	return output[:head] + "\n…\n" + output[tail:]
}
