package acp

import (
	"encoding/json"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/commanddetail"
)

type smallToolCase struct {
	name string
	tool *toolState
}

// smallToolCases are tool snapshots well under every detail cap.
func smallToolCases() []smallToolCase {
	oldText := "a\nb\nc\n"
	return []smallToolCase{
		{"terminal command", &toolState{
			id: "bash-1", title: "Run tests", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusCompleted,
			rawInput:       map[string]any{"command": "/bin/zsh -lc 'go test ./...'"},
			content:        []acpsdk.ToolCallContent{{Terminal: &acpsdk.ToolCallContentTerminal{Type: "terminal", TerminalId: "bash-1"}}},
			terminalOutput: "ok  \tpkg\t0.1s\n<done> & \u2028\n",
			meta: map[string]any{
				"claudeCode":    map[string]any{"toolName": "Bash"},
				"terminal_info": map[string]any{"terminal_id": "bash-1"},
				"terminal_exit": map[string]any{"terminal_id": "bash-1", "exit_code": float64(0)},
			},
		}},
		{"content command", &toolState{
			id: "bash-2", title: "List", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusCompleted,
			rawInput:  map[string]any{"command": "ls"},
			content:   []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("$ ls\na.go <a&b> \u2029\n"))},
			rawOutput: map[string]any{"output": "a.go\n", "metadata": map[string]any{"exit": float64(0)}},
		}},
		{"edit", &toolState{
			id: "edit-1", title: "Edit main.go", kind: acpsdk.ToolKindEdit, status: acpsdk.ToolCallStatusCompleted,
			locations: []acpsdk.ToolCallLocation{{Path: "/repo/main.go"}},
			rawInput:  map[string]any{"file_path": "/repo/main.go", "old_string": "b", "new_string": "B"},
			content:   []acpsdk.ToolCallContent{acpsdk.ToolDiffContent("/repo/main.go", "a\nB\nc\n", oldText)},
		}},
		{"write", &toolState{
			id: "write-1", title: "Write new.go", kind: acpsdk.ToolKindEdit, status: acpsdk.ToolCallStatusCompleted,
			content: []acpsdk.ToolCallContent{acpsdk.ToolDiffContent("/repo/new.go", "package main\n")},
		}},
		{"read", &toolState{
			id: "read-1", title: "Read main.go", kind: acpsdk.ToolKindRead, status: acpsdk.ToolCallStatusCompleted,
			rawInput:  map[string]any{"file_path": "/repo/main.go"},
			content:   []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("1\tpackage main\n"))},
			rawOutput: "1\tpackage main\n",
		}},
	}
}

// The detail bytes main stored for each small case before the caps existed.
// Under every cap, nothing about a row may change.
func TestACPToolDetailSmallPayloadsAreUnchanged(t *testing.T) {
	want := map[string]string{
		"terminal command": `{"command":"go test ./...","content":[{"terminalId":"bash-1","type":"terminal"}],"exitCode":0,"input":{"command":"/bin/zsh -lc 'go test ./...'"},"locations":null,"output":"ok  \tpkg\t0.1s\n\u003cdone\u003e \u0026 \u2028\n","protocol":"acp","providerToolName":"Bash","rawCommand":"/bin/zsh -lc 'go test ./...'","terminalId":"bash-1","toolKind":"execute"}`,
		"content command":  `{"command":"ls","content":[{"content":{"text":"$ ls\na.go \u003ca\u0026b\u003e \u2029\n","type":"text"},"type":"content"}],"exitCode":0,"input":{"command":"ls"},"locations":null,"output":"a.go\n","protocol":"acp","rawCommand":"ls","toolKind":"execute"}`,
		"edit":             `{"content":[{"newText":"a\nB\nc\n","oldText":"a\nb\nc\n","path":"/repo/main.go","type":"diff"}],"files":[{"additions":1,"deletions":1,"newText":"a\nB\nc\n","oldText":"a\nb\nc\n","patch":"--- /repo/main.go\n+++ /repo/main.go\n-a\n-b\n-c\n-\n+a\n+B\n+c\n+","path":"/repo/main.go","status":"modified"}],"input":{"file_path":"/repo/main.go","new_string":"B","old_string":"b"},"locations":[{"path":"/repo/main.go"}],"output":"","protocol":"acp","toolKind":"edit"}`,
		"write":            `{"content":[{"newText":"package main\n","path":"/repo/new.go","type":"diff"}],"files":[{"additions":1,"deletions":0,"newText":"package main\n","oldText":null,"patch":"--- /repo/new.go\n+++ /repo/new.go\n+package main\n+","path":"/repo/new.go","status":"added"}],"input":null,"locations":null,"output":"","protocol":"acp","toolKind":"edit"}`,
		"read":             `{"content":[{"content":{"text":"1\tpackage main\n","type":"text"},"type":"content"}],"input":{"file_path":"/repo/main.go"},"locations":null,"output":"1\tpackage main\n","protocol":"acp","toolKind":"read"}`,
	}
	for _, tc := range smallToolCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got := string((&conversation{}).toolEvent("turn-1", tc.tool, true).Detail); got != want[tc.name] {
				t.Fatalf("detail changed:\n got %s\nwant %s", got, want[tc.name])
			}
		})
	}
}

// A long command re-sends its whole accumulated output on every update. The detail
// keeps the end, where a command reports how it went, and says it was cut. The cap
// counts characters, as the store's cap on the same output streamed does, so the
// two copies agree on whether the output was cut.
func TestACPToolDetailKeepsTailOfLongCommandOutput(t *testing.T) {
	tail := strings.Repeat("é", commanddetail.MaxOutputChars)
	for name, tool := range map[string]*toolState{
		"terminal output": {id: "bash-1", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusInProgress,
			terminalOutput: "lost head\n" + tail},
		"raw output": {id: "bash-2", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusCompleted,
			rawOutput: map[string]any{"output": "lost head\n" + tail}},
	} {
		t.Run(name, func(t *testing.T) {
			var detail struct {
				Output          string `json:"output"`
				OutputTruncated bool   `json:"outputTruncated"`
			}
			if err := json.Unmarshal((&conversation{}).toolEvent("turn-1", tool, false).Detail, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Output != tail || !detail.OutputTruncated {
				t.Fatalf("output = %d chars (tail kept: %v), truncated=%v; want the last %d chars, truncated",
					len([]rune(detail.Output)), detail.Output == tail, detail.OutputTruncated, commanddetail.MaxOutputChars)
			}
		})
	}
}

// A command or file change renders from output and files, which its content
// blocks repeat, so oversized content is replaced by the tool payload marker
// rather than stored again on every update.
func TestACPToolDetailMarksOversizedContentItDoesNotRender(t *testing.T) {
	output := strings.Repeat("x", 256*1024)
	for name, tool := range map[string]*toolState{
		"command": {id: "bash-1", kind: acpsdk.ToolKindExecute, status: acpsdk.ToolCallStatusInProgress,
			content: []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock(output))}},
		"reasoning": {id: "think-1", kind: acpsdk.ToolKindThink, status: acpsdk.ToolCallStatusCompleted,
			content: []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock(output))}},
	} {
		t.Run(name, func(t *testing.T) {
			event := (&conversation{}).toolEvent("turn-1", tool, false)
			var detail struct {
				Content map[string]any `json:"content"`
			}
			if err := json.Unmarshal(event.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(contentWithoutImageData(tool.content))
			if detail.Content["truncated"] != true || detail.Content["bytes"] != float64(len(encoded)) {
				t.Fatalf("content = %.200v, want the truncation marker for %d bytes", detail.Content, len(encoded))
			}
			if len(event.Detail) > commanddetail.MaxToolPayloadChars {
				t.Fatalf("detail is %d bytes", len(event.Detail))
			}
		})
	}
}

// An MCP-style row (read, search, fetch, other) renders its content: that is the
// only place the timeline shows what the tool returned, so it is kept whole. Its
// output repeats the content and is not rendered, so that is still capped.
func TestACPToolDetailKeepsRenderedContentOfMCPStyleTools(t *testing.T) {
	file := strings.Repeat("line of a file\n", 6000)
	tool := &toolState{
		id: "read-1", kind: acpsdk.ToolKindRead, status: acpsdk.ToolCallStatusCompleted,
		content:   []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock(file))},
		rawOutput: file,
	}
	var detail struct {
		Content         []acpsdk.ToolCallContent `json:"content"`
		Output          string                   `json:"output"`
		OutputTruncated bool                     `json:"outputTruncated"`
	}
	if err := json.Unmarshal((&conversation{}).toolEvent("turn-1", tool, true).Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Content) != 1 || detail.Content[0].Content == nil || detail.Content[0].Content.Content.Text == nil ||
		detail.Content[0].Content.Content.Text.Text != file {
		t.Fatalf("read content was not kept whole: %.200v", detail.Content)
	}
	if detail.Output != file[len(file)-commanddetail.MaxOutputChars:] || !detail.OutputTruncated {
		t.Fatalf("output = %d bytes truncated=%v, want the last %d", len(detail.Output), detail.OutputTruncated,
			commanddetail.MaxOutputChars)
	}
}

// A file change renders files[].patch, which the client prefers over the before
// and after text whenever a patch is present. Past the per-file cap the patch stops
// early and says so, the same as a Codex patch, and the full texts are not stored:
// they would only be read for a file that has no patch at all. The counts describe
// the whole change, not the part kept.
func TestACPToolDetailCapsLargeFileChange(t *testing.T) {
	oldText, newText := largeFullFileEdit()
	tool := &toolState{
		id: "edit-1", kind: acpsdk.ToolKindEdit, status: acpsdk.ToolCallStatusCompleted,
		content: []acpsdk.ToolCallContent{
			acpsdk.ToolDiffContent("/repo/big.txt", newText, oldText),
			acpsdk.ToolDiffContent("/repo/small.txt", "b\n", "a\n"),
		},
	}
	event := (&conversation{}).toolEvent("turn-1", tool, true)
	var detail struct {
		Content map[string]any   `json:"content"`
		Files   []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(event.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(detail.Files))
	}
	big, small := detail.Files[0], detail.Files[1]
	stats := diffFileFromSnapshot("/repo/big.txt", &oldText, newText)
	if big["path"] != "/repo/big.txt" || big["status"] != "modified" ||
		big["additions"] != float64(stats.Additions) || big["deletions"] != float64(stats.Deletions) {
		t.Fatalf("big file = path %v status %v +%v -%v, want modified +%d -%d", big["path"], big["status"],
			big["additions"], big["deletions"], stats.Additions, stats.Deletions)
	}
	if patch, _ := big["patch"].(string); len(patch) > commanddetail.MaxPatchChars ||
		!strings.HasPrefix(patch, "--- /repo/big.txt\n+++ /repo/big.txt\n") || big["patchTruncated"] != true {
		t.Fatalf("big patch = %d bytes truncated=%v, want at most %d with its header, truncated",
			len(patch), big["patchTruncated"], commanddetail.MaxPatchChars)
	}
	if _, kept := big["oldText"]; kept {
		t.Fatal("big file kept its old text")
	}
	if _, kept := big["newText"]; kept {
		t.Fatal("big file kept its new text")
	}
	if small["patch"] != acpFilePatch("/repo/small.txt", pointer("a\n"), "b\n") || small["oldText"] != "a\n" ||
		small["newText"] != "b\n" || small["patchTruncated"] != nil {
		t.Fatalf("small file changed: %v", small)
	}
	if detail.Content["truncated"] != true {
		t.Fatalf("content = %.200v, want the truncation marker", detail.Content)
	}
	if len(event.Detail) > commanddetail.MaxPatchChars+4*1024 {
		t.Fatalf("detail is %d bytes", len(event.Detail))
	}
}

// A patch is cut on a character boundary, so the kept part is valid UTF-8.
func TestACPToolDetailCutsPatchOnCharacterBoundary(t *testing.T) {
	newText := strings.Repeat("é", commanddetail.MaxPatchChars)
	tool := &toolState{
		id: "write-1", kind: acpsdk.ToolKindEdit, status: acpsdk.ToolCallStatusCompleted,
		content: []acpsdk.ToolCallContent{acpsdk.ToolDiffContent("/repo/accents.txt", newText)},
	}
	var detail struct {
		Files []struct {
			Patch          string `json:"patch"`
			PatchTruncated bool   `json:"patchTruncated"`
		} `json:"files"`
	}
	if err := json.Unmarshal((&conversation{}).toolEvent("turn-1", tool, true).Detail, &detail); err != nil {
		t.Fatal(err)
	}
	full := acpFilePatch("/repo/accents.txt", nil, newText)
	patch := detail.Files[0].Patch
	if !detail.Files[0].PatchTruncated || len(patch) > commanddetail.MaxPatchChars ||
		len(patch) < commanddetail.MaxPatchChars-3 || !strings.HasPrefix(full, patch) {
		t.Fatalf("patch = %d bytes truncated=%v, want a prefix of at most %d bytes", len(patch),
			detail.Files[0].PatchTruncated, commanddetail.MaxPatchChars)
	}
	if strings.ContainsRune(patch, '\uFFFD') {
		t.Fatal("patch was cut inside a character")
	}
}

// largeFullFileEdit is a provider diff that carries the whole file on both sides,
// as some ACP agents send for any edit: 4000 lines, every tenth one changed.
func largeFullFileEdit() (oldText, newText string) {
	var oldLines, newLines []string
	for i := 0; i < 4000; i++ {
		line := strings.Repeat("o", 40) + "\n"
		oldLines = append(oldLines, line)
		if i%10 == 0 {
			line = strings.Repeat("n", 40) + "\n"
		}
		newLines = append(newLines, line)
	}
	return strings.Join(oldLines, ""), strings.Join(newLines, "")
}

// The full ACP patch lists every old line before any new one, so cutting it at the
// cap would keep only removals and read as though the file had been emptied. A
// capped patch keeps the start of both sides.
func TestACPToolDetailCappedPatchShowsRemovalsAndAdditions(t *testing.T) {
	oldText, newText := largeFullFileEdit()
	tool := &toolState{
		id: "edit-1", kind: acpsdk.ToolKindEdit, status: acpsdk.ToolCallStatusCompleted,
		content: []acpsdk.ToolCallContent{acpsdk.ToolDiffContent("/repo/big.txt", newText, oldText)},
	}
	var detail struct {
		Files []struct {
			Patch          string `json:"patch"`
			PatchTruncated bool   `json:"patchTruncated"`
		} `json:"files"`
	}
	if err := json.Unmarshal((&conversation{}).toolEvent("turn-1", tool, true).Detail, &detail); err != nil {
		t.Fatal(err)
	}
	patch := detail.Files[0].Patch
	lines := strings.Split(strings.TrimPrefix(patch, "--- /repo/big.txt\n+++ /repo/big.txt\n"), "\n")
	removed, added, seenAdded := 0, 0, false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "-") && !seenAdded:
			removed++
		case strings.HasPrefix(line, "+"):
			seenAdded = true
			added++
		default:
			t.Fatalf("unexpected patch line %q", line)
		}
	}
	if removed == 0 || added == 0 || !detail.Files[0].PatchTruncated || len(patch) > commanddetail.MaxPatchChars {
		t.Fatalf("patch = %d bytes with %d removed and %d added lines, truncated=%v; want both sides within %d",
			len(patch), removed, added, detail.Files[0].PatchTruncated, commanddetail.MaxPatchChars)
	}
	if removed < added/2 || added < removed/2 {
		t.Fatalf("patch kept %d removed and %d added lines, want the budget shared", removed, added)
	}
}

// Lines the two sides share at the start and the end are not the change. Past the
// cap they are dropped first, and a change that then fits is stored whole.
func TestACPToolDetailCappedPatchDropsSharedLines(t *testing.T) {
	shared := strings.Repeat(strings.Repeat("s", 40)+"\n", 2000)
	oldText := shared + "before\n" + shared
	newText := shared + "after\n" + shared
	tool := &toolState{
		id: "edit-1", kind: acpsdk.ToolKindEdit, status: acpsdk.ToolCallStatusCompleted,
		content: []acpsdk.ToolCallContent{acpsdk.ToolDiffContent("/repo/one-line.txt", newText, oldText)},
	}
	var detail struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal((&conversation{}).toolEvent("turn-1", tool, true).Detail, &detail); err != nil {
		t.Fatal(err)
	}
	file := detail.Files[0]
	if want := "--- /repo/one-line.txt\n+++ /repo/one-line.txt\n-before\n+after"; file["patch"] != want {
		t.Fatalf("patch = %.200q, want %q", file["patch"], want)
	}
	if _, marked := file["patchTruncated"]; marked {
		t.Fatal("a whole change was marked truncated")
	}
	if _, kept := file["oldText"]; kept {
		t.Fatal("kept the old text of an oversized file")
	}
	if file["additions"] != float64(1) || file["deletions"] != float64(1) {
		t.Fatalf("counts = +%v -%v, want +1 -1", file["additions"], file["deletions"])
	}
}

func TestACPToolDetailCapsPatchWithLongPath(t *testing.T) {
	for _, path := range []string{
		"/" + strings.Repeat("a/", 5000) + "file",
		"/" + strings.Repeat("🧪é/", 2000) + "file",
	} {
		for _, repeat := range []int{1, 5000} {
			oldText, newText := strings.Repeat("before", repeat), strings.Repeat("after", repeat)
			tool := &toolState{
				id: "edit-1", kind: acpsdk.ToolKindEdit, status: acpsdk.ToolCallStatusCompleted,
				content: []acpsdk.ToolCallContent{acpsdk.ToolDiffContent(path, newText, oldText)},
			}
			var detail struct {
				Files []struct {
					Path           string `json:"path"`
					Patch          string `json:"patch"`
					PatchTruncated bool   `json:"patchTruncated"`
				} `json:"files"`
			}
			if err := json.Unmarshal((&conversation{}).toolEvent("turn-1", tool, true).Detail, &detail); err != nil {
				t.Fatal(err)
			}
			file := detail.Files[0]
			if file.Path != path || len(file.Patch) > commanddetail.MaxPatchChars || strings.ContainsRune(file.Patch, '\uFFFD') {
				t.Fatalf("path retained=%v, patch=%d bytes, replacement character=%v", file.Path == path, len(file.Patch), strings.ContainsRune(file.Patch, '\uFFFD'))
			}
			if !strings.Contains(file.Patch, "\n-before") || !strings.Contains(file.Patch, "\n+after") {
				t.Fatal("long path displaced one side of the change")
			}
			if file.PatchTruncated != (repeat > 1) {
				t.Fatalf("patchTruncated=%v; only cutting changed lines should mark the patch", file.PatchTruncated)
			}
			if repeat == 1 && !strings.HasSuffix(file.Patch, "\n-"+oldText+"\n+"+newText) {
				t.Fatal("header abbreviation lost changed text")
			}
			for _, header := range strings.SplitN(file.Patch, "\n", 3)[:2] {
				if !strings.HasSuffix(header, "...") {
					t.Fatal("abbreviated header has no marker")
				}
			}
		}
	}
}
