package deepseekharnessacp

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/deepseekharness"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// DeepSeek Harness subagents, read back out of its session store.
//
// `dsh --profile acp` launches a subagent with its `subagent` tool (or
// `subagent_fork`, a child that inherits the conversation). The call completes
// as soon as the child starts, with "started subagent <name>", and the ACP
// server sends nothing else of the child: its session's events are dropped, its
// report reaches the parent model as a user message, and how it ended is only
// in that message's text. The prompt itself returns once every child is idle.
//
// The harness records all of it as it happens, in the session store under
// <DSH_HOME>/sessions (deepseekharness.SessionTail reads it), as of
// @deepseek-ai/dsh 0.2.3-alpha.2:
//
//   - the parent's log has the launch's `tool/call` and its `tool/result`
//     ("started subagent <name>"), then a `subagent/catalog` event naming the
//     child's session for that name; the launch's call id is not stored with
//     the child, so the name links the two;
//   - the child's own log has its turns, messages, and tool calls and results;
//   - each run's end is a `user/message` on the parent with source kind
//     `subagent-settled`, naming the child and its stop reason, with the
//     child's closing message as its content.

const (
	launchTool     = "subagent"
	forkLaunchTool = "subagent_fork"
	// resumeTool sends a child more work; the child's next run follows.
	resumeTool = "send_message"
	// forkAgentType labels a forked child, which starts from the parent's
	// conversation rather than from its task alone.
	forkAgentType = "fork"
	// settledSource is the parent message that says how a child's run ended.
	settledSource = "subagent-settled"
	// endWaitFor bounds how long a run's end waits for the child's own log to
	// close the run: the two sessions write in separate batches.
	endWaitFor = 2 * time.Second
	// maxChildren caps the children one conversation follows.
	maxChildren = 64
)

var startedSubagent = regexp.MustCompile(`^started subagent (\S+)`)

func subagentRecords() *acpdriver.SubagentRecords {
	return &acpdriver.SubagentRecords{
		Launch: subagentLaunch,
		// send_message wakes a child an earlier turn, maybe on an earlier
		// connection, started.
		Resumes: func(call acpdriver.RecordedCall) bool { return call.Title == resumeTool },
		Open: func(env map[string]string, _ string, session string) acpdriver.SubagentRecordReader {
			home, err := deepseekharness.Home(env)
			return &recordReader{home: home, homeErr: err, session: session, now: time.Now, turnSeq: -1,
				children: map[string]*childLog{}, launchCalls: map[string]bool{}, launchOf: map[string]string{}}
		},
	}
}

// subagentLaunch recognises a launch by its title, the bare tool name the
// harness sends, and the input only a launch carries.
func subagentLaunch(call acpdriver.RecordedCall) (acpdriver.RecordedLaunch, bool) {
	if call.Title != launchTool && call.Title != forkLaunchTool {
		return acpdriver.RecordedLaunch{}, false
	}
	input, ok := call.RawInput.(map[string]any)
	if !ok {
		return acpdriver.RecordedLaunch{}, false
	}
	prompt, _ := input["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return acpdriver.RecordedLaunch{}, false
	}
	title, _ := input["description"].(string)
	if strings.TrimSpace(title) == "" {
		title, _ = input["name"].(string)
	}
	agentType, _ := input["agent_preset"].(string)
	if agentType == "" && call.Title == forkLaunchTool {
		agentType = forkAgentType
	}
	return acpdriver.RecordedLaunch{
		ToolName: call.Title, Title: strings.TrimSpace(title),
		Prompt: prompt, AgentType: strings.TrimSpace(agentType),
	}, true
}

type recordReader struct {
	home    string
	homeErr error
	session string
	now     func() time.Time

	parent *deepseekharness.SessionTail
	// turnSeq and turnAt mark where the conversation's current turn starts in
	// the parent's log: its latest prompt from the user. A settlement after it
	// and a child run started after it are this turn's work; anything earlier
	// belonged to an earlier turn and is never filed on this one.
	turnSeq int64
	turnAt  int64
	// launchCalls are the parent's launch call ids; launchOf maps a child's
	// name to the launch that started it.
	launchCalls map[string]bool
	launchOf    map[string]string
	children    map[string]*childLog
	// order keeps the children in the order the parent started them.
	order  []*childLog
	byName map[string]*childLog
}

type childLog struct {
	id, name, launch string
	tail             *deepseekharness.SessionTail
	// gone is set once the child's log cannot be followed: its steps are lost,
	// but the parent still says how each run ended.
	gone bool
	// inSeed holds a forked child's inherited history, which is the parent's,
	// not the child's.
	inSeed     bool
	headerRead bool
	calls      map[string]nativeReplayCall
	// runOpen is whether the child is mid-run, and current whether that run
	// is the current turn's, and so filed.
	runOpen bool
	current bool
	// closed counts the current turn's runs the child's log has closed that
	// no end has been paired with yet.
	closed int
	// ends are runs whose end the parent recorded, waiting for the child's log
	// to close their run.
	ends []pendingEnd
}

type pendingEnd struct {
	end   acpdriver.RecordedEnd
	since time.Time
}

func (r *recordReader) Busy() bool {
	for _, child := range r.order {
		if child.launch == "" {
			continue
		}
		if (child.runOpen && child.current && !child.gone) || len(child.ends) > 0 {
			return true
		}
	}
	return false
}

func (r *recordReader) Read(ctx context.Context) ([]acpdriver.RecordedUpdate, error) {
	if r.homeErr != nil {
		return nil, r.homeErr
	}
	if r.parent == nil {
		path, err := deepseekharness.FindSessionLog(r.home, r.session)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		r.parent = deepseekharness.NewSessionTail(path)
	}
	// Take everything new first: a later prompt in it moves the turn's start,
	// and what came before that prompt is an earlier turn's.
	var events []deepseekharness.NativeHistoryEvent
	for {
		batch, err := r.parent.Next(ctx)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		events = append(events, batch...)
	}
	for _, event := range events {
		if isUserPrompt(event) {
			r.turnSeq, r.turnAt = event.Seq, event.Time
		}
	}
	for _, event := range events {
		r.parentEvent(event)
	}
	var out []acpdriver.RecordedUpdate
	for _, child := range r.order {
		if child.launch == "" {
			continue
		}
		if !child.gone {
			updates, err := r.readChild(ctx, child)
			out = append(out, updates...)
			if err != nil {
				if ctx.Err() != nil {
					return out, err
				}
				// A child log that cannot be followed leaves its launch without
				// its steps; the rest are unaffected.
				child.gone = true
				child.runOpen = false
			}
		}
		out = append(out, r.dueEnds(child)...)
	}
	return out, nil
}

// isUserPrompt is a prompt the user sent, which starts a turn of the
// conversation. The harness's own messages to the model (a child settling, a
// child's message, injected context) carry other sources.
func isUserPrompt(event deepseekharness.NativeHistoryEvent) bool {
	if event.Type != "user/message" {
		return false
	}
	var message struct {
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
	}
	return json.Unmarshal(event.Data, &message) == nil && message.Source.Kind == "user"
}

func (r *recordReader) parentEvent(event deepseekharness.NativeHistoryEvent) {
	switch event.Type {
	case "tool/call":
		var call nativeReplayCall
		if json.Unmarshal(event.Data, &call) == nil && call.CallID != "" &&
			(call.Name == launchTool || call.Name == forkLaunchTool) {
			r.launchCalls[call.CallID] = true
		}
	case "tool/result":
		var data struct {
			Message nativeReplayMessage `json:"message"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.Message.IsError || !r.launchCalls[data.Message.ToolCallID] {
			return
		}
		for _, block := range data.Message.Content {
			if match := startedSubagent.FindStringSubmatch(strings.TrimSpace(block.Text)); match != nil {
				r.launchOf[match[1]] = data.Message.ToolCallID
				if child := r.byName[match[1]]; child != nil && child.launch == "" {
					child.launch = data.Message.ToolCallID
				}
			}
		}
	case "subagent/catalog":
		var entry struct {
			Version int    `json:"version"`
			ChildID string `json:"childId"`
			Mode    string `json:"mode"`
			Name    string `json:"name"`
		}
		// Before v3 a child had no name to link it to its launch, and an
		// external child has no session of its own here.
		if json.Unmarshal(event.Data, &entry) != nil || entry.Version < 3 || entry.Name == "" ||
			entry.Mode == "external" || entry.ChildID == "" || r.children[entry.ChildID] != nil ||
			len(r.order) >= maxChildren {
			return
		}
		child := &childLog{id: entry.ChildID, name: entry.Name, launch: r.launchOf[entry.Name], calls: map[string]nativeReplayCall{}}
		r.children[child.id] = child
		r.order = append(r.order, child)
		if r.byName == nil {
			r.byName = map[string]*childLog{}
		}
		r.byName[child.name] = child
	case "user/message":
		var message struct {
			Source struct {
				Kind            string `json:"kind"`
				StopReason      string `json:"stopReason"`
				SenderSessionID string `json:"senderSessionId"`
			} `json:"source"`
			Content []struct {
				Type, Text string
			} `json:"content"`
		}
		if json.Unmarshal(event.Data, &message) != nil || message.Source.Kind != settledSource {
			return
		}
		child := r.children[message.Source.SenderSessionID]
		// A run that ended in an earlier turn is that turn's, filed or not.
		if child == nil || event.Seq < r.turnSeq {
			return
		}
		texts := make([]string, 0, len(message.Content))
		for _, block := range message.Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				texts = append(texts, strings.TrimSpace(block.Text))
			}
		}
		status, report := settledRun(message.Source.StopReason, texts)
		child.ends = append(child.ends, pendingEnd{
			end:   acpdriver.RecordedEnd{ID: strconv.FormatInt(event.Seq, 10), Status: status, Report: report},
			since: r.now(),
		})
	}
}

// settledRun reads a settlement notice: a one-line summary, then the child's
// closing message under "Its closing message:", or a line saying there is
// none. A completed run reports its closing message; one that did not
// complete reports the whole notice, which says why.
func settledRun(stopReason string, texts []string) (acpdriver.RecordedEndStatus, string) {
	switch stopReason {
	case "completed", "max-tokens":
		if len(texts) > 1 && texts[1] == "Its closing message:" {
			return acpdriver.RecordedCompleted, strings.Join(texts[2:], "\n\n")
		}
		return acpdriver.RecordedCompleted, ""
	case "aborted":
		return acpdriver.RecordedCancelled, strings.Join(texts, "\n\n")
	default:
		return acpdriver.RecordedFailed, strings.Join(texts, "\n\n")
	}
}

// dueEnds releases run ends in order: each once the child's log has closed a
// run to pair it with, so the run's steps go out before its report, or once it
// has waited endWaitFor for a log that may never close it (or never appear).
// A timed-out end leaves the child's state alone: a newer run may be open.
func (r *recordReader) dueEnds(child *childLog) []acpdriver.RecordedUpdate {
	var out []acpdriver.RecordedUpdate
	for len(child.ends) > 0 {
		pending := child.ends[0]
		switch {
		case child.closed > 0:
			child.closed--
		case r.now().Sub(pending.since) >= endWaitFor:
		default:
			return out
		}
		end := pending.end
		out = append(out, acpdriver.RecordedUpdate{Launch: child.launch, Child: child.id, End: &end})
		child.ends = child.ends[1:]
	}
	return out
}

func (r *recordReader) readChild(ctx context.Context, child *childLog) ([]acpdriver.RecordedUpdate, error) {
	if child.tail == nil {
		path, err := deepseekharness.FindSessionLog(r.home, child.id)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		child.tail = deepseekharness.NewSessionTail(path)
	}
	var out []acpdriver.RecordedUpdate
	emit := func(update acpsdk.SessionUpdate) {
		if child.runOpen && child.current {
			out = append(out, acpdriver.RecordedUpdate{Launch: child.launch, Child: child.id, Update: &update})
		}
	}
	for {
		events, err := child.tail.Next(ctx)
		if err != nil {
			return out, err
		}
		if !child.headerRead {
			if header, ok := child.tail.Header(); ok {
				child.headerRead = true
				child.inSeed = header.IsSeeded
			}
		}
		if len(events) == 0 {
			return out, nil
		}
		for _, event := range events {
			child.event(event, r.turnAt, emit)
		}
	}
}

func (child *childLog) event(event deepseekharness.NativeHistoryEvent, turnAt int64, emit func(acpsdk.SessionUpdate)) {
	if child.inSeed {
		var marker struct {
			Inherited bool `json:"inherited"`
		}
		if event.Type == "session/end-seed" && json.Unmarshal(event.Data, &marker) == nil && marker.Inherited {
			child.inSeed = false
		}
		return
	}
	switch event.Type {
	case "turn/start":
		child.runOpen = true
		// A run that started before the turn's prompt is an earlier turn's.
		child.current = event.Time >= turnAt
	case "turn/end":
		if child.runOpen && child.current {
			child.closed++
		}
		child.runOpen = false
	case "tool/call":
		var call nativeReplayCall
		if json.Unmarshal(event.Data, &call) != nil || call.CallID == "" {
			return
		}
		child.calls[call.CallID] = call
		emit(acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			ToolCallId: acpsdk.ToolCallId(call.CallID), Title: call.Name,
			Kind: acpsdk.ToolKindOther, Status: acpsdk.ToolCallStatusInProgress,
			RawInput: callInput(call.Arguments),
		}})
	case "tool/result":
		var data struct {
			Message nativeReplayMessage `json:"message"`
		}
		if json.Unmarshal(event.Data, &data) != nil {
			return
		}
		call, known := child.calls[data.Message.ToolCallID]
		if !known {
			return
		}
		delete(child.calls, call.CallID)
		var content []acpsdk.ToolCallContent
		for _, block := range data.Message.Content {
			if block.Type == "text" && block.Text != "" {
				content = append(content, acpsdk.ToolContent(acpsdk.TextBlock(block.Text)))
			}
		}
		status := acpsdk.ToolCallStatusCompleted
		if data.Message.IsError {
			status = acpsdk.ToolCallStatusFailed
		}
		emit(acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
			ToolCallId: acpsdk.ToolCallId(call.CallID), Status: &status, Content: content,
		}})
	case "assistant/message":
		var data struct {
			Message nativeReplayMessage `json:"message"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.Message.ID == "" {
			return
		}
		id := data.Message.ID
		for _, block := range data.Message.Content {
			// Reasoning is left out, as Claude's subagent reasoning is.
			if block.Type != "text" || block.Text == "" {
				continue
			}
			emit(acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
				MessageId: &id, Content: acpsdk.TextBlock(block.Text),
			}})
		}
	}
}

// callInput is a tool call's arguments as the ACP server sends them: parsed
// JSON, or the raw string when it is not JSON.
func callInput(arguments string) any {
	var input any
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return arguments
	}
	return input
}
