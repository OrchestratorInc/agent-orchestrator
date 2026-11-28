package domain

import "testing"

func TestConversationContextResetProviderItemID(t *testing.T) {
	const session = SessionID("orchestrator-2")
	if got := ConversationContextResetProviderItemID(session); got != "ao-context-reset:orchestrator-2" {
		t.Fatalf("ConversationContextResetProviderItemID() = %q", got)
	}
}

func TestUserSentMessage(t *testing.T) {
	for _, tc := range []struct {
		name           string
		origin         MessageOrigin
		sender         string
		authoredByUser bool
		want           bool
	}{
		{"typed in the app", MessageOriginHuman, "", false, true},
		{"sent by the user from the CLI", MessageOriginAutomation, "", true, true},
		{"automation", MessageOriginAutomation, "", false, false},
		{"relayed by an agent session", MessageOriginHuman, "worker-1", false, false},
	} {
		if got := UserSentMessage(tc.origin, tc.sender, tc.authoredByUser); got != tc.want {
			t.Errorf("%s: UserSentMessage = %v, want %v", tc.name, got, tc.want)
		}
	}
}
