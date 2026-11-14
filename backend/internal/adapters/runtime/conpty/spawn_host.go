//go:build windows || darwin || linux

package conpty

import (
	"regexp"
	"time"
)

var readyRE = regexp.MustCompile(`READY:(\d+) (\d+)`)

const spawnReadyTimeout = 10 * time.Second

const maxCapturedStderr = 8192
