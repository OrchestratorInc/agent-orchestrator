package modelcatalog

import (
	"errors"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// codeBuddyCustomPrefix marks a model the user defined in CodeBuddy's
// models.json; CodeBuddy accepts the prefixed ID for --model.
const codeBuddyCustomPrefix = "custom-local:"

// parseCodeBuddyModels reads the runnable model IDs CodeBuddy prints in its
// --model help: "Currently supported: (default-model, glm-5.3, ...)". The list
// follows the signed-in account and the models.json custom models, so it is
// exactly what a session can launch. The help may wrap, so the text after the
// --model flag is read up to the closing parenthesis.
func parseCodeBuddyModels(output []byte) ([]ports.AgentModelInfo, error) {
	text := ansiPattern.ReplaceAllString(string(output), "")
	flag := strings.Index(text, "--model <model>")
	if flag < 0 {
		return nil, errors.New("CodeBuddy help has no --model option")
	}
	text = text[flag:]
	const marker = "Currently supported: ("
	start := strings.Index(text, marker)
	if start < 0 {
		return nil, errors.New("CodeBuddy help lists no supported models")
	}
	text = text[start+len(marker):]
	end := strings.Index(text, ")")
	if end < 0 {
		return nil, errors.New("CodeBuddy supported model list is incomplete")
	}
	models := []ports.AgentModelInfo{}
	for _, field := range strings.Split(text[:end], ",") {
		id := strings.Join(strings.Fields(field), "")
		if id == "" {
			continue
		}
		label := id
		if name, ok := strings.CutPrefix(id, codeBuddyCustomPrefix); ok && name != "" {
			label = name + " (models.json)"
		}
		models = append(models, model(id, label, false))
	}
	return models, nil
}
