package litmus

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type videoProbeRunner struct {
	duration string
	calls    []string
}

func (r *videoProbeRunner) Run(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if strings.HasSuffix(name, "ffprobe") {
		return []byte(r.duration + "\n"), nil
	}
	return nil, nil
}

func TestStepCaptionsSayWhatWasDoneInTheReadersWords(t *testing.T) {
	for _, c := range []struct {
		action     string
		args       []string
		role, name string
		want       string
	}{
		{"click", []string{"@e7"}, "button", "Go back", `click "Go back"`},
		{"click", []string{"@e7"}, "button", "", "click a button"},
		{"click", []string{"@e7"}, "link", "  ", "click a link"},
		{"click", []string{"@e7"}, "", "", "click an element"},
		{"fill", []string{"@e9", "hello"}, "textbox", "Message the agent", `type "hello" into "Message the agent"`},
		{"type", []string{"@e9", "hi"}, "textbox", "", `type "hi" into a text field`},
		{"select", []string{"@e3", "dark"}, "combobox", "Theme", `select "dark" in "Theme"`},
		{"press", []string{"Enter"}, "", "", "press Enter"},
		{"press", []string{"Control+K"}, "button", "ignored", "press Control+K"},
		{"click", []string{"@e7"}, "button", `Say "hi"`, `click "Say 'hi'"`},
	} {
		if got := caption(actionRec{Seq: 3, Action: c.action, Args: c.args, TargetRole: c.role, TargetName: c.name}); got != "Step 3: "+c.want {
			t.Errorf("%s %v (%s %q): %q, want Step 3: %s", c.action, c.args, c.role, c.name, got, c.want)
		}
	}
}

func TestParseRefsReadsRoleAndNameFromASnapshot(t *testing.T) {
	snap := "- generic [ref=e1]\n  - button \"Go back\" [ref=e7]\n  - link \"Say \\\"hi\\\"\" [level=2, ref=e8]\n  - button [disabled, ref=e11]\nnot a node ref=e5\n"
	want := map[string]refInfo{"@e1": {"generic", ""}, "@e7": {"button", "Go back"}, "@e8": {"link", `Say "hi"`}, "@e11": {"button", ""}}
	if got := parseRefs(snap); !reflect.DeepEqual(got, want) {
		t.Fatalf("refs %v, want %v", got, want)
	}
}

// These small image helpers are shared by evidence and crop tests.
func whitePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func pixelAt(t *testing.T, path string, x, y int) color.Color {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img.At(x, y)
}

func TestClipArgsApplyOneSecondBeforeTwoSecondsAfterAndSixtySecondCap(t *testing.T) {
	args := clipArgs("pr.mp4", "clips/c1-pr.mp4", 0, time.Minute)
	if got := strings.Join(args, " "); got != "-y -ss 0.000 -t 60.000 -i pr.mp4 -c copy -movflags +faststart clips/c1-pr.mp4" {
		t.Fatalf("clip args = %q", got)
	}
	if got := strings.Join(clipArgs("pr.mp4", "clips/c1-pr.mp4", time.Second, 90*time.Second), " "); !strings.Contains(got, "-t 60.000") {
		t.Fatalf("clip cap = %q", got)
	}
	if got := checkClipPath("/run", "c/1"); got != filepath.Join("/run", "clips", "c-1-pr.mp4") {
		t.Fatalf("clip path = %q", got)
	}
}

func TestFinishCheckClipsUsesReceiptTimes(t *testing.T) {
	t.Run("bounded recording clip", func(t *testing.T) {
		svc, media, runDir, _ := submitSvc(t)
		runner := &videoProbeRunner{duration: "120"}
		svc.runner = runner
		run, err := svc.store.Get("run_s1")
		if err != nil {
			t.Fatal(err)
		}
		start := run.CreatedAt
		run.Legs = []Leg{{Name: "head", RecordStartedAt: start, RecordStoppedAt: start.Add(2 * time.Minute)}}
		if _, err := svc.update("run_s1", func(cur *Run) { cur.Legs = run.Legs }); err != nil {
			t.Fatal(err)
		}
		path := recordingPath(runDir, "head")
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("playable"), 0o600); err != nil {
			t.Fatal(err)
		}
		checks, err := svc.store.Checks("run_s1")
		if err != nil {
			t.Fatal(err)
		}
		checks[0].Receipts = append(checks[0].Receipts, Receipt{Seq: 4, Leg: "head", Action: "click", AfterPath: checks[0].Receipts[0].AfterPath, At: start.Add(100 * time.Second)})
		if err := svc.store.SaveChecks("run_s1", checks); err != nil {
			t.Fatal(err)
		}
		acts := []actionRec{{Seq: 1, Leg: "head", Action: "click", After: checks[0].Receipts[0].AfterPath, At: start.Add(10 * time.Second)}, {Seq: 4, Leg: "head", Action: "click", After: checks[0].Receipts[0].AfterPath, At: start.Add(100 * time.Second)}}
		got := svc.finishCheckClips(ctx, runDir, run, acts, path, start)
		if got["c1"] != checkClipPath(runDir, "c1") || !fileExists(got["c1"]) {
			t.Fatalf("clips = %#v", got)
		}
		found := false
		for _, call := range media.calls {
			found = found || strings.Contains(strings.Join(call, " "), "-ss 9.000 -t 60.000 -i "+path)
		}
		if !found {
			t.Fatalf("clip ffmpeg call = %v", media.calls)
		}
		count := 0
		for _, call := range runner.calls {
			if strings.Contains(call, "ffprobe") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("ffprobe calls = %d, want one cached probe: %v", count, runner.calls)
		}
	})
}
