package chat

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var findAttachmentSuffix = regexp.MustCompile(`(?:\n\n|^)(?:Attached files \(read these files in the workspace(?: for context)?\)|Attached images \(read these files in the workspace for visual context\)):\n((?:- [^\n]+(?:\n|$))+)$`)
var findStagedPath = regexp.MustCompile(`^\.ao/attachments/(?:attachment|image)-[A-Za-z0-9][A-Za-z0-9._-]*$`)
var findInlineImage = regexp.MustCompile(`\.ao/attachments/(?:attachment|image)-[A-Za-z0-9][A-Za-z0-9._-]*\.(?i:png|jpe?g|gif|webp|bmp)`)

func findMessageText(message domain.ConversationMessage) string {
	source := message.Text
	if message.Role != domain.MessageRoleUser {
		return source
	}
	if strings.HasSuffix(source, "</ao-worker-reports>") {
		if index := strings.LastIndex(source, "\n\n<ao-worker-reports>\n"); index >= 0 {
			source = source[:index]
		}
	}
	if match := findAttachmentSuffix.FindStringSubmatchIndex(source); match != nil {
		valid := true
		for _, line := range strings.Split(strings.TrimRight(source[match[2]:match[3]], "\n"), "\n") {
			if !findStagedPath.MatchString(strings.TrimPrefix(line, "- ")) {
				valid = false
				break
			}
		}
		if valid {
			source = source[:match[0]]
		}
	}
	var visible strings.Builder
	from := 0
	for _, match := range findInlineImage.FindAllStringIndex(source, -1) {
		if match[0] > 0 && source[match[0]-1] == '/' {
			continue
		}
		if match[1] < len(source) {
			next := source[match[1]]
			if next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z' || next >= '0' && next <= '9' || next == '_' || next == '-' {
				continue
			}
		}
		visible.WriteString(source[from:match[0]])
		from = match[1]
	}
	visible.WriteString(source[from:])
	return visible.String()
}
