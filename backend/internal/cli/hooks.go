package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitydispatch"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/memcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$`)

const (
	hooksLogName     = "hooks.log"
	maxHooksLogBytes = 1 << 20
)

type setActivityAPIRequest struct {
	ObservedAt                   time.Time                           `json:"observedAt,omitempty"`
	State                        string                              `json:"state,omitempty"`
	Event                        string                              `json:"event,omitempty"`
	ToolName                     string                              `json:"toolName,omitempty"`
	ToolUseID                    string                              `json:"toolUseId,omitempty"`
	SubagentID                   string                              `json:"subagentId,omitempty"`
	RunningSubagentIDs           *[]string                           `json:"runningSubagentIds,omitempty"`
	AgentSessionID               string                              `json:"agentSessionId,omitempty"`
	LatestUserPrompt             string                              `json:"latestUserPrompt,omitempty"`
	LatestAssistantUpdate        string                              `json:"latestAssistantUpdate,omitempty"`
	FinalAssistantMessage        string                              `json:"finalAssistantMessage,omitempty"`
	TurnUserRequest              string                              `json:"turnUserRequest,omitempty"`
	Harness                      string                              `json:"harness,omitempty"`
	ConversationCheckpointOrigin domain.ConversationCheckpointOrigin `json:"conversationCheckpointOrigin,omitempty"`
	CoordinationID               string                              `json:"coordinationId,omitempty"`
	ProviderTurnID               string                              `json:"providerTurnId,omitempty"`
	SubmissionID                 string                              `json:"submissionId,omitempty"`
	TranscriptPath               string                              `json:"transcriptPath,omitempty"`
	LaunchID                     string                              `json:"launchId,omitempty"`
	Entrypoint                   string                              `json:"entrypoint,omitempty"`
	ProcessID                    int                                 `json:"processId,omitempty"`
	Usage                        *usageHookMetadata                  `json:"usage,omitempty"`
	PRMayHaveChanged             bool                                `json:"prMayHaveChanged,omitempty"`
}

type usageHookMetadata struct {
	Harness                string `json:"harness"`
	ProviderID             string `json:"providerId,omitempty"`
	TranscriptPath         string `json:"transcriptPath,omitempty"`
	ModelID                string `json:"modelId,omitempty"`
	SubagentID             string `json:"subagentId,omitempty"`
	SubagentTranscriptPath string `json:"subagentTranscriptPath,omitempty"`
}

type setReviewActivityAPIRequest struct {
	State          string `json:"state,omitempty"`
	Event          string `json:"event,omitempty"`
	AgentSessionID string `json:"agentSessionId,omitempty"`
	LaunchID       string `json:"launchId,omitempty"`
}

const maxActivityMetaLen = 256

const (
	maxHookInteractionLen = 16 << 10
	maxHookTranscriptPath = 4096
)

func activityMeta(payload []byte) (toolName, toolUseID string) {
	payload = normalizeHookPayload(payload)
	var p struct {
		ToolName  string `json:"tool_name"`
		ToolUseID string `json:"tool_use_id"`
	}
	_ = json.Unmarshal(payload, &p)
	if len(p.ToolName) > maxActivityMetaLen {
		p.ToolName = ""
	}
	if len(p.ToolUseID) > maxActivityMetaLen {
		p.ToolUseID = ""
	}
	return p.ToolName, p.ToolUseID
}

func hookCommandMayChangePR(payload []byte) bool {
	var p struct {
		ToolInput struct {
			Command json.RawMessage `json:"command"`
		} `json:"tool_input"`
	}
	return json.Unmarshal(normalizeHookPayload(payload), &p) == nil && domain.CommandMayChangePR(string(p.ToolInput.Command))
}

func kimiApprovalID(payload []byte) string {
	var p struct {
		ID        string      `json:"id"`
		SessionID string      `json:"session_id"`
		AgentID   string      `json:"agent_id"`
		TurnID    json.Number `json:"turn_id"`
	}
	if json.Unmarshal(normalizeHookPayload(payload), &p) != nil {
		return ""
	}
	for _, value := range []string{p.ID, p.SessionID, p.AgentID} {
		if value == "" || len(value) > maxActivityMetaLen || domain.SanitizeControlChars(value) != value {
			return ""
		}
	}
	turn, err := p.TurnID.Int64()
	if err != nil || turn < 0 {
		return ""
	}
	key := p.SessionID + "\x00" + p.AgentID + "\x00" + strconv.FormatInt(turn, 10) + "\x00" + p.ID
	return fmt.Sprintf("kimi-approval:%x", sha256.Sum256([]byte(key)))
}

func claudeSubagentFacts(event string, payload []byte) (string, *[]string) {
	var p struct {
		AgentID         string          `json:"agent_id"`
		BackgroundTasks json.RawMessage `json:"background_tasks"`
	}
	if json.Unmarshal(normalizeHookPayload(payload), &p) != nil {
		return "", nil
	}
	id := validSubagentID(p.AgentID)
	if (event != "stop" && event != "subagent-stop") || len(p.BackgroundTasks) == 0 || p.BackgroundTasks[0] != '[' {
		return id, nil
	}
	var tasks []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if json.Unmarshal(p.BackgroundTasks, &tasks) != nil || len(tasks) > 128 {
		return id, nil
	}
	running := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.Type != "subagent" {
			continue
		}
		childID := validSubagentID(task.ID)
		if childID == "" {
			return id, nil
		}
		running = append(running, childID)
	}
	return id, &running
}

func validSubagentID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > maxActivityMetaLen || domain.SanitizeControlChars(id) != id {
		return ""
	}
	return id
}

func codexSubagentID(event string, payload []byte) string {
	if event != "subagent-start" && event != "subagent-stop" && event != "user-prompt-submit" {
		return ""
	}
	var p struct {
		AgentID string `json:"agent_id"`
	}
	if json.Unmarshal(normalizeHookPayload(payload), &p) != nil {
		return ""
	}
	return validSubagentID(p.AgentID)
}

func codexSpawnToolUseID(payload []byte) string {
	var p struct {
		ToolName     string `json:"tool_name"`
		ToolUseID    string `json:"tool_use_id"`
		AgentID      string `json:"agent_id"`
		ToolResponse string `json:"tool_response"`
	}
	if json.Unmarshal(normalizeHookPayload(payload), &p) != nil ||
		!isCodexSpawnToolName(p.ToolName) || p.AgentID != "" {
		return ""
	}
	id := validSubagentID(p.ToolUseID)
	if id == "" {
		return ""
	}
	var response struct {
		TaskName string `json:"task_name"`
	}
	if json.Unmarshal([]byte(p.ToolResponse), &response) != nil || response.TaskName == "" {
		return ""
	}
	return id
}

func isCodexSpawnToolName(name string) bool {
	return name == "spawn_agent" || name == "collaborationspawn_agent"
}

func normalizeHookPayload(payload []byte) []byte {
	return bytes.TrimPrefix(payload, []byte("\xef\xbb\xbf"))
}

func hookAgentSessionIDForHarness(agent string, payload []byte) string {
	if agent == "cline" {
		id, _ := activitydispatch.AgentSessionID(agent, payload)
		return id
	}
	if activitydispatch.SupportsHarness(domain.AgentHarness(agent)) {
		return hookAgentSessionID(payload)
	}
	return ""
}

func hookAgentSessionID(payload []byte) string {
	payload = normalizeHookPayload(payload)
	var p struct {
		SessionID           string `json:"session_id"`
		SessionIDCamel      string `json:"sessionId"`
		ConversationID      string `json:"conversation_id"`
		ConversationIDCamel string `json:"conversationId"`
		SessionContext      struct {
			RootSessionID string `json:"rootSessionId"`
		} `json:"sessionContext"`
		TaskIDCamel string `json:"taskId"`
		TaskIDSnake string `json:"task_id"`
	}
	_ = json.Unmarshal(payload, &p)
	id := strings.TrimSpace(p.SessionContext.RootSessionID)
	if id == "" {
		id = strings.TrimSpace(p.SessionID)
	}
	if id == "" {
		id = strings.TrimSpace(p.SessionIDCamel)
	}
	if id == "" {
		id = strings.TrimSpace(p.ConversationID)
	}
	if id == "" {
		id = strings.TrimSpace(p.ConversationIDCamel)
	}
	if id == "" {
		id = strings.TrimSpace(p.TaskIDCamel)
	}
	if id == "" {
		id = strings.TrimSpace(p.TaskIDSnake)
	}
	if len(id) > maxActivityMetaLen {
		return ""
	}
	return id
}

func hookLaunchID(payload []byte) string {
	payload = normalizeHookPayload(payload)
	var p struct {
		LaunchID      string `json:"launch_id"`
		LaunchIDCamel string `json:"launchId"`
	}
	_ = json.Unmarshal(payload, &p)
	id := strings.TrimSpace(p.LaunchID)
	if id == "" {
		id = strings.TrimSpace(p.LaunchIDCamel)
	}
	if len(id) > maxActivityMetaLen {
		return ""
	}
	return id
}

func hookUsageMetadata(agent string, payload []byte) *usageHookMetadata {
	payload = normalizeHookPayload(payload)
	harness := domain.AgentHarness(agent)
	if harness != domain.HarnessClaudeCode && harness != domain.HarnessCodex && harness != domain.HarnessPi {
		return nil
	}
	var native struct {
		TranscriptPath         string `json:"transcript_path"`
		Model                  string `json:"model"`
		SubagentID             string `json:"agent_id"`
		SubagentTranscriptPath string `json:"agent_transcript_path"`
	}
	if json.Unmarshal(payload, &native) != nil {
		return nil
	}
	meta := &usageHookMetadata{
		Harness:                agent,
		TranscriptPath:         strings.TrimSpace(native.TranscriptPath),
		ModelID:                strings.TrimSpace(native.Model),
		SubagentID:             strings.TrimSpace(native.SubagentID),
		SubagentTranscriptPath: strings.TrimSpace(native.SubagentTranscriptPath),
	}
	if harness == domain.HarnessPi {
		meta.ModelID = ""
		meta.SubagentID = ""
		meta.SubagentTranscriptPath = ""
		if meta.TranscriptPath == "" {
			return nil
		}
		return meta
	}
	if meta.TranscriptPath == "" && meta.SubagentTranscriptPath == "" && meta.ModelID == "" {
		return nil
	}
	meta.ProviderID = claudeHookProviderHint(harness)
	return meta
}

func claudeHookProviderHint(harness domain.AgentHarness) string {
	if harness != domain.HarnessClaudeCode {
		return ""
	}
	bedrock := hookRouteFlagEnabled(os.Getenv("CLAUDE_CODE_USE_BEDROCK"))
	vertex := hookRouteFlagEnabled(os.Getenv("CLAUDE_CODE_USE_VERTEX"))
	if bedrock != vertex {
		if bedrock {
			return "bedrock"
		}
		return "vertex_ai"
	}
	if bedrock {
		return pricing.UnidentifiedBillingRoute
	}
	baseURL := strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
	if baseURL == "" {
		return "anthropic"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return pricing.UnidentifiedBillingRoute
	}
	if parsed.Hostname() == "" && !strings.Contains(baseURL, "://") {
		parsed, err = url.Parse("https://" + baseURL)
	}
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return pricing.UnidentifiedBillingRoute
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "api.anthropic.com":
		return "anthropic"
	case "api.z.ai":
		return "zai"
	default:
		return pricing.UnidentifiedBillingRoute
	}
}

func hookRouteFlagEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type hookConversationSnapshot struct {
	ProviderTurnID        string
	LatestUserPrompt      string
	LatestAssistantUpdate string
	FinalAssistantMessage string
	TurnUserRequest       string
	CheckpointOrigin      domain.ConversationCheckpointOrigin
	CoordinationID        string
	TranscriptPath        string
}

func hookConversationFacts(agent domain.AgentHarness, event string, payload []byte) hookConversationSnapshot {
	payload = normalizeHookPayload(payload)
	var p struct {
		Prompt               string `json:"prompt"`
		TurnID               string `json:"turn_id"`
		PromptID             string `json:"prompt_id"`
		UserPrompt           string `json:"user_prompt"`
		UserPromptCamel      string `json:"userPrompt"`
		LastAssistantMessage string `json:"last_assistant_message"`
		LastAssistantCamel   string `json:"lastAssistantMessage"`
		PromptResponse       string `json:"prompt_response"`
		PromptResponseCamel  string `json:"promptResponse"`
		TranscriptPath       string `json:"transcript_path"`
		TranscriptPathCamel  string `json:"transcriptPath"`
		SubagentID           string `json:"agent_id"`
	}
	decodeErr := json.Unmarshal(payload, &p)
	observedPrompt := firstHookValue(p.Prompt, p.UserPrompt, p.UserPromptCamel)
	coordinationPrompt := observedPrompt
	if agent == domain.HarnessClaudeCode {
		coordinationPrompt = normalizeClaudePastedPrompt(observedPrompt)
	}
	var userPrompt, assistant, finalMessage, turnID string
	origin := domain.ConversationCheckpointOriginUnknown
	if strings.TrimSpace(p.SubagentID) == "" {
		if event == "user-prompt-submit" || event == "stop" {
			switch agent {
			case domain.HarnessCodex:
				turnID = strings.TrimSpace(p.TurnID)
			case domain.HarnessClaudeCode:
				turnID = strings.TrimSpace(p.PromptID)
			}
			if len(turnID) > maxActivityMetaLen || domain.SanitizeControlChars(turnID) != turnID {
				turnID = ""
			}
		}
		switch event {
		case "user-prompt-submit":
			userPrompt = observedPrompt
			origin = domain.ConversationCheckpointOriginHuman
		case "stop":
			if agent == domain.HarnessClaudeCode || agent == domain.HarnessContinue {
				assistant = p.LastAssistantMessage
			}
			finalMessage = firstHookValue(p.LastAssistantMessage, p.LastAssistantCamel, p.PromptResponse, p.PromptResponseCamel)
		}
	}
	if isAOCoordinationMessage(coordinationPrompt) {
		userPrompt = ""
		assistant = ""
		finalMessage = ""
		if event == "user-prompt-submit" || event == "stop" {
			origin = domain.ConversationCheckpointOriginCoordination
		}
	}
	turnRequest := userPrompt
	if decodeErr != nil {
		turnRequest, finalMessage = "", ""
	}
	coordinationID, _ := domain.CoordinationDeliveryID(coordinationPrompt)
	return hookConversationSnapshot{
		ProviderTurnID:        turnID,
		LatestUserPrompt:      capHookText(userPrompt, maxHookInteractionLen),
		LatestAssistantUpdate: capHookText(assistant, maxHookInteractionLen),
		FinalAssistantMessage: capHookText(finalMessage, maxHookInteractionLen),
		TurnUserRequest:       capHookText(turnRequest, maxHookInteractionLen),
		CheckpointOrigin:      origin,
		CoordinationID:        coordinationID,
		TranscriptPath:        capHookText(firstHookValue(p.TranscriptPath, p.TranscriptPathCamel), maxHookTranscriptPath),
	}
}

var claudePastedContentPrefix = regexp.MustCompile(`^<pasted_content id="([A-Za-z0-9_-]+)">`)
var claudePastedContentTag = regexp.MustCompile(`<pasted_content id="([A-Za-z0-9_-]+)">|</pasted_content(?: id="([A-Za-z0-9_-]+)")?>`)
var claudePastedContentLeadingClosing = regexp.MustCompile(`^</pasted_content(?: id="([A-Za-z0-9_-]+)")?>`)

func normalizeClaudePastedPrompt(prompt string) string {
	match := claudePastedContentPrefix.FindStringSubmatch(prompt)
	if match == nil {
		return prompt
	}
	content := strings.TrimPrefix(prompt, match[0])
	before, after, ok := strings.Cut(content, `</pasted_content id="`+match[1]+`">`)
	if !ok {
		before, after, ok = strings.Cut(content, "</pasted_content>")
	}
	if !ok {
		return prompt
	}
	return before + after
}

func reportDeliveryHasOnlyPastedClosers(value string) bool {
	const reportClose = "</ao-report-delivery>"
	closeAt := strings.Index(value, reportClose)
	if closeAt < 0 {
		return false
	}
	suffix := strings.TrimSpace(value[closeAt+len(reportClose):])
	if suffix == "" {
		return true
	}
	var open []string
	closeBlock := func(id string) bool {
		for i := len(open) - 1; i >= 0; i-- {
			if id == "" || open[i] == id {
				open = append(open[:i], open[i+1:]...)
				return true
			}
		}
		return false
	}
	for _, tag := range claudePastedContentTag.FindAllStringSubmatch(value[:closeAt], -1) {
		if tag[1] != "" {
			open = append(open, tag[1])
		} else if !closeBlock(tag[2]) {
			return false
		}
	}
	for suffix != "" {
		match := claudePastedContentLeadingClosing.FindStringSubmatch(suffix)
		if len(match) != 2 || !closeBlock(match[1]) {
			return false
		}
		suffix = strings.TrimSpace(suffix[len(match[0]):])
	}
	return true
}

func firstHookValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func isAOCoordinationMessage(value string) bool {
	value = strings.TrimSpace(value)
	if _, reportDelivery := domain.ReportDeliveryID(value); reportDelivery {
		return reportDeliveryHasOnlyPastedClosers(value)
	}
	_, sessionDelivery := domain.CoordinationDeliveryID(value)
	return sessionDelivery || strings.HasPrefix(value, "<ao-handoff-request") ||
		strings.HasPrefix(value, "AO transferred the previous agent's context in hidden system instructions.")
}

func capHookText(value string, limit int) string {
	value = domain.SanitizeControlChars(strings.TrimSpace(value))
	if limit <= 0 || len(value) <= limit {
		return value
	}
	const marker = "\n[... truncated by AO ...]\n"
	budget := limit - len(marker)
	if budget <= 0 {
		return ""
	}
	head := budget / 2
	tail := budget - head
	return strings.ToValidUTF8(string([]byte(value)[:head])+marker+string([]byte(value)[len(value)-tail:]), "?")
}

type sessionStartHookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

type cursorPermissionHookOutput struct {
	Permission string `json:"permission"`
}

type claudePermissionHookOutput struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message,omitempty"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

type claudePreToolUseDenyOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

const artifactToolName = "Artifact"

const artifactNudgeReason = "Orchestrator does not show Claude artifacts. Write the deliverable as a file in the session artifact directory; " +
	"in a chat session, use html_render."

func claimArtifactNudge(sessionID string) bool {
	dataDir := strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
	if dataDir == "" {
		return false
	}
	dir := filepath.Join(dataDir, "hooks", sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(dir, "artifact-nudge"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func (c *commandContext) emitArtifactNudge(agent, event, sessionID string, payload []byte) {
	if domain.AgentHarness(agent) != domain.HarnessClaudeCode || event != "pre-tool-use" {
		return
	}
	if toolName, _ := activityMeta(payload); toolName != artifactToolName || !claimArtifactNudge(sessionID) {
		return
	}
	var out claudePreToolUseDenyOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = artifactNudgeReason
	if err := json.NewEncoder(c.deps.Out).Encode(out); err != nil {
		c.reportHookFailure(agent, event, sessionID, fmt.Errorf("write artifact nudge: %w", err))
	}
}

type claudePostToolUseOutput struct {
	SystemMessage      string `json:"systemMessage,omitempty"`
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

const claudeComputerUseToolPrefix = "mcp__computer-use__"

const envDesktopApp = "AO_DESKTOP_APP"

const computerUsePermissionNotice = "Computer use needs Orchestrator to have Accessibility and Screen Recording: macOS checks Orchestrator for " +
	"everything this session runs, not Claude. Grant them in Orchestrator under Settings > Computer use agents > Native computer use > Grant macOS permissions to Orchestrator, " +
	"then restart this session's agent if it still says not granted."

const computerUsePermissionContext = "This session runs inside Orchestrator, so macOS checks Orchestrator's permissions, not the Claude desktop app's " +
	"or a terminal's, for everything it runs. Ignore the advice to grant Claude: tell the user to grant Accessibility and Screen Recording to " +
	"Orchestrator in Orchestrator's Settings > Computer use agents > Native computer use > Grant macOS permissions to Orchestrator, and that if it still says not granted afterwards, " +
	"this session's agent needs a restart to see them."

func computerUsePermissionDenied(payload []byte) bool {
	if toolName, _ := activityMeta(payload); !strings.HasPrefix(toolName, claudeComputerUseToolPrefix) {
		return false
	}
	var p struct {
		ToolResponse json.RawMessage `json:"tool_response"`
		Error        string          `json:"error"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return false
	}
	for _, text := range []string{string(p.ToolResponse), p.Error} {
		lower := strings.ToLower(text)
		if strings.Contains(lower, "permission(s) are still not granted") &&
			(strings.Contains(lower, "accessibility") || strings.Contains(lower, "screen recording")) {
			return true
		}
	}
	return false
}

func (c *commandContext) emitComputerUsePermissionNotice(agent, event, sessionID string, payload []byte) {
	if domain.AgentHarness(agent) != domain.HarnessClaudeCode || os.Getenv(envDesktopApp) != "1" {
		return
	}
	var hookEventName string
	switch event {
	case "post-tool-use":
		hookEventName = "PostToolUse"
	case "post-tool-use-failure":
		hookEventName = "PostToolUseFailure"
	default:
		return
	}
	if !computerUsePermissionDenied(payload) {
		return
	}
	var out claudePostToolUseOutput
	out.SystemMessage = computerUsePermissionNotice
	out.HookSpecificOutput.HookEventName = hookEventName
	out.HookSpecificOutput.AdditionalContext = computerUsePermissionContext
	if err := json.NewEncoder(c.deps.Out).Encode(out); err != nil {
		c.reportHookFailure(agent, event, sessionID, fmt.Errorf("write computer use permission notice: %w", err))
	}
}

const reviewerPermissionDenyMessage = "Orchestrator headless reviewer: no human can answer permission prompts. " +
	"Use only the allowlisted read commands (git diff/log/show/status, gh pr view/diff/checks, Read, Grep, Glob) " +
	"and the exact single-line `printf '%s' '<json>' | ...` submit commands from the review task."

func reviewerPermissionDecision(payload []byte, workerSessionID string) claudePermissionHookOutput {
	var p struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	_ = json.Unmarshal(payload, &p)
	var out claudePermissionHookOutput
	out.HookSpecificOutput.HookEventName = "PermissionRequest"
	out.HookSpecificOutput.Decision.Behavior = "deny"
	out.HookSpecificOutput.Decision.Message = reviewerPermissionDenyMessage
	if p.ToolName != "Bash" || !claudecode.ReviewerSubmitCommandAllowed(p.ToolInput.Command, workerSessionID) {
		return out
	}
	out.HookSpecificOutput.Decision.Behavior = "allow"
	out.HookSpecificOutput.Decision.Message = ""
	return out
}

func newHooksCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:    "hooks <agent> <event>",
		Short:  "Receive an agent hook callback (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runHook(cmd.Context(), args[0], args[1])
		},
	}
}

func (c *commandContext) runHook(ctx context.Context, agent, event string) error {
	observedAt := c.deps.Now()
	if isAgyModernHookEvent(agent, event) {
		_, _ = fmt.Fprintln(c.deps.Out, "{}")
	}
	reviewSessionID := strings.TrimSpace(os.Getenv("AO_REVIEW_SESSION_ID"))
	if reviewSessionID != "" {
		if !sessionIDPattern.MatchString(reviewSessionID) {
			return nil
		}
		return c.runReviewHook(ctx, agent, event, reviewSessionID)
	}
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if !sessionIDPattern.MatchString(sessionID) {
		return nil
	}
	if agent == "memcode" && strings.HasPrefix(event, "context-chunk-") {
		if err := memcode.EmitInstructionChunk(c.deps.Out, event); err != nil {
			c.reportHookFailure(agent, event, sessionID, err)
		}
		return nil
	}
	if agent == "memcode" && event == "session-start" {
		reportAfterRestore, err := memcode.EmitStartWitness(c.deps.Out)
		if err != nil {
			c.reportHookFailure(agent, event, sessionID, err)
			return nil
		}
		if os.Getenv("AO_MEMCODE_INTERACTIVE_RESTORE") == "1" && !reportAfterRestore {
			return nil
		}
	}
	if agent == "memcode" && event == "session-end" {
		return nil
	}
	var payload []byte
	if hookReadsStdin(agent, event) {
		var err error
		payload, err = io.ReadAll(c.deps.In)
		if err != nil {
			c.reportHookFailure(agent, event, sessionID, fmt.Errorf("read stdin: %w", err))
		}
	}
	if shouldEmitSessionStartContext(agent, event) {
		c.emitSessionStartContext(agent, event, sessionID)
	}
	c.emitArtifactNudge(agent, event, sessionID, payload)
	c.emitComputerUsePermissionNotice(agent, event, sessionID, payload)
	if isCursorPermissionHook(agent, event) {
		return c.runCursorPermissionHook(ctx, agent, event, sessionID, payload)
	}

	state, hasActivity := activitydispatch.Derive(agent, event, payload)
	agentSessionID := hookAgentSessionIDForHarness(agent, payload)
	if agent == "memcode" && event == "session-start" {
		agentSessionID = os.Getenv("MEMCODE_SESSION_ID")
	}
	usage := hookUsageMetadata(agent, payload)
	var subagentID string
	var runningSubagentIDs *[]string
	if domain.AgentHarness(agent) == domain.HarnessClaudeCode {
		subagentID, runningSubagentIDs = claudeSubagentFacts(event, payload)
	} else if domain.AgentHarness(agent) == domain.HarnessCodex {
		subagentID = codexSubagentID(event, payload)
		if event == "post-tool-use" {
			if spawnID := codexSpawnToolUseID(payload); spawnID != "" {
				subagentID = spawnID
				event = "subagent-spawn"
			}
		}
	}
	prMayHaveChanged := event == "post-tool-use" && hookCommandMayChangePR(payload)
	if !hasActivity && agentSessionID == "" && usage == nil && subagentID == "" && !prMayHaveChanged {
		return nil
	}

	launchID := validLaunchID(os.Getenv("AO_RUNTIME_LAUNCH_ID"))
	if launchID == "" {
		launchID = validLaunchID(hookLaunchID(payload))
	}

	toolName, toolUseID := activityMeta(payload)
	if domain.AgentHarness(agent) == domain.HarnessKimi && (event == "permission-request" || event == "permission-resolved") {
		toolUseID = kimiApprovalID(payload)
	}
	if domain.AgentHarness(agent) == domain.HarnessCursor && event == "post-tool-use-failure" {
		if failureEvent, failureTool, ok := cursor.TerminalFailureCorrelation(payload); ok {
			event = failureEvent
			toolName = failureTool
		}
	}
	conversation := hookConversationSnapshot{}
	switch domain.AgentHarness(agent) {
	case domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessContinue:
		conversation = hookConversationFacts(domain.AgentHarness(agent), event, payload)
	case domain.HarnessOpenCode, domain.HarnessGrok, domain.HarnessKilocode,
		domain.HarnessOMP, domain.HarnessPi,
		domain.HarnessAmp, domain.HarnessPrimeAgent, domain.HarnessMiniMaxCode:
		conversation = hookSemanticAcceptanceFacts(event, payload)
		if domain.AgentHarness(agent) == domain.HarnessPi {
			conversation.TranscriptPath = hookConversationFacts(domain.HarnessPi, event, payload).TranscriptPath
		}
	}
	history := hookConversationFacts(domain.AgentHarness(agent), event, payload)
	conversation.TurnUserRequest, conversation.FinalAssistantMessage = history.TurnUserRequest, history.FinalAssistantMessage
	if domain.AgentHarness(agent) == domain.HarnessCopilot && event == "stop" && conversation.FinalAssistantMessage == "" {
		conversation.FinalAssistantMessage = capHookText(copilot.FinalAssistantMessage(payload), maxHookInteractionLen)
	}
	path := "sessions/" + url.PathEscape(sessionID) + "/activity"
	req := setActivityAPIRequest{
		ObservedAt:                   observedAt,
		Event:                        event,
		ToolName:                     toolName,
		ToolUseID:                    toolUseID,
		SubagentID:                   subagentID,
		RunningSubagentIDs:           runningSubagentIDs,
		AgentSessionID:               agentSessionID,
		LatestUserPrompt:             conversation.LatestUserPrompt,
		LatestAssistantUpdate:        conversation.LatestAssistantUpdate,
		FinalAssistantMessage:        conversation.FinalAssistantMessage,
		TurnUserRequest:              conversation.TurnUserRequest,
		Harness:                      agent,
		ConversationCheckpointOrigin: conversation.CheckpointOrigin,
		CoordinationID:               conversation.CoordinationID,
		ProviderTurnID:               conversation.ProviderTurnID,
		TranscriptPath:               conversation.TranscriptPath,
		LaunchID:                     launchID,
		Usage:                        usage,
		PRMayHaveChanged:             prMayHaveChanged,
	}
	if hasActivity {
		req.State = string(state)
	}
	if domain.AgentHarness(agent) == domain.HarnessClaudeCode {
		if entrypoint := strings.TrimSpace(os.Getenv("CLAUDE_CODE_ENTRYPOINT")); len(entrypoint) <= maxActivityMetaLen {
			req.Entrypoint = entrypoint
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(os.Getenv("CLAUDE_PID"))); err == nil && pid > 1 {
			req.ProcessID = pid
		}
	}
	if domain.AgentHarness(agent) == domain.HarnessClaudeCode && event == "user-prompt-submit" &&
		launchID != "" && agentSessionID != "" && conversation.CheckpointOrigin != domain.ConversationCheckpointOriginUnknown {
		req.SubmissionID = uuid.NewString()
		output := sessionStartHookOutput{}
		output.HookSpecificOutput.HookEventName = "UserPromptSubmit"
		output.HookSpecificOutput.AdditionalContext = domain.NativeSubmissionContext(req.SubmissionID)
		if err := json.NewEncoder(c.deps.Out).Encode(output); err != nil {
			return fmt.Errorf("emit native submission correlation: %w", err)
		}
	}
	if err := c.postActivityHook(ctx, path, req); err != nil {
		c.reportHookFailure(agent, event, sessionID, err)
	}
	return nil
}

func hookSemanticAcceptanceFacts(event string, payload []byte) hookConversationSnapshot {
	if event != "user-prompt-submit" {
		return hookConversationSnapshot{}
	}
	var p struct {
		Prompt string `json:"prompt"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return hookConversationSnapshot{}
	}
	id, ok := domain.CoordinationDeliveryID(p.Prompt)
	if !ok {
		return hookConversationSnapshot{}
	}
	return hookConversationSnapshot{
		CheckpointOrigin: domain.ConversationCheckpointOriginCoordination,
		CoordinationID:   id,
	}
}

func (c *commandContext) postActivityHook(ctx context.Context, path string, req setActivityAPIRequest) error {
	for attempt := 0; ; attempt++ {
		err := c.postJSON(ctx, path, req, nil)
		var response apiResponseError
		if attempt >= 3 || !errors.As(err, &response) || response.StatusCode != http.StatusServiceUnavailable ||
			response.ErrorBody.Code != "ACTIVITY_PROJECTION_BUSY" {
			return err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func isCursorPermissionHook(agent, event string) bool {
	if domain.AgentHarness(agent) != domain.HarnessCursor {
		return false
	}
	switch event {
	case "before-shell-execution", "before-mcp-execution":
		return true
	default:
		return false
	}
}

func (c *commandContext) runCursorPermissionHook(ctx context.Context, agent, event, sessionID string, payload []byte) error {
	mode := ports.PermissionMode(strings.TrimSpace(os.Getenv(cursor.EnvPermissionMode)))
	decision := cursor.EvaluatePermission(mode, event, payload)

	launchID := validLaunchID(os.Getenv("AO_RUNTIME_LAUNCH_ID"))
	if launchID == "" {
		launchID = validLaunchID(hookLaunchID(payload))
	}

	path := "sessions/" + url.PathEscape(sessionID) + "/activity"
	req := setActivityAPIRequest{
		State:          string(decision.State),
		Event:          event,
		ToolName:       cursor.HookToolName(event, payload),
		AgentSessionID: hookAgentSessionID(payload),
		LaunchID:       launchID,
	}
	if err := c.postActivityHook(ctx, path, req); err != nil {
		c.reportHookFailure(agent, event, sessionID, err)
		if decision.Permission == "ask" {
			return fmt.Errorf("persist blocked Cursor activity: %w", err)
		}
	}

	out := cursorPermissionHookOutput{Permission: decision.Permission}
	if err := json.NewEncoder(c.deps.Out).Encode(out); err != nil {
		c.reportHookFailure(agent, event, sessionID, fmt.Errorf("write permission response: %w", err))
	}
	return nil
}

func isAgyModernHookEvent(agent, event string) bool {
	if domain.AgentHarness(agent) != domain.HarnessAgy {
		return false
	}
	switch event {
	case "pre-invocation", "post-tool-use", "stop":
		return true
	default:
		return false
	}
}

func (c *commandContext) runReviewHook(ctx context.Context, agent, event, reviewSessionID string) error {
	var payload []byte
	if hookReadsStdin(agent, event) {
		var err error
		payload, err = io.ReadAll(c.deps.In)
		if err != nil {
			c.reportHookFailure(agent, event, reviewSessionID, fmt.Errorf("read stdin: %w", err))
		}
	}
	if domain.AgentHarness(agent) == domain.HarnessClaudeCode && event == "permission-request" {
		out := reviewerPermissionDecision(payload, strings.TrimSpace(os.Getenv("AO_REVIEW_WORKER_SESSION_ID")))
		if err := json.NewEncoder(c.deps.Out).Encode(out); err != nil {
			c.reportHookFailure(agent, event, reviewSessionID, fmt.Errorf("write permission response: %w", err))
		}
		return nil
	}
	state, hasActivity := activitydispatch.Derive(agent, event, payload)
	agentSessionID := hookAgentSessionIDForHarness(agent, payload)
	if !hasActivity && agentSessionID == "" {
		return nil
	}
	launchID := validLaunchID(os.Getenv("AO_RUNTIME_LAUNCH_ID"))
	if launchID == "" {
		launchID = validLaunchID(hookLaunchID(payload))
	}
	path := "reviews/" + url.PathEscape(reviewSessionID) + "/activity"
	req := setReviewActivityAPIRequest{
		Event:          event,
		AgentSessionID: agentSessionID,
		LaunchID:       launchID,
	}
	if hasActivity {
		req.State = string(state)
	}
	if err := c.postJSON(ctx, path, req, nil); err != nil {
		c.reportHookFailure(agent, event, reviewSessionID, err)
	}
	return nil
}

func hookReadsStdin(agent, event string) bool {
	return agent != "aider" || event != "notification"
}

func validLaunchID(value string) string {
	value = strings.TrimSpace(value)
	if !sessionIDPattern.MatchString(value) {
		return ""
	}
	return value
}

type openHandsContextHookOutput struct {
	AdditionalContext string `json:"additionalContext"`
}

func shouldEmitSessionStartContext(agent, event string) bool {
	if agent == "gemini" {
		return event == "user-prompt-submit"
	}
	if agent == string(domain.HarnessOpenHands) {
		return event == "user-prompt-submit"
	}
	if event != "session-start" {
		return false
	}
	switch agent {
	case "agy", "devin":
		return true
	default:
		return false
	}
}

func (c *commandContext) emitSessionStartContext(agent, event, sessionID string) {
	dataDir := strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
	if dataDir == "" {
		return
	}
	path := filepath.Join(dataDir, "prompts", sessionID, "system.md")
	data, err := os.ReadFile(path) //nolint:gosec // sessionID is bounded by sessionIDPattern.
	if err != nil {
		c.reportHookFailure(agent, event, sessionID, fmt.Errorf("read system prompt: %w", err))
		return
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return
	}
	var out any
	if agent == string(domain.HarnessOpenHands) {
		out = openHandsContextHookOutput{AdditionalContext: prompt}
	} else {
		var start sessionStartHookOutput
		start.HookSpecificOutput.HookEventName = "SessionStart"
		if agent == "gemini" {
			start.HookSpecificOutput.HookEventName = "BeforeAgent"
		}
		start.HookSpecificOutput.AdditionalContext = prompt
		out = start
	}
	if err := json.NewEncoder(c.deps.Out).Encode(out); err != nil {
		c.reportHookFailure(agent, event, sessionID, fmt.Errorf("write session-start context: %w", err))
	}
}

func (c *commandContext) reportHookFailure(agent, event, sessionID string, cause error) {
	msg := fmt.Sprintf("ao hooks %s %s: %v", agent, event, cause)
	if !errors.Is(cause, errDaemonNotRunning) {
		_, _ = fmt.Fprintln(c.deps.Err, msg)
	}
	dataDir := strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
	if dataDir == "" {
		return
	}
	line := fmt.Sprintf("%s session=%s %s\n", time.Now().UTC().Format(time.RFC3339), sessionID, msg)
	appendHooksLog(dataDir, line)
}

func appendHooksLog(dataDir, line string) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return
	}
	path := filepath.Join(dataDir, hooksLogName)
	flags := os.O_APPEND | os.O_CREATE | os.O_WRONLY
	if info, err := os.Stat(path); err == nil && info.Size() > maxHooksLogBytes {
		flags = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
	}
	f, err := os.OpenFile(path, flags, 0o600) //nolint:gosec // path is rooted in AO's own data dir
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
}
