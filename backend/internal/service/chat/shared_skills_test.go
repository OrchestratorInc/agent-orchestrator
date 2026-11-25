package chat_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillbridge"
)

func shareSkill(t *testing.T, name, body string) skillbridge.Skill {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Does "+name+".\n---\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return skillbridge.Skill{Name: name, Description: "Does " + name + ".", Path: dir}
}

func shareSkills(t *testing.T, h *harness, skills ...skillbridge.Skill) {
	t.Helper()
	if err := skillbridge.WriteIndex(h.rendersDir, string(testSession), skills); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsAddWhatTheAgentDoesNotList(t *testing.T) {
	conv := &skillfulConversation{
		fakeConversation: newFakeConversation(),
		skills:           []ports.ChatSkill{{Name: "review", DisplayName: "review", Source: "agent"}, {Name: "skill:pi-one", Source: "agent"}},
	}
	h := newHarnessWithConversation(t, conv)
	shareSkills(t, h, shareSkill(t, "Review", ""), shareSkill(t, "pi-one", ""), shareSkill(t, "deploy", ""), shareSkill(t, "two words", ""))

	skills, err := h.svc.Skills(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, skill := range skills {
		got = append(got, skill.Name+":"+skill.Source)
	}
	if want := "review:agent skill:pi-one:agent deploy:orchestrator"; strings.Join(got, " ") != want {
		t.Fatalf("skills = %v, want %s", got, want)
	}
}

func TestSkillsForAnAgentThatListsNone(t *testing.T) {
	h := newHarness(t)
	shareSkills(t, h, shareSkill(t, "deploy", ""))
	skills, err := h.svc.Skills(context.Background(), testSession)
	if err != nil || len(skills) != 1 || skills[0].Name != "deploy" || skills[0].Description != "Does deploy." {
		t.Fatalf("skills = %+v, %v", skills, err)
	}
}

func TestSharedSkillInstructionsGoWithTheMessage(t *testing.T) {
	deploy := func(t *testing.T) skillbridge.Skill { return shareSkill(t, "deploy", "Ship it with ./ship.sh") }
	for _, tc := range []struct {
		name   string
		msg    ports.ChatUserMessage
		expand bool
	}{
		{"named first", ports.ChatUserMessage{Text: "/deploy to staging"}, true},
		{"ignoring case", ports.ChatUserMessage{Text: "  /DEPLOY to staging"}, true},
		{"in a sentence", ports.ChatUserMessage{Text: "please /deploy to staging"}, false},
		{"a path", ports.ChatUserMessage{Text: "/deploy/staging.yml is wrong"}, false},
		{"a longer name", ports.ChatUserMessage{Text: "/deploy-all now"}, false},
		{"not shared", ports.ChatUserMessage{Text: "/other to staging"}, false},
		{"relayed by the user", ports.ChatUserMessage{Text: "/deploy to staging", Origin: domain.MessageOriginAutomation, AuthoredByUser: true}, true},
		{"written by someone else", ports.ChatUserMessage{Text: "/deploy to staging", Origin: domain.MessageOriginAutomation}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			skill := deploy(t)
			shareSkills(t, h, skill)
			tc.msg.ClientMessageID = "m-1"
			if tc.msg.Origin == "" {
				tc.msg.Origin = domain.MessageOriginHuman
			}
			if _, err := h.svc.Send(context.Background(), testSession, tc.msg); err != nil {
				t.Fatal(err)
			}
			h.conv.mu.Lock()
			sent := h.conv.sent[len(h.conv.sent)-1].Text
			h.conv.mu.Unlock()
			if !tc.expand {
				if sent != tc.msg.Text {
					t.Fatalf("sent %q, want the message as written", sent)
				}
				return
			}
			if !strings.HasPrefix(sent, "Use the \"deploy\" skill.\n\nto staging\n\n<user_skill name=\"deploy\"") ||
				!strings.Contains(sent, "Ship it with ./ship.sh") || !strings.Contains(sent, filepath.ToSlash(skill.Path)) {
				t.Fatalf("sent %q, want the skill's instructions", sent)
			}
			// The timeline keeps what the user wrote.
			snapshot, err := h.svc.Snapshot(context.Background(), testSession)
			if err != nil || len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != tc.msg.Text {
				t.Fatalf("timeline = %+v, %v, want what the user wrote", snapshot.Messages, err)
			}
		})
	}
}

func TestSharedSkillsTheAgentHasAreLeftToIt(t *testing.T) {
	conv := &skillfulConversation{fakeConversation: newFakeConversation(), skills: []ports.ChatSkill{{Name: "deploy"}}}
	h := newHarnessWithConversation(t, conv)
	shareSkills(t, h, shareSkill(t, "deploy", "Ship it"))
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "/deploy now", ClientMessageID: "m-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	conv.mu.Lock()
	defer conv.mu.Unlock()
	if got := conv.sent[0].Text; got != "/deploy now" {
		t.Fatalf("sent %q, want the agent's own command", got)
	}
}

func TestSharedSkillsSeveralLongAndGone(t *testing.T) {
	h := newHarness(t)
	long := shareSkill(t, "long", strings.Repeat("x", 80<<10))
	gone := shareSkill(t, "gone", "")
	if err := os.RemoveAll(gone.Path); err != nil {
		t.Fatal(err)
	}
	shareSkills(t, h, shareSkill(t, "one", "First."), long, gone)
	send := func(id, text string) string {
		t.Helper()
		if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
			Text: text, ClientMessageID: id, Origin: domain.MessageOriginHuman,
		}); err != nil {
			t.Fatal(err)
		}
		h.conv.mu.Lock()
		defer h.conv.mu.Unlock()
		return h.conv.sent[len(h.conv.sent)-1].Text
	}
	got := send("m-1", "/one /long go")
	if !strings.HasPrefix(got, "Use the \"one\" and \"long\" skills.\n\ngo\n\n") || !strings.Contains(got, "First.") ||
		!strings.Contains(got, "Read SKILL.md in its folder") || len(got) > 4<<10 {
		t.Fatalf("sent %d bytes: %.400q", len(got), got)
	}
}

func TestSharedSkillThatIsGoneIsLeftAsWritten(t *testing.T) {
	h := newHarness(t)
	gone := shareSkill(t, "gone", "")
	if err := os.RemoveAll(gone.Path); err != nil {
		t.Fatal(err)
	}
	shareSkills(t, h, gone)
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "/gone", ClientMessageID: "m-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	h.conv.mu.Lock()
	defer h.conv.mu.Unlock()
	if got := h.conv.sent[0].Text; got != "/gone" {
		t.Fatalf("sent %q", got)
	}
}

func TestSharedSkillInstructionsGoWithASideQuestion(t *testing.T) {
	h := newSideWorkflow(t, "", domain.HarnessOpenCode)
	shareSkills(t, h.harness, shareSkill(t, "deploy", "Ship it with ./ship.sh"))
	side := h.side(t, "skills", true)
	if _, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "/deploy now", ClientMessageID: "first"}); err != nil {
		t.Fatal(err)
	}
	h.awaitSide(t, side.ID, func(domain.SideSnapshot) bool { return h.provider(0).sendCallCount() == 1 })
	provider := h.provider(0)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if got := provider.sent[0].Text; !strings.Contains(got, "Use the \"deploy\" skill.\n\nnow") || !strings.Contains(got, "Ship it with ./ship.sh") {
		t.Fatalf("sent %q", got)
	}
}
