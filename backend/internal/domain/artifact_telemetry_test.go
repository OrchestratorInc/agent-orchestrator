package domain

import "testing"

func TestArtifactContentCategory(t *testing.T) {
	for path, want := range map[string]string{
		"report.html":     "html",
		"docs/Plan.MD":    "markdown",
		"shots/a.png":     "image",
		"rows.csv":        "text",
		"deck.pdf":        "pdf",
		"clip.mov":        "video",
		"notes.weird-ext": "other",
		"brand.icon":      "other",
		"Makefile":        "other",
		".env":            "other",
	} {
		got := ArtifactContentCategory(path)
		if got != want {
			t.Errorf("ArtifactContentCategory(%q) = %q, want %q", path, got, want)
		}
		if !ValidArtifactContentCategory(got) {
			t.Errorf("ArtifactContentCategory(%q) = %q, which ValidArtifactContentCategory refuses", path, got)
		}
	}
	for _, hostile := range []string{"", "HTML", "/Users/alice/a.html", "png"} {
		if ValidArtifactContentCategory(hostile) {
			t.Errorf("ValidArtifactContentCategory(%q) = true", hostile)
		}
	}
}

func TestArtifactSizeBucket(t *testing.T) {
	for size, want := range map[int64]string{
		0: "xs", 10*1024 - 1: "xs", 10 * 1024: "s", 100*1024 - 1: "s", 100 * 1024: "m",
		1024*1024 - 1: "m", 1024 * 1024: "l", 10*1024*1024 - 1: "l", 10 * 1024 * 1024: "xl",
	} {
		if got := ArtifactSizeBucket(size); got != want || !ValidArtifactSizeBucket(got) {
			t.Errorf("ArtifactSizeBucket(%d) = %q, want %q", size, got, want)
		}
	}
	if ValidArtifactSizeBucket("2048") {
		t.Error("ValidArtifactSizeBucket accepted a byte count")
	}
}

func TestArtifactSource(t *testing.T) {
	for _, tc := range []struct {
		kind      SessionKind
		requested RequestedOutput
		want      string
	}{
		{KindWorker, RequestedOutputArtifact, "artifacts_tab"},
		{KindWorker, "", "worker"},
		{KindOrchestrator, "", "orchestrator"},
	} {
		if got := ArtifactSource(tc.kind, tc.requested); got != tc.want || !ValidArtifactSource(got) {
			t.Errorf("ArtifactSource(%q, %q) = %q, want %q", tc.kind, tc.requested, got, tc.want)
		}
	}
	if !ValidArtifactRegisterFailure(ArtifactRegisterSessionNotFound) {
		t.Error("refused session_not_found")
	}
	if ValidArtifactSource("mer-1") || ValidArtifactRegisterFailure("ARTIFACT_PATH_OUTSIDE_DIR") {
		t.Error("accepted a value outside the vocabulary")
	}
}
