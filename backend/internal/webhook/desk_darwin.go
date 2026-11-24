package webhook

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const desktopBundleID = "dev.agent-orchestrator.desktop"

var idleRE = regexp.MustCompile(`"HIDIdleTime" = (\d+)`)

func deskActive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	front, err := exec.CommandContext(ctx, "lsappinfo", "front").Output()
	if err != nil {
		return false
	}
	info, err := exec.CommandContext(ctx, "lsappinfo", "info", "-only", "bundleid", strings.TrimSpace(string(front))).Output()
	if err != nil || !strings.Contains(string(info), `"`+desktopBundleID+`"`) {
		return false
	}
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem", "-d", "4").Output()
	if err != nil {
		return false
	}
	m := idleRE.FindSubmatch(out)
	if m == nil {
		return false
	}
	ns, err := strconv.ParseInt(string(m[1]), 10, 64)
	return err == nil && time.Duration(ns) < deskIdleLimit
}
