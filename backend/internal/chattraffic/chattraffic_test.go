package chattraffic_test

import (
	"crypto/sha1" //nolint:gosec // computes git object ids for the test tree.
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chattraffic"
)

func loadSamples(t *testing.T) (chattraffic.Run, chattraffic.Run, chattraffic.Limits, chattraffic.Options) {
	t.Helper()
	runBytes, err := os.ReadFile("testdata/sample-run.json")
	if err != nil {
		t.Fatal(err)
	}
	limitBytes, err := os.ReadFile("testdata/sample-limits.json")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := chattraffic.DecodeRun(runBytes)
	if err != nil {
		t.Fatalf("sample run rejected: %v", err)
	}
	candidate, err := chattraffic.DecodeRun(runBytes)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Side = "candidate"
	candidate.Scenarios = append([]chattraffic.Scenario(nil), baseline.Scenarios...)
	limits, err := chattraffic.DecodeLimits(limitBytes)
	if err != nil {
		t.Fatalf("sample limits rejected: %v", err)
	}
	return baseline, candidate, limits, chattraffic.Options{LimitsSha256: chattraffic.SHA256Hex(limitBytes)}
}

func TestCompareIdenticalRunsPasses(t *testing.T) {
	baseline, candidate, limits, opts := loadSamples(t)
	report := chattraffic.Compare(baseline, candidate, limits, opts)
	if report.Status != chattraffic.StatusPass || report.ExitCode != chattraffic.ExitPass {
		t.Fatalf("status %s exit %d reasons %+v", report.Status, report.ExitCode, report.Reasons)
	}
	if len(report.Rows) != 2 || report.Rows[0].Phase != "open-chat" {
		t.Fatalf("rows not in allowlist order: %+v", report.Rows)
	}
}

func TestCompareLimitIsInclusive(t *testing.T) {
	baseline, candidate, limits, opts := loadSamples(t)
	limits.MaxIncreasePermille = 1000
	limit := limits.Rows[0].Max.DownloadBytes
	candidate.Scenarios[0].Metrics.DownloadBytes = limit
	if report := chattraffic.Compare(baseline, candidate, limits, opts); report.ExitCode != chattraffic.ExitPass {
		t.Fatalf("value equal to the limit failed: %+v", report.Reasons)
	}
	candidate.Scenarios[0].Metrics.DownloadBytes = limit + 1
	report := chattraffic.Compare(baseline, candidate, limits, opts)
	if report.ExitCode != chattraffic.ExitFail || !hasReason(report, "over-limit", "downloadBytes") {
		t.Fatalf("limit+1 did not fail: %+v", report.Reasons)
	}
}

func TestCompareRelativeIncreaseFailsUnderTheLimit(t *testing.T) {
	baseline, candidate, limits, opts := loadSamples(t)
	// 2.0% of 42000 is 840: 42840 is allowed, 42841 is not.
	limits.Rows[0].Max.JSONBytes = 50_000
	candidate.Scenarios[0].Metrics.JSONBytes = 42_840
	if report := chattraffic.Compare(baseline, candidate, limits, opts); report.ExitCode != chattraffic.ExitPass {
		t.Fatalf("increase at the cap failed: %+v", report.Reasons)
	}
	candidate.Scenarios[0].Metrics.JSONBytes = 42_841
	report := chattraffic.Compare(baseline, candidate, limits, opts)
	if report.ExitCode != chattraffic.ExitFail || !hasReason(report, "regressed", "jsonBytes") {
		t.Fatalf("increase past the cap did not fail: %+v", report.Reasons)
	}
}

func TestCompareSemanticLossFailsEvenWhenSmaller(t *testing.T) {
	baseline, candidate, limits, opts := loadSamples(t)
	scenario := &candidate.Scenarios[1]
	scenario.Metrics.BodyBytes /= 2
	scenario.Metrics.JSONBytes /= 2
	scenario.State.ClientDigest = strings.Repeat("6", 64)
	report := chattraffic.Compare(baseline, candidate, limits, opts)
	if report.ExitCode != chattraffic.ExitFail || !hasReason(report, "candidate-state-mismatch", "") {
		t.Fatalf("dropped content passed: %+v", report.Reasons)
	}
}

func TestCompareRefusesIncomparableRuns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(base, cand *chattraffic.Run, opts *chattraffic.Options)
		code   string
	}{
		{"harness", func(_, c *chattraffic.Run, _ *chattraffic.Options) { c.HarnessSha256 = strings.Repeat("9", 64) }, "harness-mismatch"},
		{"fixtures", func(_, c *chattraffic.Run, _ *chattraffic.Options) { c.FixturesSha256 = strings.Repeat("9", 64) }, "fixtures-mismatch"},
		{"limits", func(_, _ *chattraffic.Run, o *chattraffic.Options) { o.LimitsSha256 = strings.Repeat("9", 64) }, "limits-mismatch"},
		{"toolchain", func(_, c *chattraffic.Run, _ *chattraffic.Options) { c.GoVersion = "go1.26.0" }, "toolchain-mismatch"},
		{"client", func(_, c *chattraffic.Run, _ *chattraffic.Options) {
			c.ClientSourceSha256 = strings.Repeat("9", 64)
		}, "client-source-unmodeled"},
		{"baseline-dirty", func(b, _ *chattraffic.Run, _ *chattraffic.Options) {
			b.Dirty, b.DirtyDiffSha256 = true, strings.Repeat("8", 64)
		}, "baseline-dirty"},
		{"ref", func(_, _ *chattraffic.Run, o *chattraffic.Options) { o.ExpectBaselineRef = strings.Repeat("0", 40) }, "baseline-ref-mismatch"},
		{"missing-scenario", func(_, c *chattraffic.Run, _ *chattraffic.Options) { c.Scenarios = c.Scenarios[:1] }, "scenario-missing-in-candidate"},
		{"expected-state", func(_, c *chattraffic.Run, _ *chattraffic.Options) {
			c.Scenarios[0].State.ExpectedDigest = strings.Repeat("7", 64)
			c.Scenarios[0].State.ClientDigest = c.Scenarios[0].State.ExpectedDigest
			c.Scenarios[0].State.ServerDigest = c.Scenarios[0].State.ExpectedDigest
		}, "expected-state-mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline, candidate, limits, opts := loadSamples(t)
			tc.mutate(&baseline, &candidate, &opts)
			report := chattraffic.Compare(baseline, candidate, limits, opts)
			if report.ExitCode != chattraffic.ExitIncomparable || !hasReason(report, tc.code, "") {
				t.Fatalf("exit %d reasons %+v", report.ExitCode, report.Reasons)
			}
			for _, row := range report.Rows {
				for _, metric := range row.Metrics {
					if tc.name != "missing-scenario" && tc.name != "expected-state" && metric.RelativeAvailable {
						t.Fatalf("incomparable runs reported a relative change for %s", metric.Name)
					}
				}
			}
		})
	}
}

func TestAllowedClientChangeIsNeverAPass(t *testing.T) {
	baseline, candidate, limits, opts := loadSamples(t)
	candidate.ClientSourceSha256 = strings.Repeat("9", 64)
	opts.AllowClientChange = true
	report := chattraffic.Compare(baseline, candidate, limits, opts)
	if report.Status != chattraffic.StatusUnverified || report.ExitCode != chattraffic.ExitIncomparable ||
		!hasReason(report, "client-source-unmodeled-allowed", "") {
		t.Fatalf("status %s exit %d reasons %+v", report.Status, report.ExitCode, report.Reasons)
	}
	if !strings.Contains(chattraffic.RenderMarkdown(report), "**Unverified:**") {
		t.Fatal("the Markdown report does not label the comparison unverified")
	}
	// A real failure still reports as a failure.
	candidate.Scenarios[0].Metrics.DownloadBytes = limits.Rows[0].Max.DownloadBytes + 1
	if report := chattraffic.Compare(baseline, candidate, limits, opts); report.ExitCode != chattraffic.ExitFail {
		t.Fatalf("a limit failure under --allow-client-change exited %d", report.ExitCode)
	}
}

func TestDecodeRunRejectsLooseJSON(t *testing.T) {
	sample, err := os.ReadFile("testdata/sample-run.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(sample)
	for name, mutated := range map[string]string{
		"duplicate key": strings.Replace(text, `"dirty": false,`, `"dirty": false, "dirty": false,`, 1),
		"case variant":  strings.Replace(text, `"goos"`, `"GOOS"`, 1),
		"unknown key":   strings.Replace(text, `"dirty": false,`, `"dirty": false, "extra": 1,`, 1),
		"missing key":   strings.Replace(text, `"nonce": "0123456789abcdef0123456789abcdef",`, ``, 1),
		"fraction":      strings.Replace(text, `"requests": 1,`, `"requests": 1.0,`, 1),
		"exponent":      strings.Replace(text, `"requests": 1,`, `"requests": 1e0,`, 1),
		"negative":      strings.Replace(text, `"requests": 1,`, `"requests": -1,`, 1),
		"quoted":        strings.Replace(text, `"requests": 1,`, `"requests": "1",`, 1),
		"too large":     strings.Replace(text, `"requests": 1,`, `"requests": 1000000001,`, 1),
		"trailing data": text + "{}",
		"bom":           "\xef\xbb\xbf" + text,
		"download sum":  strings.Replace(text, `"downloadBytes": 5192,`, `"downloadBytes": 5193,`, 1),
		"bad ref":       strings.Replace(text, `999321b38cc5f57bc53ab9c255cb10c4e8999e68`, `main`, 1),
		"provider":      strings.Replace(text, `"provider": "codex"`, `"provider": "gemini"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if mutated == text {
				t.Fatal("mutation did not apply")
			}
			if _, err := chattraffic.DecodeRun([]byte(mutated)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestMarkdownEscapesEveryString(t *testing.T) {
	report := chattraffic.Report{
		Status:  "pass|`x`",
		Reasons: []chattraffic.Reason{{Code: "a|b<script>", Severity: "info", Scenario: "x\ny"}},
		Rows:    []chattraffic.ReportRow{{Provider: "c|d", Phase: "p", Transport: "t", Status: "pass"}},
	}
	markdown := chattraffic.RenderMarkdown(report)
	for _, raw := range []string{"a|b", "<script>", "c|d", "x\ny"} {
		if strings.Contains(markdown, raw) {
			t.Fatalf("unescaped %q in:\n%s", raw, markdown)
		}
	}
	if !strings.Contains(markdown, `a\|b&lt;script&gt;`) {
		t.Fatalf("escaped reason missing:\n%s", markdown)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOverlayOnlyAddsHarnessFiles(t *testing.T) {
	candidate := t.TempDir()
	writeFile(t, candidate, chattraffic.HarnessPackageDir+"/schema.go", "package chattraffic\n")
	writeFile(t, candidate, chattraffic.HarnessTestFile, "package httpd\n")
	writeFile(t, candidate, "backend/internal/httpd/events.go", "candidate product code\n")

	baseline := t.TempDir()
	writeFile(t, baseline, "backend/internal/httpd/events.go", "baseline product code\n")
	entries, err := chattraffic.Overlay(candidate, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Mode != "added" {
		t.Fatalf("entries %+v", entries)
	}
	product, _ := os.ReadFile(filepath.Join(baseline, "backend/internal/httpd/events.go"))
	if string(product) != "baseline product code\n" {
		t.Fatal("overlay replaced baseline product code")
	}
	// A second overlay finds identical files and adds nothing.
	if entries, err := chattraffic.Overlay(candidate, baseline); err != nil || entries[0].Mode != "identical" {
		t.Fatalf("re-overlay: %+v %v", entries, err)
	}
	// A baseline that already has a different harness file is never shadowed.
	writeFile(t, baseline, chattraffic.HarnessTestFile, "package httpd // older harness\n")
	if _, err := chattraffic.Overlay(candidate, baseline); err == nil {
		t.Fatal("overlay shadowed a different baseline harness file")
	}
}

func TestOverlayRefusesSymlinkedHarnessFiles(t *testing.T) {
	candidate := t.TempDir()
	writeFile(t, candidate, chattraffic.HarnessTestFile, "package httpd\n")
	writeFile(t, candidate, "outside.go", "package secret\n")
	link := filepath.Join(candidate, filepath.FromSlash(chattraffic.HarnessPackageDir), "link.go")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(candidate, "outside.go"), link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := chattraffic.Overlay(candidate, t.TempDir()); err == nil {
		t.Fatal("overlay followed a symlink")
	}
}

func blobID(content string) string {
	h := sha1.New() //nolint:gosec // git object id.
	h.Write([]byte("blob " + strconv.Itoa(len(content)) + "\x00" + content))
	return hex.EncodeToString(h.Sum(nil))
}

func TestVerifyExportBindsTheTreeToTheCommit(t *testing.T) {
	product := "package httpd\n"
	lsTree := []byte("100644 blob " + blobID(product) + "\tbackend/internal/httpd/events.go\x00")
	export := t.TempDir()
	writeFile(t, export, "backend/internal/httpd/events.go", product)
	writeFile(t, export, chattraffic.HarnessTestFile, "package httpd // harness\n")
	overlay := []chattraffic.OverlayEntry{{
		Path: chattraffic.HarnessTestFile, Sha256: chattraffic.SHA256Hex([]byte("package httpd // harness\n")), Mode: "added",
	}}
	if err := chattraffic.VerifyExport(export, lsTree, overlay); err != nil {
		t.Fatal(err)
	}
	writeFile(t, export, "backend/internal/httpd/events.go", "package httpd // candidate code\n")
	if err := chattraffic.VerifyExport(export, lsTree, overlay); err == nil {
		t.Fatal("a modified product file passed")
	}
	writeFile(t, export, "backend/internal/httpd/events.go", product)
	writeFile(t, export, "backend/internal/httpd/extra.go", "package httpd\n")
	if err := chattraffic.VerifyExport(export, lsTree, overlay); err == nil {
		t.Fatal("an extra file passed")
	}
	if err := os.Remove(filepath.Join(export, "backend/internal/httpd/extra.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(export, "backend/internal/httpd/events.go")); err != nil {
		t.Fatal(err)
	}
	if err := chattraffic.VerifyExport(export, lsTree, overlay); err == nil {
		t.Fatal("a missing file passed")
	}
	// A symlink to the right bytes outside the tree is still not the commit.
	outside := filepath.Join(t.TempDir(), "events.go")
	if err := os.WriteFile(outside, []byte(product), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(export, "backend/internal/httpd/events.go")); err != nil {
		t.Fatal(err)
	}
	if err := chattraffic.VerifyExport(export, lsTree, overlay); err == nil {
		t.Fatal("a symlinked product file passed")
	}
}

func hasReason(report chattraffic.Report, code, metric string) bool {
	for _, reason := range report.Reasons {
		if reason.Code == code && (metric == "" || reason.Metric == metric) {
			return true
		}
	}
	return false
}

func TestOutputPreviewKeepsHeadAndTailOnRuneBoundaries(t *testing.T) {
	if got := chattraffic.OutputPreview("short", 64); got != "short" {
		t.Fatalf("an output that fits was changed: %q", got)
	}
	if got := chattraffic.OutputPreview(strings.Repeat("x", 100), 0); len(got) != 100 {
		t.Fatalf("a zero limit cut the output to %d bytes", len(got))
	}
	// é occupies bytes 31-32, so a head of 32 would split it.
	head := strings.Repeat("a", 31) + "é" + strings.Repeat("b", 70)
	if got, want := chattraffic.OutputPreview(head, 64), strings.Repeat("a", 31)+"\n…\n"+strings.Repeat("b", 33); got != want {
		t.Fatalf("head preview = %q, want %q", got, want)
	}
	// é occupies bytes 70-71, so a tail starting at 71 would split it.
	tail := strings.Repeat("a", 70) + "é" + strings.Repeat("c", 31)
	if got, want := chattraffic.OutputPreview(tail, 64), strings.Repeat("a", 32)+"\n…\n"+strings.Repeat("c", 31); got != want {
		t.Fatalf("tail preview = %q, want %q", got, want)
	}
}

func TestCanonicalPayloadKeepsToolContentOnly(t *testing.T) {
	detail := map[string]any{
		"output": "x", "outputBytes": 9.0, "text": "thinking",
		"exitCode": 0.0,
		"files":    []any{map[string]any{"path": "a<b>&c.go", "patch": "@@ -1 +1 @@"}},
	}
	want := `{"exitCode":0,"files":[{"patch":"@@ -1 +1 @@","path":"a<b>&c.go"}]}`
	if got := chattraffic.CanonicalPayload(detail); got != want {
		t.Fatalf("payload = %s, want %s", got, want)
	}
	if got := chattraffic.CanonicalPayload(map[string]any{"output": "x", "text": "y"}); got != "" {
		t.Fatalf("a detail with only output and text has payload %q", got)
	}
	if got := chattraffic.CanonicalPayload(nil); got != "" {
		t.Fatalf("a missing detail has payload %q", got)
	}
}

// The reviewed limits name the fixture set they cover. Regenerated expected
// states change that set, so the limits must be reviewed and rebound with
// them; otherwise every benchmark run is refused as incomparable.
func TestLimitsBindTheCommittedFixtures(t *testing.T) {
	data, err := os.ReadFile("limits.json")
	if err != nil {
		t.Fatal(err)
	}
	limits, err := chattraffic.DecodeLimits(data)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "..")
	fixtures, err := chattraffic.DirectoryDigests(root, chattraffic.FixturesDir)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := chattraffic.DirectoryDigests(root, chattraffic.ExpectedDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := chattraffic.ListDigest(append(fixtures, expected...)); got != limits.FixturesSha256 {
		t.Fatalf("limits.json is bound to fixtures %s, but the committed fixtures and expected states are %s", limits.FixturesSha256, got)
	}
}

func TestCanonicalStateRecordsEachRowsTurn(t *testing.T) {
	turns := []string{"completed", "completed"}
	rows := []chattraffic.CanonicalRow{
		{Turn: 0, Kind: "message", Role: "user", Text: "first"},
		{Turn: 1, Kind: "message", Role: "user", Text: "second"},
		{Turn: 1, Kind: "activity", ActivityKind: "command", Status: "completed", Text: "ls"},
	}
	moved := append([]chattraffic.CanonicalRow(nil), rows...)
	moved[2].Turn = 0
	if chattraffic.CanonicalState(rows, turns) == chattraffic.CanonicalState(moved, turns) {
		t.Fatal("a row moved to the previous turn left the canonical state unchanged")
	}
	// Two completed turns exchanging their rows keep the same rows and states.
	swapped := append([]chattraffic.CanonicalRow(nil), rows...)
	swapped[0].Turn, swapped[1].Turn = 1, 0
	if chattraffic.CanonicalState(rows, turns) == chattraffic.CanonicalState(swapped, turns) {
		t.Fatal("two turns exchanging rows left the canonical state unchanged")
	}
}
