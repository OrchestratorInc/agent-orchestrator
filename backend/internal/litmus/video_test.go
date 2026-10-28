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

func TestVideoCutsUseEachLegRecordingAndCaptionTimeline(t *testing.T) {
	svc, media, dir, artifacts := submitSvc(t)
	svc.runner = &videoProbeRunner{duration: "120"}
	run, err := svc.update("run_s1", func(r *Run) {
		r.Legs[0].RecordStartedAt = r.CreatedAt
		r.Legs[1].RecordStartedAt = r.CreatedAt.Add(5 * time.Second)
		for i := range r.Legs {
			r.Legs[i].RecordStoppedAt = r.CreatedAt.Add(2 * time.Minute)
		}
	})
	is(t, "")(nil, err)
	for _, leg := range run.Legs {
		writeFile(t, recordingPath(dir, leg.Name), "raw-"+leg.Name)
	}
	_, err = svc.Submit(ctx, "s1", verdictInput(t, svc, passVerdict()))
	is(t, "")(nil, err)
	find := func(name string) string {
		for _, call := range media.calls {
			if call[len(call)-1] == name {
				return strings.Join(call, " ")
			}
		}
		return ""
	}
	head, base := find(cutPath(dir, "head")), find(cutPath(dir, "base"))
	if !strings.Contains(head, "-ss 9.000 -t 2.000 -i "+recordingPath(dir, "head")) || !strings.Contains(head, "-ss 19.000 -t 2.000 -i "+recordingPath(dir, "head")) || !strings.Contains(head, "concat=n=2") {
		t.Fatal(head)
	}
	if !strings.Contains(base, "-ss 4.000 -t 2.000 -i "+recordingPath(dir, "base")) || !strings.Contains(base, "concat=n=1") {
		t.Fatal(base)
	}
	data, err := os.ReadFile(filepath.Join(dir, "head-cut-captions.srt"))
	is(t, "")(nil, err)
	if string(data) != "1\n00:00:00,000 --> 00:00:02,000\nStep 1: click an element\n\n2\n00:00:02,000 --> 00:00:04,000\nStep 2: click an element\n\n" {
		t.Fatal(string(data))
	}
	for _, name := range []string{"head.mp4", "base.mp4", "head-cut.mp4", "base-cut.mp4"} {
		exists(t, filepath.Join(artifacts, name))
	}
}
