package chat

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
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

// sharedSkillToken is a slash command at the start of a message. A skill's
// name may have spaces ("Poteto Mode"), so the name is matched against the
// shared skills' names rather than read up to the first space.
var sharedSkillToken = regexp.MustCompile(`^\s*/[^\s/]`)

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

// invocable reports whether a skill's name can be typed after a slash: one
// line, no slash, and no space at either end. Spaces inside are allowed, as
// agents list skills such as "Poteto Mode" under their frontmatter name.
func invocable(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && !strings.ContainsAny(name, "/\t\r\n")
}

// leadingSkill finds the shared skill whose /name starts text: the longest
// name followed by a space or the end, ignoring case. end is where the name
// stops in text.
func leadingSkill(text string, names []string) (name string, end int, ok bool) {
	start := len(text) - len(strings.TrimLeft(text, " \t\r\n"))
	if !strings.HasPrefix(text[start:], "/") {
		return "", 0, false
	}
	start++
	for _, candidate := range names {
		stop := start + len(candidate)
		if stop > len(text) || !strings.EqualFold(text[start:stop], candidate) {
			continue
		}
		if stop == len(text) || strings.ContainsRune(" \t\r\n", rune(text[stop])) {
			return candidate, stop, true
		}
	}
	return "", 0, false
}

// withSharedSkillInstructions hands the agent the instructions of the skills a
// message starts with and the agent does not have: `/name` invokes a skill only
// there. Only a message from the user's side counts: what they typed, and a
// session's opening prompt, which includes one an orchestrator writes for a
// worker it spawns. A relayed review comment that says /deploy must not run the
// user's skill, nor must an issue quoted inside an opening prompt. A skill
// named twice is added once.
func (c *Controller) withSharedSkillInstructions(ctx context.Context, msg ports.ChatUserMessage) ports.ChatUserMessage {
	return withSharedSkillInstructions(ctx, c.skillSharing, c.dataDir, c.sessionID, c.conv, msg)
}

// sharedSkillIndex is the session's shared skills, or none while the setting
// is off: switching it off takes effect in running chats at once.
func sharedSkillIndex(ctx context.Context, enabled func(context.Context) bool, dataDir string, id domain.SessionID) []skillbridge.Skill {
	if dataDir == "" || enabled == nil || !enabled(ctx) {
		return nil
	}
	return skillbridge.ReadIndex(dataDir, string(id))
}

func withSharedSkillInstructions(
	ctx context.Context,
	enabled func(context.Context) bool,
	dataDir string,
	id domain.SessionID,
	conv ports.ChatConversation,
	msg ports.ChatUserMessage,
) ports.ChatUserMessage {
	if !sharedSkillToken.MatchString(msg.Text) {
		return msg
	}
	if msg.Origin != "" && msg.Origin != domain.MessageOriginHuman && !msg.AuthoredByUser {
		return msg
	}
	shared := map[string]skillbridge.Skill{}
	var sharedNames []string
	for _, skill := range sharedSkillIndex(ctx, enabled, dataDir, id) {
		if key := strings.ToLower(skill.Name); invocable(skill.Name) && shared[key].Name == "" {
			shared[key] = skill
			sharedNames = append(sharedNames, skill.Name)
		}
	}
	if len(shared) == 0 {
		return msg
	}
	// Longest first, so "/deploy prod" names "deploy prod" before "deploy".
	slices.SortStableFunc(sharedNames, func(a, b string) int { return len(b) - len(a) })
	rest := msg.Text
	var names, sections []string
	var own map[string]bool
	added := map[string]bool{}
	for len(names) < maxSharedSkills {
		name, end, ok := leadingSkill(rest, sharedNames)
		if !ok {
			break
		}
		key := strings.ToLower(name)
		skill := shared[key]
		if added[key] {
			rest = rest[end:]
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
		rest = rest[end:]
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
