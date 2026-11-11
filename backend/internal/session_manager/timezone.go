package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// localTimeZone returns the machine's IANA time zone name (for example
// "Asia/Kolkata"), or "" when it cannot be resolved. Go reports the system
// zone only as "Local", so this reads TZ first and then the /etc/localtime
// symlink that macOS and most Linux distributions maintain, and finally
// /etc/timezone, which Debian-style systems write when /etc/localtime is a
// plain copy rather than a link.
func localTimeZone() string {
	return resolveTimeZone(os.Getenv("TZ"), os.Readlink, os.ReadFile)
}

func resolveTimeZone(tzEnv string, readlink func(string) (string, error), readFile func(string) ([]byte, error)) string {
	if name := strings.TrimPrefix(strings.TrimSpace(tzEnv), ":"); name != "" {
		// TZ may also name a zone file directly, e.g. /usr/share/zoneinfo/Asia/Tokyo.
		if filepath.IsAbs(name) {
			return zoneFromPath(name)
		}
		if validTimeZone(name) {
			return name
		}
		return ""
	}
	if target, err := readlink("/etc/localtime"); err == nil {
		if name := zoneFromPath(target); name != "" {
			return name
		}
	}
	if data, err := readFile("/etc/timezone"); err == nil {
		if name := strings.TrimSpace(string(data)); validTimeZone(name) {
			return name
		}
	}
	return ""
}

// zoneFromPath extracts the IANA name from a path inside a zoneinfo directory.
func zoneFromPath(path string) string {
	_, name, ok := strings.Cut(filepath.ToSlash(path), "zoneinfo/")
	if !ok || !validTimeZone(name) {
		return ""
	}
	return name
}

// serverDefaultZones are the UTC aliases servers, containers and CI runners
// default to. A person's own machine is almost never set to one (London is
// Europe/London), so on a cloud sandbox or remote host they say nothing about
// the user and the section is omitted rather than asserting UTC is local.
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

// timeZonePrompt tells the agent which zone to show times in. Provider APIs
// such as GitHub return UTC, and without this the agent repeats those
// timestamps verbatim instead of the user's wall-clock time.
func timeZonePrompt(zone string) string {
	if zone == "" {
		return ""
	}
	return "## Time Zone\n\n" +
		"The user's local time zone is " + zone + ". When you mention a time, such as a merge, a CI run or a deadline, give it in this zone. Timestamps from GitHub and most other APIs are in UTC, so convert them first; run `date` if you are unsure of the current offset."
}
