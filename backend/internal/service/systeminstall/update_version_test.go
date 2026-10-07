package systeminstall

import "testing"

func TestFindUpdateVersionAcceptsSupportedForms(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantDisplay string
		wantCore    [4]uint64
		wantParts   int
		wantPre     []string
		wantBuild   string
	}{
		{name: "two components", input: "tool v1.2\n", wantDisplay: "1.2", wantCore: [4]uint64{1, 2}, wantParts: 2},
		{name: "three components", input: "tool 1.2.3", wantDisplay: "1.2.3", wantCore: [4]uint64{1, 2, 3}, wantParts: 3},
		{name: "copilot sentence", input: "GitHub Copilot CLI 1.0.91.\nRun 'copilot update' to check for updates.", wantDisplay: "1.0.91", wantCore: [4]uint64{1, 0, 91}, wantParts: 3},
		{name: "sentence at end", input: "CLI 1.2.3.", wantDisplay: "1.2.3", wantCore: [4]uint64{1, 2, 3}, wantParts: 3},
		{name: "four components", input: "release=1.2.3.4", wantDisplay: "1.2.3.4", wantCore: [4]uint64{1, 2, 3, 4}, wantParts: 4},
		{name: "prerelease and build", input: "cli 1.2.3-beta.2+build.7", wantDisplay: "1.2.3-beta.2+build.7", wantCore: [4]uint64{1, 2, 3}, wantParts: 3, wantPre: []string{"beta", "2"}, wantBuild: "build.7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findUpdateVersion(tt.input)
			if !ok {
				t.Fatalf("findUpdateVersion(%q) did not find a version", tt.input)
			}
			if got.display != tt.wantDisplay || got.core != tt.wantCore || got.parts != tt.wantParts || got.build != tt.wantBuild {
				t.Fatalf("version = %+v, want display=%q core=%v parts=%d build=%q", got, tt.wantDisplay, tt.wantCore, tt.wantParts, tt.wantBuild)
			}
			if len(got.prerelease) != len(tt.wantPre) {
				t.Fatalf("prerelease = %v, want %v", got.prerelease, tt.wantPre)
			}
			for index := range tt.wantPre {
				if got.prerelease[index] != tt.wantPre[index] {
					t.Fatalf("prerelease = %v, want %v", got.prerelease, tt.wantPre)
				}
			}
		})
	}

	for _, input := range []string{"tool development", "1", "1.2.3.4.5", "1.2-beta..2"} {
		if _, ok := parseUpdateVersion(input); ok {
			t.Fatalf("parseUpdateVersion(%q) unexpectedly succeeded", input)
		}
	}
}

func TestFindUpdateVersionDoesNotSliceMalformedOrAmbiguousVersions(t *testing.T) {
	for _, input := range []string{"CLI 1.2.3.4.5", "CLI 1.2.3..", "CLI 1.2.3.foo", "CLI 1.2-beta..2", "CLI 1.2.3.\nRuntime 4.5.6."} {
		if got, ok := findUpdateVersion(input); ok {
			t.Errorf("findUpdateVersion(%q) = %q, want unknown", input, got.display)
		}
	}
}

func TestCompareUpdateVersionsRequiresCompatibleChannels(t *testing.T) {
	tests := []struct {
		name           string
		installed      string
		latest         string
		wantOrder      int
		wantComparable bool
	}{
		{name: "missing core component is zero", installed: "1.2", latest: "1.2.0", wantComparable: true},
		{name: "fourth component participates", installed: "1.2.3.4", latest: "1.2.3.5", wantOrder: -1, wantComparable: true},
		{name: "numeric prerelease identifiers", installed: "1.2.3-beta.2", latest: "1.2.3-beta.10", wantOrder: -1, wantComparable: true},
		{name: "build metadata ignored", installed: "1.2.3+build.1", latest: "1.2.3+build.2", wantComparable: true},
		{name: "stable follows prerelease", installed: "1.2.3-beta.2", latest: "1.2.3", wantOrder: -1, wantComparable: true},
		{name: "different prerelease channels", installed: "1.2.3-beta.2", latest: "1.2.3-rc.1", wantComparable: false},
		{name: "different channels and different cores", installed: "1.2.3-beta.2", latest: "2.0.0-rc.1", wantComparable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installed, ok := parseUpdateVersion(tt.installed)
			if !ok {
				t.Fatalf("could not parse installed fixture %q", tt.installed)
			}
			latest, ok := parseUpdateVersion(tt.latest)
			if !ok {
				t.Fatalf("could not parse latest fixture %q", tt.latest)
			}
			order, versionsComparable := compareUpdateVersions(installed, latest)
			if order != tt.wantOrder || versionsComparable != tt.wantComparable {
				t.Fatalf("compare(%q, %q) = (%d, %t), want (%d, %t)", tt.installed, tt.latest, order, versionsComparable, tt.wantOrder, tt.wantComparable)
			}
		})
	}
}
