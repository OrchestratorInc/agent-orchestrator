package litmus

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFindingClipRangeUsesSelectedMomentsAndThreeSecondPadding(t *testing.T) {
	start := time.Unix(1000, 0)
	for _, c := range []struct {
		name                 string
		seconds, cited       []int
		ms                   map[int]int64
		clip                 *FindingClip
		span, from, duration int
		ok                   bool
	}{
		{name: "one", seconds: []int{10}, cited: []int{1}, clip: &FindingClip{From: 1, To: 1}, span: 120, from: 7, duration: 6, ok: true},
		{name: "one slow action", seconds: []int{10}, cited: []int{1}, clip: &FindingClip{From: 1, To: 1}, ms: map[int]int64{1: 6000}, span: 120, from: 1, duration: 12, ok: true},
		{name: "no selected range", seconds: []int{10, 100, 118}, cited: []int{3, 1, 2}, span: 180},
		{name: "selected end before last citation", seconds: []int{10, 50, 100}, cited: []int{3, 1, 2}, clip: &FindingClip{From: 1, To: 2}, span: 120, from: 7, duration: 46, ok: true},
		{name: "explicit range without cap", seconds: []int{10, 500}, cited: []int{1, 2}, clip: &FindingClip{From: 1, To: 2}, span: 600, from: 7, duration: 496, ok: true},
		{name: "explicit uncited endpoints", seconds: []int{10, 50, 100}, cited: []int{2}, clip: &FindingClip{From: 1, To: 3}, span: 120, from: 7, duration: 96, ok: true},
		{name: "explicit slow action", seconds: []int{10, 50}, clip: &FindingClip{From: 1, To: 2}, ms: map[int]int64{1: 6000}, span: 120, from: 1, duration: 52, ok: true},
		{name: "explicit action starts before recording", seconds: []int{2, 10}, clip: &FindingClip{From: 1, To: 2}, ms: map[int]int64{1: 5000}, span: 120, from: 0, duration: 13, ok: true},
		{name: "explicit endpoint before recording", seconds: []int{-1, 5}, clip: &FindingClip{From: 1, To: 2}, span: 120},
		{name: "explicit without proof", seconds: []int{10, 100}, clip: &FindingClip{From: 1, To: 2}, span: 120, from: 7, duration: 96, ok: true},
		{name: "explicit single moment", seconds: []int{10, 100}, cited: []int{1}, clip: &FindingClip{From: 2, To: 2}, span: 120, from: 97, duration: 6, ok: true},
		{name: "explicit missing from", seconds: []int{10, 50, 100}, clip: &FindingClip{From: 0, To: 3}, span: 120},
		{name: "explicit missing to", seconds: []int{10, 50, 100}, clip: &FindingClip{From: 1, To: 4}, span: 120},
		{name: "explicit reversed", seconds: []int{10, 100}, clip: &FindingClip{From: 2, To: 1}, span: 120},
		{name: "start clamp", clip: &FindingClip{From: 1, To: 1}, seconds: []int{1}, cited: []int{1}, span: 120, from: 0, duration: 4, ok: true},
		{name: "end clamp", clip: &FindingClip{From: 1, To: 1}, seconds: []int{119}, cited: []int{1}, span: 120, from: 116, duration: 4, ok: true},
		{name: "past recording", clip: &FindingClip{From: 1, To: 1}, seconds: []int{121}, cited: []int{1}, span: 120},
		{name: "before recording", clip: &FindingClip{From: 1, To: 1}, seconds: []int{-1}, cited: []int{1}, span: 120},
		{name: "no recording", clip: &FindingClip{From: 1, To: 1}, seconds: []int{10}, cited: []int{1}},
		{name: "none", span: 120},
	} {
		t.Run(c.name, func(t *testing.T) {
			var receipts []Receipt
			for i, sec := range c.seconds {
				receipts = append(receipts, Receipt{Seq: i + 1, At: start.Add(time.Duration(sec) * time.Second), MS: c.ms[i+1]})
			}
			slices.Reverse(receipts)
			// The other leg's receipt IDs and timestamps cannot affect this finding.
			receipts = append(receipts, Receipt{Seq: 4, Leg: "base", At: start.Add(110 * time.Second)})
			f := Finding{Leg: "head", Receipts: c.cited, Clip: c.clip}
			from, duration, ok := findingClipRange(f, receipts, start, time.Duration(c.span)*time.Second)
			if ok != c.ok || from != time.Duration(c.from)*time.Second || duration != time.Duration(c.duration)*time.Second {
				t.Fatalf("from %s duration %s ok %t, want %d s %d s %t", from, duration, ok, c.from, c.duration, c.ok)
			}
		})
	}
}

func TestFindingClipAfterRecordingRestartIncludesLastAction(t *testing.T) {
	svc, media, dir, _ := submitSvc(t)
	svc.runner = &videoProbeRunner{duration: "120"}
	start := svc.clock()
	file := recordingPath(dir, "head")
	writeFile(t, file, "recording")
	v := Verdict{Findings: []Finding{{Leg: "head", Receipts: []int{1, 2}, Clip: &FindingClip{From: 2, To: 2}}}}
	actions := []actionRec{
		{Seq: 1, Leg: "head", At: start.Add(-time.Second)},
		{Seq: 2, Leg: "head", At: start.Add(5 * time.Second), MS: 1000},
	}
	clips := svc.finishFindingClips(ctx, dir, v, "head", actions, file, start)
	if clips["finding-1"] != findingClipPath(dir, 0) || len(media.calls) != 1 {
		t.Fatalf("clips %v, media calls %v", clips, media.calls)
	}
	if got := strings.Join(media.calls[0], " "); !strings.Contains(got, "-ss 1.000 -t 7.000 -i "+file) {
		t.Fatalf("clip omitted the last action: %s", got)
	}
}

func TestFindingClipCutsSelectedMomentsOnOnlyFindingLeg(t *testing.T) {
	for _, tc := range []struct {
		name, window string
		cited        []int
		clip         *FindingClip
	}{
		{"explicit whole range", "-ss 7.000 -t 496.000", []int{1, 4}, &FindingClip{From: 1, To: 4}},
		{"selected earlier moment", "-ss 7.000 -t 6.000", []int{4, 1}, &FindingClip{From: 1, To: 1}},
		{"selected nearby moments", "-ss 479.000 -t 24.000", []int{4, 1, 3}, &FindingClip{From: 3, To: 4}},
		{"explicit uncited endpoints", "-ss 7.000 -t 496.000", []int{3}, &FindingClip{From: 1, To: 4}},
		{"explicit without proof", "-ss 7.000 -t 496.000", nil, &FindingClip{From: 1, To: 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, media, dir, _ := submitSvc(t)
			svc.runner = &videoProbeRunner{duration: "600"}
			start := svc.clock()
			file := recordingPath(dir, "base")
			writeFile(t, file, "recording")
			v := Verdict{Findings: []Finding{{Leg: "base", Receipts: tc.cited, Clip: tc.clip}}}
			actions := []actionRec{
				{Seq: 1, Leg: "head", At: start.Add(time.Second)},
				{Seq: 1, Leg: "base", At: start.Add(10 * time.Second)},
				{Seq: 3, Leg: "base", At: start.Add(482 * time.Second)},
				{Seq: 4, Leg: "base", At: start.Add(500 * time.Second)},
			}
			clips := svc.finishFindingClips(ctx, dir, v, "base", actions, file, start)
			if clips["finding-1"] != findingClipPath(dir, 0) {
				t.Fatalf("clips %v", clips)
			}
			if !slices.ContainsFunc(media.calls, func(args []string) bool {
				return strings.Contains(strings.Join(args, " "), tc.window+" -i "+file)
			}) {
				t.Fatal(media.calls)
			}
			if got := svc.finishFindingClips(ctx, dir, v, "head", actions, file, start); len(got) != 0 {
				t.Fatalf("wrong leg clips %v", got)
			}
		})
	}
}

func TestFindingImageUsesClipToAtFullWidth(t *testing.T) {
	svc, _, dir, _ := submitSvc(t)
	receipts, err := svc.store.Receipts("run_s1")
	is(t, "")(nil, err)
	selected := receipts[0].AfterPath
	whitePNG(t, selected, 1701, 900)
	v := Verdict{Result: "fail", Findings: []Finding{{Title: "Menu", Leg: "head", Severity: "bug", Receipts: []int{2, 1}, Clip: &FindingClip{From: 1, To: 1}}}}
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	_, _, err = svc.composeComment(run, v, svc.credentialPattern(ctx))
	is(t, "")(nil, err)
	original, err := os.ReadFile(selected)
	is(t, "")(nil, err)
	staged, err := os.ReadFile(filepath.Join(dir, "evidence", "finding-1.png"))
	is(t, "")(nil, err)
	if !bytes.Equal(original, staged) {
		t.Fatal("finding image was cropped or scaled")
	}
	is(t, "")(nil, os.Remove(selected))
	if _, ok := findingShot(v.Findings[0], receipts); ok {
		t.Fatal("missing clip.to shot fell back to another receipt")
	}
}

func TestReceiptsNumberEachLegAndResumeAfterRestart(t *testing.T) {
	svc, _, _, dir := driveSvc(t)
	run, err := svc.store.Get("run_s1")
	is(t, "")(nil, err)
	base, err := svc.attachEngine(ctx, run.ID, Leg{Name: "base", State: "running", DebugPort: 9401})
	is(t, "")(nil, err)
	run.Legs = append(run.Legs, base)
	is(t, "")(nil, svc.store.Save(run))
	for _, c := range []struct {
		leg  string
		want int
	}{{"head", 1}, {"base", 1}, {"head", 2}, {"base", 2}} {
		out, err := svc.Drive(ctx, "s1", "press", []string{"Enter", "--leg", c.leg})
		is(t, "")(nil, err)
		rec := out["receipt"].(actionRec)
		if rec.Seq != c.want || !strings.HasSuffix(rec.After, c.leg+"-"+string(rune('0'+c.want))+"-after.png") {
			t.Fatalf("receipt %+v", rec)
		}
	}
	svc.seqs = map[string]int{}
	out, err := svc.Drive(ctx, "s1", "press", []string{"Enter", "--leg", "base"})
	is(t, "")(nil, err)
	if out["receipt"].(actionRec).Seq != 3 {
		t.Fatal(out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "actions.jsonl"))
	is(t, "")(nil, err)
	var first actionRec
	is(t, "")(nil, json.Unmarshal(bytes.Split(data, []byte("\n"))[0], &first))
	if first.Leg != "head" || first.Seq != 1 {
		t.Fatalf("first %+v", first)
	}
}
