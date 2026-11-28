package mobilebridge

import (
	"context"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// bonjourTimeout bounds the scutil call: Status is polled while the Connect
// Mobile modal is open and must never stall on it.
const bonjourTimeout = 2 * time.Second

// bonjourCacheFor is how long a looked-up name is reused. The name changes
// only when the user renames the Mac, and Status is polled every few seconds.
const bonjourCacheFor = time.Minute

var dnsLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

var bonjourCache struct {
	sync.Mutex
	name string
	at   time.Time
}

// LocalLANHosts is what the daemon advertises as LAN endpoints: every private
// IPv4 address, then this machine's Bonjour name when it has one.
//
// The name is what lets a phone find the machine again after a DHCP lease
// moves it to a new address. The phone keeps a LAN name across refreshes
// (mobile lib/mergeEndpoints.ts), while the IPs it stored all die with the old
// lease, and an iPhone cannot fall back to Tailscale over plain HTTP.
func LocalLANHosts() []string {
	return withBonjourName(LocalPrivateIPv4s(), LocalBonjourName())
}

func withBonjourName(ips []string, name string) []string {
	if len(ips) == 0 || name == "" {
		return ips
	}
	return append(append([]string(nil), ips...), name)
}

// LocalBonjourName returns this Mac's mDNS name ("Studio.local"), or "" off
// macOS or when it cannot be read. Only macOS is guaranteed to answer mDNS for
// its own name; elsewhere it depends on a responder (Avahi) that may not run.
func LocalBonjourName() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	bonjourCache.Lock()
	defer bonjourCache.Unlock()
	if !bonjourCache.at.IsZero() && time.Since(bonjourCache.at) < bonjourCacheFor {
		return bonjourCache.name
	}
	bonjourCache.name = queryBonjourName(context.Background(), func(ctx context.Context) ([]byte, error) {
		return aoprocess.CommandContext(ctx, "scutil", "--get", "LocalHostName").Output()
	})
	bonjourCache.at = time.Now()
	return bonjourCache.name
}

func queryBonjourName(ctx context.Context, run func(context.Context) ([]byte, error)) string {
	ctx, cancel := context.WithTimeout(ctx, bonjourTimeout)
	defer cancel()
	out, err := run(ctx)
	if err != nil {
		return ""
	}
	label := strings.TrimSpace(string(out))
	if !dnsLabel.MatchString(label) {
		return ""
	}
	return label + ".local"
}
