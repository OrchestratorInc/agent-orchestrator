package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
)

func userTimeZone(ctx context.Context, dataDir string) string {
	if ctx.Err() != nil {
		return ""
	}
	if zone := clienttz.Read(ctx, dataDir); zone != "" {
		return zone
	}
	return localTimeZone(ctx)
}

func localTimeZone(ctx context.Context) string {
	return resolveTimeZone(ctx, os.Getenv("TZ"), os.Readlink, os.ReadFile)
}

func resolveTimeZone(ctx context.Context, tzEnv string, readlink func(string) (string, error), readFile func(string) ([]byte, error)) string {
	if name := strings.TrimPrefix(strings.TrimSpace(tzEnv), ":"); name != "" {
		if filepath.IsAbs(name) {
			zone, _ := zoneFromPath(name)
			return zone
		}
		if validTimeZone(name) {
			return name
		}
		return ""
	}
	if ctx.Err() != nil {
		return ""
	}
	if target, err := readlink("/etc/localtime"); err == nil {
		if zone, identified := zoneFromPath(target); identified {
			return zone
		}
	}
	if ctx.Err() != nil {
		return ""
	}
	if data, err := readFile("/etc/timezone"); err == nil {
		if name := strings.TrimSpace(string(data)); validTimeZone(name) {
			return name
		}
	}
	return ""
}

func zoneFromPath(path string) (zone string, identified bool) {
	_, name, ok := strings.Cut(filepath.ToSlash(path), "zoneinfo/")
	if !ok || name == "" {
		return "", false
	}
	if !validTimeZone(name) {
		return "", true
	}
	return name, true
}

var serverDefaultZones = map[string]bool{
	"UTC": true, "UCT": true, "GMT": true, "GMT0": true, "GMT+0": true, "GMT-0": true,
	"Greenwich": true, "Universal": true, "Zulu": true,
}

func validTimeZone(name string) bool {
	if name == "" || name == "Local" || filepath.IsAbs(name) {
		return false
	}
	if serverDefaultZones[name] || strings.HasPrefix(name, "Etc/") {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func timeZonePrompt(zone string) string {
	if zone == "" {
		return ""
	}
	return "## Time Zone\n\n" +
		"The user's local time zone is " + zone + ". When you mention a time, such as a merge, a CI run or a deadline, give it in this zone. Timestamps from GitHub and most other APIs are in UTC, so convert them first; run `date` if you are unsure of the current offset."
}
