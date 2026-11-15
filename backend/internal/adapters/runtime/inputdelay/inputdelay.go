package inputdelay

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	chunkRunes = 512
	burstDelay = time.Second
	burstStep  = 150 * time.Millisecond
	maxDelay   = 2 * time.Second
)

func ForMessage(message string, configured time.Duration) time.Duration {
	if message == "" {
		return 0
	}

	runes := utf8.RuneCountInString(message)
	if !strings.Contains(message, "\n") && runes <= chunkRunes {
		return configured
	}

	chunks := (runes + chunkRunes - 1) / chunkRunes
	delay := burstDelay + time.Duration(chunks-1)*burstStep
	if delay > maxDelay {
		delay = maxDelay
	}
	if configured > delay {
		return configured
	}
	return delay
}
