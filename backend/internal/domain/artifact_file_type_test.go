package domain

import "testing"

func TestArtifactFileType(t *testing.T) {
	for _, tc := range []struct {
		path   string
		bundle bool
		want   string
	}{
		{"report.html", false, "html"},
		{"docs/Plan.MD", false, "markdown"},
		{"shots/a.png", false, "image"},
		{"rows.csv", false, "data"},
		{"notes.weird-ext", false, "other"},
		{"Makefile", false, "other"},
		{".env", false, "other"},
		{"brand.icon", true, "bundle"},
	} {
		got := ArtifactFileType(tc.path, tc.bundle)
		if got != tc.want {
			t.Errorf("ArtifactFileType(%q, %v) = %q, want %q", tc.path, tc.bundle, got, tc.want)
		}
		if !ValidArtifactFileType(got) {
			t.Errorf("ArtifactFileType(%q) = %q, which ValidArtifactFileType refuses", tc.path, got)
		}
	}
	for _, hostile := range []string{"", "HTML", "/Users/alice/a.html", "png"} {
		if ValidArtifactFileType(hostile) {
			t.Errorf("ValidArtifactFileType(%q) = true", hostile)
		}
	}
}
