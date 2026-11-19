package chat

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPromptSuggestionContextIncludesOnlyEightRecentTextMessages(t *testing.T) {
	messages := []domain.ConversationMessage{{Role: domain.MessageRoleUser, Text: "old context"}}
	for range 8 {
		messages = append(messages, domain.ConversationMessage{Role: domain.MessageRoleAssistant, Text: strings.Repeat("界", 4000)})
	}
	messages = append(messages, domain.ConversationMessage{Role: domain.MessageRole("system"), Text: "system context"})
	transcript := promptSuggestionContext(messages)
	if strings.Contains(transcript, "old context") || strings.Contains(transcript, "system context") ||
		strings.Count(transcript, "assistant: ") != 8 || !utf8.ValidString(transcript) || utf8.RuneCountInString(transcript) > 8*(2048+15) {
		t.Fatalf("context was not bounded: runes=%d", utf8.RuneCountInString(transcript))
	}
}

func TestPromptSuggestionContextKeepsHumanInstructionsAndAssistantEnding(t *testing.T) {
	transcript := promptSuggestionContext([]domain.ConversationMessage{
		{Role: domain.MessageRoleUser, Text: "Implement autocomplete. " + strings.Repeat("x", 4000) + " old ending"},
		{Role: domain.MessageRoleAssistant, Text: "old beginning " + strings.Repeat("x", 4000) + " Add a regression test next."},
	})
	if !strings.Contains(transcript, "Implement autocomplete.") || !strings.Contains(transcript, "Add a regression test next.") ||
		strings.Contains(transcript, "old ending") || strings.Contains(transcript, "old beginning") {
		t.Fatal("bounded context lost the human instructions or assistant conclusion")
	}
}
