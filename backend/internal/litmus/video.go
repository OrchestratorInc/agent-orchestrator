package litmus

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	homebrewFFmpeg  = "/opt/homebrew/bin/ffmpeg"
	homebrewFFprobe = "/opt/homebrew/bin/ffprobe"
	stopTimeout     = 30 * time.Second
	videoTimeout    = 2 * time.Minute
	noVideo         = "none (recording was not available)"
)

// Allow setup plus 100 ms per captured frame, including probe and faststart.
func recordingEncodeTimeout(frames int) time.Duration {
	return videoTimeout + time.Duration(frames)*100*time.Millisecond
}

// Media runs ffmpeg with the given arguments. Tests substitute a fake, so no unit test encodes video.
type Media interface {
	Run(ctx context.Context, args ...string) error
}

type execMedia struct{}

func (execMedia) Run(ctx context.Context, args ...string) error {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		bin = homebrewFFmpeg // the daemon's PATH may lack Homebrew
	}
	cmd := process.CommandContext(ctx, bin, append([]string{"-hide_banner", "-loglevel", "error"}, args...)...)
	errOut := &capWriter{max: 64 << 10}
	cmd.Stderr, cmd.WaitDelay = errOut, 2*time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLines(errOut.buf, 5))
	}
	return nil
}

type playableCacheEntry struct {
	size     int64
	modified time.Time
	duration float64
}

var playableCache = struct {
	sync.Mutex
	m map[string]playableCacheEntry
}{m: map[string]playableCacheEntry{}}

// recordingPath is where a leg's screencast is encoded; cutPath is the captioned video built for the leg.
func recordingPath(runDir, leg string) string { return filepath.Join(runDir, leg, leg+".mp4") }
func cutPath(runDir, leg string) string       { return filepath.Join(runDir, leg+"-cut.mp4") }

func checkClipPath(runDir, checkID string) string {
	checkID = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, checkID)
	checkID = strings.Trim(checkID, "-")
	if checkID == "" {
		checkID = "check"
	}
	return filepath.Join(runDir, "clips", checkID+"-pr.mp4")
}

// recordingDuration asks ffprobe for the duration. A non-empty file is not
// enough: an interrupted MP4 often has bytes but no playable index.
func (s *Service) recordingDuration(ctx context.Context, path string) (float64, bool) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return 0, false
	}
	playableCache.Lock()
	if cached, ok := playableCache.m[path]; ok && cached.size == st.Size() && cached.modified.Equal(st.ModTime()) {
		playableCache.Unlock()
		return cached.duration, cached.duration > 0
	}
	playableCache.Unlock()
	if s.runner == nil {
		return 0, false
	}
	probe := "ffprobe"
	if _, err := exec.LookPath(probe); err != nil {
		probe = homebrewFFprobe
	}
	out, err := s.runner.Run(ctx, "", nil, probe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path)
	duration := float64(0)
	if err == nil {
		line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		d, parseErr := strconv.ParseFloat(line, 64)
		if parseErr == nil {
			duration = d
		}
	}
	playableCache.Lock()
	playableCache.m[path] = playableCacheEntry{size: st.Size(), modified: st.ModTime(), duration: duration}
	playableCache.Unlock()
	return duration, duration > 0
}

func (s *Service) probeRecording(ctx context.Context, path string) bool {
	_, ok := s.recordingDuration(ctx, path)
	return ok
}

// legVideo is what the video step left for one leg: raw is its recording (<leg>.mp4), cut is <leg>-cut.mp4.
type legVideo struct {
	leg      string
	raw, cut bool
	// source is the playable recording used for cuts.
	source string
	// clips maps check ids to per-check PR clips. It is filled by finishVideo;
	// callers that publish comments can use checkClipPath when they need the
	// on-disk path without depending on this internal result.
	clips map[string]string
}

func (v legVideo) files() (out []string) {
	if v.raw {
		out = append(out, v.leg+".mp4")
	}
	if v.cut {
		out = append(out, v.leg+"-cut.mp4")
	}
	return out
}

func (v legVideo) String() string {
	switch {
	case v.raw && v.cut:
		return fmt.Sprintf("%s: recorded (%s.mp4; %s-cut.mp4 keeps a 2 s window around each action)", v.leg, v.leg, v.leg)
	case v.raw:
		return fmt.Sprintf("%s: recorded (%s.mp4)", v.leg, v.leg)
	}
	return fmt.Sprintf("%s: recording was not available", v.leg)
}

// videoLine is the verdict's Video: text, one entry per leg that has video.
func videoLine(vs []legVideo) string {
	if len(vs) == 0 {
		return noVideo
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.String()
	}
	return strings.Join(parts, "; ")
}

// finishVideo builds each leg's cut from that leg's own recording and only that leg's actions: a 2 s window around
// each action, captioned. A leg without a recording is left out.
// ffmpeg failures are logged: the verdict never waits on video.
func (s *Service) finishVideo(ctx context.Context, runDir string, run Run) (out []legVideo) {
	ctx, cancel := context.WithTimeout(ctx, videoTimeout)
	defer cancel()
	all := readActions(runDir)
	for _, leg := range []string{"head", "base"} {
		acts := slices.DeleteFunc(slices.Clone(all), func(a actionRec) bool { return cmp.Or(a.Leg, "head") != leg })
		var l Leg
		if i := slices.IndexFunc(run.Legs, func(l Leg) bool { return l.Name == leg }); i >= 0 {
			l = run.Legs[i]
		}
		started := l.RecordStartedAt
		v, cut := legVideo{leg: leg}, cutPath(runDir, leg)
		source := recordingPath(runDir, leg)
		if !started.IsZero() && s.probeRecording(ctx, source) {
			v.raw, v.source = true, source
			if segs := segments(acts, started); len(segs) > 0 {
				duration, _ := s.recordingDuration(ctx, source)
				covered := segs[len(segs)-1].to <= time.Duration(duration*float64(time.Second))
				if covered {
					v.cut = s.encode(ctx, runDir, leg+"-cut-captions.srt", captions(segs), func(srt string) []string { return cutArgs(source, srt, cut, segs) }) == nil
				}
			}
		}
		if leg == "head" {
			v.clips = s.finishCheckClips(ctx, runDir, run, all, source, started)
		}
		if v.raw || v.cut {
			out = append(out, v)
		}
	}
	return out
}

func clipArgs(in, out string, from, duration time.Duration) []string {
	duration = min(duration, time.Minute)
	return []string{"-y", "-ss", fmt.Sprintf("%.3f", from.Seconds()), "-t", fmt.Sprintf("%.3f", duration.Seconds()), "-i", in,
		"-c", "copy", "-movflags", "+faststart", out}
}

func actionForReceipt(r Receipt, all []actionRec) (actionRec, bool) {
	if i := slices.IndexFunc(all, func(a actionRec) bool { return a.Seq == r.Seq && a.onLeg() == r.onLeg() }); i >= 0 {
		a := all[i]
		if a.At.IsZero() {
			a.At = r.At
		}
		if a.After == "" {
			a.After = r.AfterPath
		}
		return a, true
	}
	if r.AfterPath == "" {
		return actionRec{}, false
	}
	return actionRec{Seq: r.Seq, Leg: r.Leg, Action: r.Action, Args: r.Args, Before: r.BeforePath, After: r.AfterPath, At: r.At,
		TargetRole: r.TargetRole, TargetName: r.TargetName}, true
}

func (a actionRec) onLeg() string { return cmp.Or(a.Leg, "head") }

// finishCheckClips writes one clip for each head check when its recording covers
// the receipt window.
func (s *Service) finishCheckClips(ctx context.Context, runDir string, run Run, all []actionRec, source string, started time.Time) map[string]string {
	if s.store == nil {
		return nil
	}
	checks, err := s.store.Checks(run.ID)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, check := range checks {
		if check.onLeg() != "head" {
			continue
		}
		var acts []actionRec
		for _, r := range check.legReceipts() {
			if a, ok := actionForReceipt(r, all); ok {
				acts = append(acts, a)
			}
		}
		if len(acts) == 0 {
			continue
		}
		slices.SortFunc(acts, func(a, b actionRec) int { return a.At.Compare(b.At) })
		first, last := acts[0].At, acts[len(acts)-1].At
		if first.IsZero() || last.IsZero() {
			continue
		}
		path := checkClipPath(runDir, check.ID)
		duration, playable := s.recordingDuration(ctx, source)
		covered := !started.IsZero() && last.Sub(started)+2*time.Second <= time.Duration(duration*float64(time.Second))
		if source != "" && playable && covered {
			from := max(first.Sub(started)-time.Second, 0)
			to := max(last.Sub(started)+2*time.Second, from+time.Millisecond)
			duration := min(to-from, time.Minute)
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err == nil && s.media.Run(ctx, clipArgs(source, path, from, duration)...) == nil && fileExists(path) {
				out[check.ID] = path
				continue
			}
		}
	}
	return out
}

// encode writes the caption file and runs ffmpeg with the arguments build returns for it.
func (s *Service) encode(ctx context.Context, runDir, srtName, srt string, build func(srtPath string) []string) error {
	path := filepath.Join(runDir, srtName)
	err := os.WriteFile(path, []byte(srt), 0o600)
	if err == nil {
		err = s.media.Run(ctx, build(path)...)
	}
	if err != nil {
		slog.Warn("litmus: video step failed", "run", filepath.Base(runDir), "error", err)
	}
	return err
}

// seg is a stretch of video and its caption.
type seg struct {
	from, to time.Duration
	text     string
}

// segments gives each action a 2 s window of the recording (1 s either side) and merges windows that touch.
func segments(acts []actionRec, start time.Time) (out []seg) {
	for _, a := range acts {
		if a.Action == "test" { // a package test run is not a moment on screen
			continue
		}
		at := a.At.Sub(start)
		if at < 0 {
			continue
		}
		from, to := max(at-time.Second, 0), at+time.Second
		if n := len(out); n > 0 && from <= out[n-1].to {
			out[n-1].to, out[n-1].text = to, out[n-1].text+" / "+caption(a)
		} else {
			out = append(out, seg{from, to, caption(a)})
		}
	}
	return out
}

// caption is the line under a step in the video: Step 3: click "Collapse sidebar".
func caption(a actionRec) string {
	return fmt.Sprintf("Step %d: %s", a.Seq, stepText(a.Action, a.Args, a.TargetRole, a.TargetName))
}

// refInfo is what a snapshot line says about an element: its role and accessible name.
type refInfo struct{ role, name string }

var snapRef = regexp.MustCompile(`^\s*- ([A-Za-z]+)(?: ("(?:[^"\\]|\\.)*"))?[^\n]*?\bref=(e[0-9]+)`)

// parseRefs reads `snapshot` output (`- button "Go back" [ref=e7]`) into refs keyed the way actions spell them (@e7).
func parseRefs(snap string) map[string]refInfo {
	out := map[string]refInfo{}
	for _, l := range strings.Split(snap, "\n") {
		if m := snapRef.FindStringSubmatch(l); m != nil {
			name, _ := strconv.Unquote(m[2])
			out["@"+m[3]] = refInfo{m[1], name}
		}
	}
	return out
}

var roleWords = map[string]string{"button": "a button", "link": "a link", "textbox": "a text field", "combobox": "a field", "checkbox": "a checkbox", "tab": "a tab", "menuitem": "a menu item"}

// plainName is an element's accessible name as a reader should see it: a project row's name is joined to its path
// and age, so only the first line is kept, cut at the first word that is a local path.
func plainName(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	isPath := func(w string) bool {
		return strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~/") || strings.Contains(w, "/Users/") || strings.Contains(w, "/home/")
	}
	words := strings.Fields(s)
	if i := slices.IndexFunc(words, isPath); i > 0 {
		words = words[:i]
	}
	return strings.Join(slices.DeleteFunc(words, isPath), " ")
}

// stepText says what an action did in the words a reader would use: click "Go back", type "hi" into "Message the
// agent", press Enter. An element the snapshot did not name is "a button" (or "an element" with no role either).
func stepText(action string, args []string, role, name string) string {
	quote := func(s string) string {
		return `"` + clip(strings.ReplaceAll(strings.Join(strings.Fields(s), " "), `"`, "'"), 40) + `"`
	}
	target := cmp.Or(roleWords[role], "an element")
	if name = plainName(name); name != "" {
		target = quote(name)
	}
	text := ""
	if len(args) > 1 {
		text = quote(args[1])
	}
	switch action {
	case "press":
		return "press " + strings.Join(args, " ")
	case "fill", "type":
		return "type " + cmp.Or(text, "text") + " into " + target
	case "select":
		return "select " + cmp.Or(text, "an option") + " in " + target
	}
	return action + " " + target
}

// cutArgs joins the segments of in into out, one seeked input per segment, with the captions as a soft track.
func cutArgs(in, srt, out string, segs []seg) []string {
	args, graph := []string{"-y"}, ""
	for i, sg := range segs {
		args = append(args, "-ss", fmt.Sprintf("%.3f", sg.from.Seconds()), "-t", fmt.Sprintf("%.3f", (sg.to-sg.from).Seconds()), "-i", in)
		graph += fmt.Sprintf("[%d:v]", i)
	}
	graph += fmt.Sprintf("concat=n=%d:v=1:a=0,format=yuv420p[v]", len(segs))
	return append(args, "-i", srt, "-filter_complex", graph, "-map", "[v]", "-map", fmt.Sprintf("%d:s", len(segs)),
		"-c:v", "libx264", "-c:s", "mov_text", "-movflags", "+faststart", out)
}

// captions renders segs as SRT on the output timeline: each segment starts where the previous one ended.
func captions(segs []seg) string {
	var b strings.Builder
	var at time.Duration
	for i, sg := range segs {
		d := sg.to - sg.from
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(at), srtTime(at+d), sg.text)
		at += d
	}
	return b.String()
}

func srtTime(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60, d.Milliseconds()%1000)
}
