package devices

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

var (
	iosOK         = ports.DevicePlatformAvailability{Platform: ports.DevicePlatformIOS, Available: true}
	androidOK     = ports.DevicePlatformAvailability{Platform: ports.DevicePlatformAndroid, Available: true}
	iosMissing    = ports.DevicePlatformAvailability{Platform: ports.DevicePlatformIOS, Reason: "iOS Simulators need macOS with Xcode."}
	androidMissng = ports.DevicePlatformAvailability{Platform: ports.DevicePlatformAndroid, Reason: "Install the Android SDK."}
)

func TestResolveLadder(t *testing.T) {
	tests := []struct {
		name         string
		in           ResolveInput
		wantProvider string
		wantPlatform ports.DevicePlatform
		wantFallback bool
	}{
		{
			name:         "local machine runs the requested platform",
			in:           ResolveInput{Requested: ports.DevicePlatformIOS, Local: []ports.DevicePlatformAvailability{iosOK, androidOK}, LocalSlotFree: true},
			wantProvider: "local", wantPlatform: ports.DevicePlatformIOS,
		},
		{
			name:         "pinned provider wins",
			in:           ResolveInput{Pinned: "argent", Requested: ports.DevicePlatformIOS, Local: []ports.DevicePlatformAvailability{iosOK}, LocalSlotFree: true},
			wantProvider: "argent", wantPlatform: ports.DevicePlatformIOS,
		},
		{
			name:         "pinned local that cannot run the platform falls back",
			in:           ResolveInput{Pinned: "local", Requested: ports.DevicePlatformIOS, Local: []ports.DevicePlatformAvailability{iosMissing}},
			wantPlatform: ports.DevicePlatformIOS, wantFallback: true,
		},
		{
			name: "no local iOS uses a remote host",
			in: ResolveInput{
				Requested: ports.DevicePlatformIOS, Local: []ports.DevicePlatformAvailability{iosMissing, androidOK}, LocalSlotFree: true,
				RemoteHosts: map[ports.DevicePlatform][]string{ports.DevicePlatformIOS: {"mac-mini"}},
			},
			wantProvider: "mac-mini", wantPlatform: ports.DevicePlatformIOS,
		},
		{
			name: "full local slots overflow to an allowed cloud",
			in: ResolveInput{
				Requested: ports.DevicePlatformIOS, Local: []ports.DevicePlatformAvailability{iosOK}, LocalSlotFree: false,
				CloudProviders: map[ports.DevicePlatform][]string{ports.DevicePlatformIOS: {"argent"}}, CloudAllowed: true,
			},
			wantProvider: "argent", wantPlatform: ports.DevicePlatformIOS,
		},
		{
			name: "cloud configured but not allowed falls back",
			in: ResolveInput{
				Requested: ports.DevicePlatformIOS, Local: []ports.DevicePlatformAvailability{iosMissing},
				CloudProviders: map[ports.DevicePlatform][]string{ports.DevicePlatformIOS: {"argent"}},
			},
			wantPlatform: ports.DevicePlatformIOS, wantFallback: true,
		},
		{
			name:         "project platform this machine can run is picked",
			in:           ResolveInput{ProjectPlatforms: []ports.DevicePlatform{ports.DevicePlatformIOS, ports.DevicePlatformAndroid}, Local: []ports.DevicePlatformAvailability{iosMissing, androidOK}, LocalSlotFree: true},
			wantProvider: "local", wantPlatform: ports.DevicePlatformAndroid,
		},
		{
			name:         "nothing available explains what is missing",
			in:           ResolveInput{ProjectPlatforms: []ports.DevicePlatform{ports.DevicePlatformAndroid}, Local: []ports.DevicePlatformAvailability{iosMissing, androidMissng}, LocalSlotFree: true},
			wantPlatform: ports.DevicePlatformAndroid, wantFallback: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.in)
			if got.Provider != tt.wantProvider || got.Platform != tt.wantPlatform {
				t.Fatalf("Resolve() = %+v, want provider %q platform %q", got, tt.wantProvider, tt.wantPlatform)
			}
			if tt.wantFallback != (got.Fallback != "") {
				t.Fatalf("Resolve() fallback = %q, want fallback %v", got.Fallback, tt.wantFallback)
			}
			if got.OK() && got.Reason == "" {
				t.Fatalf("Resolve() picked %q without a reason", got.Provider)
			}
		})
	}
}

func TestDetectPlatforms(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  []ports.DevicePlatform
	}{
		{name: "expo app builds both", files: []string{"app.json"}, want: []ports.DevicePlatform{ports.DevicePlatformIOS, ports.DevicePlatformAndroid}},
		{name: "flutter app builds both", files: []string{"pubspec.yaml"}, want: []ports.DevicePlatform{ports.DevicePlatformIOS, ports.DevicePlatformAndroid}},
		{name: "native iOS", files: []string{"Demo.xcodeproj/project.pbxproj"}, want: []ports.DevicePlatform{ports.DevicePlatformIOS}},
		{name: "native android", files: []string{"app/build.gradle.kts"}, want: []ports.DevicePlatform{ports.DevicePlatformAndroid}},
		{name: "bare react native", files: []string{"ios/Demo.xcworkspace/contents.xcworkspacedata", "android/app/build.gradle"}, want: []ports.DevicePlatform{ports.DevicePlatformIOS, ports.DevicePlatformAndroid}},
		{name: "not a mobile project", files: []string{"go.mod"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				path := filepath.Join(dir, f)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := DetectPlatforms(dir)
			if len(got) != len(tt.want) {
				t.Fatalf("DetectPlatforms() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("DetectPlatforms() = %v, want %v", got, tt.want)
				}
			}
		})
	}
	if got := DetectPlatforms(""); got != nil {
		t.Fatalf("DetectPlatforms(\"\") = %v, want nil", got)
	}
}

// A full machine says what is full (how many devices run at once, not every
// simulator) and which sessions hold them.
func TestResolveFullMachineNamesTheCapAndTheHolders(t *testing.T) {
	got := Resolve(ResolveInput{
		Requested: ports.DevicePlatformIOS, Local: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformIOS, Available: true}},
		LocalSlotFree: false, LocalCap: 3, LocalHolders: []string{"ao-1", "ao-2", "ao-3"},
	})
	want := "This machine already runs 3 devices for other sessions, the most it runs at once (ao-1, ao-2, ao-3). Release one of them, or end its session, to open another."
	if got.OK() || got.Fallback != want {
		t.Fatalf("Resolve() = %+v, want fallback %q", got, want)
	}
}
