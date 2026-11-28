package devices

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ResolveInput is everything the resolver weighs when a session asks for a
// device. Only the local rung has a provider today; the remote-host and cloud
// rungs are part of the ladder so adding a provider means filling an input,
// not restructuring the decision.
type ResolveInput struct {
	// Requested is the platform the caller asked for, or empty.
	Requested ports.DevicePlatform
	// Pinned is a provider the caller or project forced, or empty.
	Pinned string
	// ProjectPlatforms is what the session's workspace looks like it builds.
	ProjectPlatforms []ports.DevicePlatform
	// Local is the local provider's platform availability.
	Local []ports.DevicePlatformAvailability
	// LocalSlotFree is false when local devices are at capacity.
	LocalSlotFree bool
	// LocalCap is how many local devices run at once, and LocalHolders the
	// sessions holding them, so a full machine says who to ask.
	LocalCap     int
	LocalHolders []string
	// RemoteHosts and CloudProviders list configured providers by platform.
	RemoteHosts    map[ports.DevicePlatform][]string
	CloudProviders map[ports.DevicePlatform][]string
	CloudAllowed   bool
}

// Decision is the resolver's pick and the reason shown to the user.
type Decision struct {
	Platform ports.DevicePlatform
	Provider string
	Reason   string
	// Fallback is set when no provider can serve the request; it says what is
	// missing and what the user can do instead.
	Fallback string
}

// OK reports whether the decision names a provider.
func (d Decision) OK() bool { return d.Provider != "" }

// Resolve walks the ladder: pinned provider, local, remote host, cloud,
// fallback. The closest and cheapest device wins.
func Resolve(in ResolveInput) Decision {
	platform := pickPlatform(in)
	local := availability(in.Local, platform)
	if in.Pinned != "" {
		if in.Pinned == "local" && !local.Available {
			return Decision{Platform: platform, Fallback: "The pinned local provider cannot run " + platformLabel(platform) + ": " + local.Reason}
		}
		return Decision{Platform: platform, Provider: in.Pinned, Reason: "Pinned to " + in.Pinned}
	}
	if local.Available && in.LocalSlotFree {
		return Decision{Platform: platform, Provider: "local", Reason: "Runs on this machine"}
	}
	if hosts := in.RemoteHosts[platform]; len(hosts) > 0 {
		return Decision{Platform: platform, Provider: hosts[0], Reason: "This machine cannot run it; using remote host " + hosts[0]}
	}
	if clouds := in.CloudProviders[platform]; in.CloudAllowed && len(clouds) > 0 {
		return Decision{Platform: platform, Provider: clouds[0], Reason: "No local or remote device; using " + clouds[0]}
	}
	if local.Available && !in.LocalSlotFree {
		// The machine has free simulators; what is full is how many run at
		// once (each holds several GB of memory). Say so, and who holds them.
		msg := fmt.Sprintf("This machine already runs %d devices for other sessions, the most it runs at once", in.LocalCap)
		if len(in.LocalHolders) > 0 {
			msg += " (" + strings.Join(in.LocalHolders, ", ") + ")"
		}
		return Decision{Platform: platform, Fallback: msg + ". Release one of them, or end its session, to open another."}
	}
	missing := local.Reason
	return Decision{
		Platform: platform,
		Fallback: missing + " No remote host or cloud provider is configured. For Expo apps, the Expo Go QR code still works on your own phone.",
	}
}

// pickPlatform uses the requested platform, else the project's single
// platform, else the first project platform this machine can run.
func pickPlatform(in ResolveInput) ports.DevicePlatform {
	if in.Requested != "" {
		return in.Requested
	}
	candidates := in.ProjectPlatforms
	if len(candidates) == 0 {
		candidates = []ports.DevicePlatform{ports.DevicePlatformIOS, ports.DevicePlatformAndroid}
	}
	for _, p := range candidates {
		if availability(in.Local, p).Available {
			return p
		}
	}
	return candidates[0]
}

func availability(all []ports.DevicePlatformAvailability, platform ports.DevicePlatform) ports.DevicePlatformAvailability {
	for _, a := range all {
		if a.Platform == platform {
			return a
		}
	}
	return ports.DevicePlatformAvailability{Platform: platform, Reason: platformLabel(platform) + " is not supported here."}
}

func platformLabel(p ports.DevicePlatform) string {
	switch p {
	case ports.DevicePlatformIOS:
		return "iOS"
	case ports.DevicePlatformAndroid:
		return "Android"
	}
	return string(p)
}

// DetectPlatforms guesses which platforms a workspace builds from its files:
// Expo and Flutter projects build both, an Xcode project builds iOS, and a
// Gradle app module builds Android.
func DetectPlatforms(workspace string) []ports.DevicePlatform {
	if workspace == "" {
		return nil
	}
	exists := func(rel ...string) bool {
		_, err := os.Stat(filepath.Join(append([]string{workspace}, rel...)...))
		return err == nil
	}
	glob := func(pattern string) bool {
		matches, _ := filepath.Glob(filepath.Join(workspace, pattern))
		return len(matches) > 0
	}
	if exists("app.json") || exists("app.config.js") || exists("app.config.ts") || exists("pubspec.yaml") {
		return []ports.DevicePlatform{ports.DevicePlatformIOS, ports.DevicePlatformAndroid}
	}
	var out []ports.DevicePlatform
	if glob("*.xcodeproj") || glob("ios/*.xcodeproj") || glob("ios/*.xcworkspace") {
		out = append(out, ports.DevicePlatformIOS)
	}
	if exists("android", "app", "build.gradle") || exists("android", "app", "build.gradle.kts") ||
		exists("app", "build.gradle") || exists("app", "build.gradle.kts") {
		out = append(out, ports.DevicePlatformAndroid)
	}
	return slices.Clip(out)
}
