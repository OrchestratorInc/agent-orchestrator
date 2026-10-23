package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestArtifactSummaryNamesUpToTwoFiles(t *testing.T) {
	files := func(paths ...string) []artifactFile {
		out := make([]artifactFile, 0, len(paths))
		for _, path := range paths {
			out = append(out, artifactFile{Path: path})
		}
		return out
	}
	cases := []struct {
		files []artifactFile
		count int
		want  string
	}{
		{nil, 0, "-"},
		{files("report.html"), 1, "report.html"},
		{files("report.html", "notes.md"), 2, "report.html, notes.md"},
		{files("a.md", "b.md", "c.md", "d.md"), 4, "a.md, b.md (+2)"},
		// A listing names at most ten files; the count says how many there are.
		{files("a.md", "b.md", "c.md"), 200, "a.md, b.md (+198)"},
		// An older control plane sends no count.
		{files("a.md", "b.md", "c.md"), 0, "a.md, b.md (+1)"},
	}
	for _, tc := range cases {
		if got := artifactSummary(tc.files, tc.count); got != tc.want {
			t.Errorf("artifactSummary(%d files, count %d) = %q, want %q", len(tc.files), tc.count, got, tc.want)
		}
	}
}

// A full page of workers, each listing the most files the control plane sends
// with the longest path a bucket key allows, decodes within the client's
// response limit.
func TestListChildrenDecodesAFullPageOfArtifactFiles(t *testing.T) {
	longest := strings.Repeat("p", 1024-len("orgs/00000000-0000-0000-0000-000000000000/sessions/00000000-0000-0000-0000-000000000000/")-len("/000.md")) + "/"
	count := 200
	page := struct {
		Items []session `json:"items"`
	}{}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for index := range 100 {
		child := session{ID: fmt.Sprintf("child-%03d", index), Kind: "worker", Status: "working", ArtifactCount: &count, ArtifactsUpdatedAt: &now, ArtifactFilesTruncated: true}
		for file := range 10 {
			child.ArtifactFiles = append(child.ArtifactFiles, artifactFile{Path: fmt.Sprintf("%s%03d.md", longest, file), Kind: "markdown", Size: 25 << 20, UpdatedAt: now})
		}
		page.Items = append(page.Items, child)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > maxResponseBody {
		t.Fatalf("test page is %d bytes, over the %d limit it checks", len(body), maxResponseBody)
	}
	c := reviewTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	items, err := listChildren(context.Background(), c, false)
	if err != nil {
		t.Fatalf("listChildren: %v", err)
	}
	if len(items) != 100 || *items[99].ArtifactCount != 200 || !items[99].ArtifactFilesTruncated || len(items[99].ArtifactFiles) != 10 {
		t.Fatalf("decoded %d children, last = %+v", len(items), items[len(items)-1].ArtifactCount)
	}
}

// Paths made of & encode to six bytes a character; the control plane lists
// as many such files per worker as fit its byte budget, two at the longest.
func TestListChildrenDecodesAFullPageOfEscapedArtifactPaths(t *testing.T) {
	segment := strings.Repeat("&", 255)
	head := segment + "/" + segment + "/" + segment + "/"
	escaped := func(file int) string {
		name := fmt.Sprintf("/%03d.md", file)
		return head + strings.Repeat("&", 1024-len("orgs/00000000-0000-0000-0000-000000000000/sessions/00000000-0000-0000-0000-000000000000/")-len(head)-len(name)) + name
	}
	count := 200
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	page := struct {
		Items []session `json:"items"`
	}{}
	for index := range 100 {
		child := session{ID: fmt.Sprintf("child-%03d", index), Kind: "worker", Status: "working", ArtifactCount: &count, ArtifactsUpdatedAt: &now, ArtifactFilesTruncated: true}
		for file := range 2 {
			child.ArtifactFiles = append(child.ArtifactFiles, artifactFile{Path: escaped(file), Kind: "markdown", Size: 25 << 20, UpdatedAt: now})
		}
		page.Items = append(page.Items, child)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > maxResponseBody {
		t.Fatalf("test page is %d bytes, over the %d limit it checks", len(body), maxResponseBody)
	}
	c := reviewTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	items, err := listChildren(context.Background(), c, false)
	if err != nil {
		t.Fatalf("listChildren: %v", err)
	}
	if len(items) != 100 || items[0].ArtifactFiles[0].Path != escaped(0) {
		t.Fatalf("decoded %d children", len(items))
	}
}
