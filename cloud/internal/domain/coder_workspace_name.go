package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

const (
	// MaxCoderWorkspaceName is Coder's workspace name length limit.
	MaxCoderWorkspaceName = 32
	// coderWorkspaceNameIDLength is the session-id segment of a readable name,
	// as long as the 12 characters of the earlier <prefix>-<id> names.
	coderWorkspaceNameIDLength = 12
	// coderWorkspaceRetryIDLength is the shortest id segment of the retry
	// name when the first name already used all 32 characters.
	coderWorkspaceRetryIDLength = coderWorkspaceNameIDLength + 4
	// maxCoderWorkspaceNameWord bounds the chat-name word of a worker's name.
	maxCoderWorkspaceNameWord = 10
	// minCoderWorkspaceNameWord is how much of the word survives a long prefix:
	// the prefix is shortened before the word drops below it.
	minCoderWorkspaceNameWord = 3
	// defaultCoderWorkspaceNamePrefix names workspaces of a project with no
	// workspace name prefix.
	defaultCoderWorkspaceNamePrefix = "ao"
)

var coderWorkspaceNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateCoderWorkspaceName accepts a name Coder takes for a workspace: at
// most 32 lowercase letters, digits and single hyphens, with no leading or
// trailing hyphen.
func ValidateCoderWorkspaceName(name string) error {
	if len(name) > MaxCoderWorkspaceName || !coderWorkspaceNamePattern.MatchString(name) {
		return errors.New("Coder workspace name must be 1-32 lowercase letters, digits, or single hyphens, with no leading or trailing hyphen")
	}
	return nil
}

// CoderWorkspaceName is the readable Coder workspace name AO gives a new
// session's workspace: <prefix>-o-<id> for an orchestrator and
// <prefix>-w-<word>-<id> for a worker, where <word> is the first word of the
// session's display name and <id> the first 12 characters of the session id.
// An empty or invalid prefix uses "ao"; a display name with no usable word
// omits the word. When the name does not fit Coder's 32 characters, the word
// is shortened first, down to 3 characters, then the prefix; the role marker
// and the id are always kept. Prefixes are at most 20 characters, so the
// shortened prefix keeps at least 13 of them and the id never has to shrink.
// The name is chosen once, when the session is created, and stored on its
// sandbox profile; it never changes afterwards, even if the session is
// renamed. See CoderWorkspaceNames for the name a create conflict retries.
func CoderWorkspaceName(prefix, kind, displayName, sessionID string) string {
	name, _ := CoderWorkspaceNames(prefix, kind, displayName, sessionID)
	return name
}

// CoderWorkspaceNames returns CoderWorkspaceName and the one name AO retries
// with when Coder rejects the first as already taken by another workspace:
// the same prefix, role and word with a longer id, filling the rest of the 32
// characters. When the first name already uses all 32, the word and then the
// prefix are shortened to make room for a 16-character id. The two names
// always differ.
//
// Readable names never collide with the <prefix>-<id> and ao-<id> names of
// sessions created before them in a way the lookup could confuse: AO looks a
// workspace up only by the exact name stored for its own session, and a
// workspace that held that name before the session chose it is never
// adopted (see the Coder provider's FindBySession).
func CoderWorkspaceNames(prefix, kind, displayName, sessionID string) (name, retry string) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || ValidateCoderWorkspaceNamePrefix(prefix) != nil {
		prefix = defaultCoderWorkspaceNamePrefix
	}
	role, word := "w", coderWorkspaceNameWord(displayName)
	if kind == "orchestrator" {
		role, word = "o", ""
	}
	prefix, word = fitCoderWorkspaceName(prefix, role, word, coderWorkspaceNameIDLength)
	name = joinCoderWorkspaceName(prefix, role, word, coderWorkspaceNameID(sessionID, coderWorkspaceNameIDLength))
	retryID := MaxCoderWorkspaceName - len(name) + coderWorkspaceNameIDLength
	if retryID <= coderWorkspaceNameIDLength {
		retryID = coderWorkspaceRetryIDLength
		prefix, word = fitCoderWorkspaceName(prefix, role, word, retryID)
	}
	return name, joinCoderWorkspaceName(prefix, role, word, coderWorkspaceNameID(sessionID, retryID))
}

// fitCoderWorkspaceName shortens the word (down to 3 characters) and then the
// prefix so <prefix>-<role>[-<word>]-<id> fits Coder's limit with an id of
// idLength characters.
func fitCoderWorkspaceName(prefix, role, word string, idLength int) (string, string) {
	// What remains for <prefix> and, for a worker, "-<word>".
	room := MaxCoderWorkspaceName - len("-"+role+"-") - idLength
	if word != "" && len(prefix)+1+len(word) > room {
		word = word[:max(min(len(word), minCoderWorkspaceNameWord), room-1-len(prefix))]
	}
	keep := room
	if word != "" {
		keep -= 1 + len(word)
	}
	if len(prefix) > keep {
		prefix = strings.TrimRight(prefix[:keep], "-")
	}
	return prefix, word
}

func joinCoderWorkspaceName(prefix, role, word, id string) string {
	name := prefix + "-" + role
	if word != "" {
		name += "-" + word
	}
	return name + "-" + id
}

// coderWorkspaceNameWord is the first run of ASCII letters and digits in a
// display name, lower-cased and at most maxCoderWorkspaceNameWord characters;
// "" when the name has none.
func coderWorkspaceNameWord(displayName string) string {
	var word strings.Builder
	for _, character := range strings.ToLower(displayName) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			word.WriteRune(character)
			if word.Len() == maxCoderWorkspaceNameWord {
				break
			}
			continue
		}
		if word.Len() > 0 {
			break
		}
	}
	return word.String()
}

// coderWorkspaceNameID is the leading length letters and digits of the
// session id (random hex for the v4 UUIDs AO issues), or of its SHA-256 when
// the id has fewer.
func coderWorkspaceNameID(sessionID string, length int) string {
	sessionID = strings.TrimSpace(sessionID)
	var compact strings.Builder
	for _, character := range strings.ToLower(sessionID) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			compact.WriteRune(character)
			if compact.Len() == length {
				return compact.String()
			}
		}
	}
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])[:length]
}
