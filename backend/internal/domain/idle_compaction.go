package domain

import "time"

// IdleCompactionMode is when an idle Claude Chat compacts its history.
//
// Claude keeps a conversation's prompt cache for an hour on a subscription and
// five minutes on an API key or usage credits. The first request after that
// reprocesses the whole history at full price, which on a long conversation
// costs many times a normal turn. Compacting shrinks what that request carries.
// Claude Code does this itself in an interactive terminal (its idleCompaction
// setting), but not in the agent SDK that Chat runs, so AO does it for Chat.
type IdleCompactionMode string

const (
	// IdleCompactionOnReturn compacts first when a message arrives after the
	// cache expired. The default: it never adds a request the user did not
	// cause, and every later request in the turn reads a short history.
	IdleCompactionOnReturn IdleCompactionMode = "onReturn"
	// IdleCompactionBeforeCacheExpires compacts shortly before the cache
	// expires, while reading the history is still cheap. Beta: it compacts
	// conversations nobody returns to as well.
	IdleCompactionBeforeCacheExpires IdleCompactionMode = "beforeCacheExpires"
	// IdleCompactionOff never compacts an idle conversation.
	IdleCompactionOff IdleCompactionMode = "off"
)

// IdleCompactionMinContextTokens is the context size below which an idle
// conversation is left alone. Below it a cold re-read is cheap, so compacting
// mostly loses context for little saving. Matches Claude Code's own
// idleCompaction default.
const IdleCompactionMinContextTokens = 200_000

// IdleCompactionLead is how long before the cache expires a beforeCacheExpires
// compaction starts: enough for a hibernated session to resume and send the
// compaction while the cache is still warm.
const IdleCompactionLead = 2 * time.Minute

// IdleCompactionFallbackTTL is the cache lifetime assumed when the last request
// did not report one (a request served fully from cache writes no entry). It
// is Claude's one-hour cache plus slack, so an unknown lifetime only compacts
// a conversation that is certainly cold.
const IdleCompactionFallbackTTL = 70 * time.Minute

// Valid reports whether m is a known mode.
func (m IdleCompactionMode) Valid() bool {
	switch m {
	case IdleCompactionOnReturn, IdleCompactionBeforeCacheExpires, IdleCompactionOff:
		return true
	}
	return false
}

// NormalizeIdleCompaction maps a stored value to a known mode. A value written
// by a newer build resolves to the default rather than to a mode this build
// cannot run.
func NormalizeIdleCompaction(value string) IdleCompactionMode {
	if mode := IdleCompactionMode(value); mode.Valid() {
		return mode
	}
	return IdleCompactionOnReturn
}
