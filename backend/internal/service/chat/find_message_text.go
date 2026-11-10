package chat

import (
	"crypto/sha256"
	"regexp"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var findAttachmentSuffix = regexp.MustCompile(`(?:\n\n|^)(?:Attached files \(read these files in the workspace(?: for context)?\)|Attached images \(read these files in the workspace for visual context\)):\n((?:- [^\n]+(?:\n|$))+)$`)
var findStagedPath = regexp.MustCompile(`^\.ao/attachments/(?:attachment|image)-[A-Za-z0-9][A-Za-z0-9._-]*$`)
var findInlineImage = regexp.MustCompile(`\.ao/attachments/(?:attachment|image)-[A-Za-z0-9][A-Za-z0-9._-]*\.(?i:png|jpe?g|gif|webp|bmp)`)

const maxFindBlockCacheBytes = 16 << 20

// findBlockCache keeps parsed settled messages, so a re-search of a long
// transcript does not run Markdown over all of it again. Blocks depend only on
// the text, so its hash is the key. Cached slices are shared and read-only.
var findBlockCache = struct {
	sync.Mutex
	blocks map[[sha256.Size]byte][]string
	bytes  int
}{blocks: map[[sha256.Size]byte][]string{}}

func findMessageBlocks(message domain.ConversationMessage) []string {
	source := findMessageText(message)
	var parse func(string) []string
	switch message.Role {
	case domain.MessageRoleAssistant:
		parse = func(source string) []string { return FindBlocks(source, true) }
	case domain.MessageRoleUser:
		parse = FindUserBlocks
	default:
		return FindBlocks(source, false)
	}
	if message.Streaming {
		return parse(source)
	}
	// The same text splits differently as a reply and as a person's message.
	key := sha256.Sum256([]byte(string(message.Role) + "\x00" + source))
	findBlockCache.Lock()
	blocks, ok := findBlockCache.blocks[key]
	findBlockCache.Unlock()
	if ok {
		return blocks
	}
	blocks = parse(source)
	size := 0
	for _, block := range blocks {
		size += len(block)
	}
	if size <= maxFindBlockCacheBytes {
		findBlockCache.Lock()
		if findBlockCache.bytes+size > maxFindBlockCacheBytes {
			findBlockCache.blocks, findBlockCache.bytes = map[[sha256.Size]byte][]string{}, 0
		}
		if _, exists := findBlockCache.blocks[key]; !exists {
			findBlockCache.blocks[key] = blocks
			findBlockCache.bytes += size
		}
		findBlockCache.Unlock()
	}
	return blocks
}

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
