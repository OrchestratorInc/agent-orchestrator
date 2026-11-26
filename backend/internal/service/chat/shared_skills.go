package chat

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillbridge"
)

// sharedSkillSource marks a `/` entry Orchestrator runs itself: the agent does
// not know the skill, so its instructions go along with the message.
const sharedSkillSource = "orchestrator"

// maxSharedSkills bounds how many skills one message can bring in.
const maxSharedSkills = 4

// sharedSkillToken is one /name at the start of a message.
var sharedSkillToken = regexp.MustCompile(`^\s*/([^\s/]+)(\s|$)`)

// withSharedSkills adds the user's skills the agent did not list itself.
func withSharedSkills(skills []ports.ChatSkill, shared []skillbridge.Skill) []ports.ChatSkill {
	own := skillNameSet(skills)
	for _, skill := range shared {
		if own[strings.ToLower(skill.Name)] || !invocable(skill.Name) {
			continue
		}
		skills = append(skills, ports.ChatSkill{
			Name: skill.Name, DisplayName: skill.Name, Description: skill.Description, Source: sharedSkillSource,
		})
	}
	return skills
}

func skillNameSet(skills []ports.ChatSkill) map[string]bool {
	names := make(map[string]bool, len(skills))
	for _, skill := range skills {
		names[strings.ToLower(strings.TrimPrefix(skill.Name, skillCommandPrefix))] = true
	}
	return names
}

// invocable reports whether a skill's name can be typed as one /name word.
func invocable(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/ \t\r\n")
}

// withSharedSkillInstructions hands the agent the instructions of the skills a
// message starts with and the agent does not have: `/name` invokes a skill only
// there. Only a message from the user's side counts: what they typed, and a
// session's opening prompt, which includes one an orchestrator writes for a
// worker it spawns. A relayed review comment that says /deploy must not run the
// user's skill, nor must an issue quoted inside an opening prompt. A skill
// named twice is added once.
func (c *Controller) withSharedSkillInstructions(ctx context.Context, msg ports.ChatUserMessage) ports.ChatUserMessage {
	return withSharedSkillInstructions(ctx, c.dataDir, c.sessionID, c.conv, msg)
}

func withSharedSkillInstructions(
	ctx context.Context,
	dataDir string,
	id domain.SessionID,
	conv ports.ChatConversation,
	msg ports.ChatUserMessage,
) ports.ChatUserMessage {
	if dataDir == "" || !sharedSkillToken.MatchString(msg.Text) {
		return msg
	}
	if msg.Origin != "" && msg.Origin != domain.MessageOriginHuman && !msg.AuthoredByUser {
		return msg
	}
	shared := map[string]skillbridge.Skill{}
	for _, skill := range skillbridge.ReadIndex(dataDir, string(id)) {
		if invocable(skill.Name) {
			shared[strings.ToLower(skill.Name)] = skill
		}
	}
	if len(shared) == 0 {
		return msg
	}
	rest := msg.Text
	var names, sections []string
	var own map[string]bool
	added := map[string]bool{}
	for len(names) < maxSharedSkills {
		token := sharedSkillToken.FindStringSubmatchIndex(rest)
		if token == nil {
			break
		}
		key := strings.ToLower(rest[token[2]:token[3]])
		skill, ok := shared[key]
		if !ok {
			break
		}
		if added[key] {
			rest = rest[token[3]:]
			continue
		}
		if own == nil {
			own = ownSkillNames(ctx, conv)
		}
		if own[strings.ToLower(skill.Name)] {
			break
		}
		section, ok := sharedSkillSection(skill)
		if !ok {
			break
		}
		added[key] = true
		names = append(names, fmt.Sprintf("%q", skill.Name))
		sections = append(sections, section)
		rest = rest[token[3]:]
	}
	if len(names) == 0 {
		return msg
	}
	// The slash goes: an agent may read /name as a command of its own and refuse
	// one it does not have.
	msg.Text = "Use the " + strings.Join(names, " and ") + " " + plural("skill", len(names)) + "."
	if rest = strings.TrimSpace(rest); rest != "" {
		msg.Text += "\n\n" + rest
	}
	msg.Text += strings.Join(sections, "")
	return msg
}

func plural(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

// ownSkillNames are the skills the agent has itself, which it runs its own way.
// An agent that does not answer is taken to have none of the user's.
func ownSkillNames(ctx context.Context, conv ports.ChatConversation) map[string]bool {
	lister, ok := conv.(ports.ChatSkillLister)
	if !ok {
		return map[string]bool{}
	}
	listCtx, cancel := context.WithTimeout(ctx, skillsListTimeout)
	defer cancel()
	skills, err := lister.ListSkills(listCtx)
	if err != nil {
		return map[string]bool{}
	}
	return skillNameSet(skills)
}

func sharedSkillSection(skill skillbridge.Skill) (string, bool) {
	body, whole := skill.Instructions()
	folder := filepath.ToSlash(skill.Path)
	if !whole {
		return fmt.Sprintf("\n\n<user_skill name=%q folder=%q>\nThe user asked for this skill. Read SKILL.md in its folder and follow it.\n</user_skill>", skill.Name, folder), skill.Exists()
	}
	return fmt.Sprintf("\n\n<user_skill name=%q folder=%q>\nThe user asked for this skill. Follow its instructions; any other file they name is in its folder.\n\n%s\n</user_skill>", skill.Name, folder, body), true
}
