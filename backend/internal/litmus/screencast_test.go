package litmus

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeScreencast sends frames only after the preceding acknowledgement arrives.
// Every connection must start its own screencast before it can receive frames.
func fakeScreencast(t *testing.T, frames, disconnects int) (string, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	var connections, acks atomic.Int32
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		connection := int(connections.Add(1))
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		read := func() (string, map[string]any, bool) {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return "", nil, false
			}
			var request struct {
				Method string
				Params map[string]any
			}
			if err := json.Unmarshal(data, &request); err != nil {
				t.Error(err)
				return "", nil, false
			}
			return request.Method, request.Params, true
		}
		method, params, ok := read()
		if !ok || method != "Page.startScreencast" || params["format"] != "jpeg" || params["quality"] != float64(70) ||
			params["maxWidth"] != float64(1320) || params["maxHeight"] != float64(780) || params["everyNthFrame"] != float64(6) {
			t.Errorf("start request: %s %v", method, params)
			return
		}
		for i := 0; i < frames; i++ {
			sessionID := connection*100000 + i
			data, _ := json.Marshal(map[string]any{"method": "Page.screencastFrame", "params": map[string]any{
				"sessionId": sessionID, "data": base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("jpeg-%d", sessionID))),
			}})
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				t.Error(err)
				return
			}
			method, params, ok = read()
			if !ok || method != "Page.screencastFrameAck" || params["sessionId"] != float64(sessionID) {
				t.Errorf("ack request: %s %v", method, params)
				return
			}
			acks.Add(1)
		}
		// The pong is read on the client's next Read, after its last frame has
		// been saved. This keeps stop from racing that final disk write.
		stopped := make(chan struct{})
		go func() {
			_, _, _ = read() // also services pong control frames on the server
			close(stopped)
		}()
		if err := conn.Ping(ctx); err != nil {
			t.Error(err)
			return
		}
		if connection <= disconnects {
			return // force the client to resolve and connect again
		}
		close(done)
		// Wait for stop or cancellation, keeping the last connection alive.
		<-stopped
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), &acks, done
}

func readFrameIndex(t *testing.T, dir string) []screencastFrame {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "frames.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var frames []screencastFrame
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var frame screencastFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func TestScreencastAcknowledgesThrottlesAndWritesManifest(t *testing.T) {
	url, acks, done := fakeScreencast(t, 20, 0)
	dir := t.TempDir()
	start := time.Unix(1000, 0).UTC()
	var ticks atomic.Int32
	clock := func() time.Time { return start.Add(time.Duration(ticks.Add(1)-1) * 50 * time.Millisecond) }
	r := newScreencast(context.Background(), dir, start, clock, func() time.Duration { return time.Duration(ticks.Load()-1) * 50 * time.Millisecond },
		func(context.Context) (string, error) { return url, nil }, func(err error) { t.Error(err) })
	t.Cleanup(func() { _ = r.stop(start.Add(time.Second)) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("frames were not acknowledged")
	}
	if err := r.stop(start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if acks.Load() != 20 {
		t.Fatalf("acks = %d, want all 20", acks.Load())
	}
	frames := readFrameIndex(t, dir)
	if len(frames) != 5 {
		t.Fatalf("kept frames = %d, want 5", len(frames))
	}
	for i, frame := range frames {
		if frame.N != i+1 || frame.WallMS != start.Add(time.Duration(i)*200*time.Millisecond).UnixMilli() {
			t.Errorf("frame %d = %+v", i, frame)
		}
		data, err := os.ReadFile(filepath.Join(dir, "frames", fmt.Sprintf("%06d.jpg", i+1)))
		if err != nil || !strings.HasPrefix(string(data), "jpeg-") {
			t.Fatalf("frame bytes %q: %v", data, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta screencastManifest
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if !meta.StartedWall.Equal(start) || !meta.StoppedWall.Equal(start.Add(time.Second)) || meta.Frames != 5 || meta.Reconnects != 0 {
		t.Fatalf("manifest = %+v", meta)
	}
}

func TestScreencastReconnectsAndAppendsFrames(t *testing.T) {
	url, firstAcks, _ := fakeScreencast(t, 3, 1)
	replacedURL, nextAcks, done := fakeScreencast(t, 3, 0)
	start := time.Unix(1000, 0).UTC()
	var ticks, resolves atomic.Int32
	var problems atomic.Int32
	dir := t.TempDir()
	r := newScreencast(context.Background(), dir, start,
		func() time.Time { return start.Add(time.Duration(ticks.Add(1)) * time.Second) },
		func() time.Duration { return time.Duration(ticks.Load()) * time.Second },
		func(context.Context) (string, error) {
			if resolves.Add(1) == 1 {
				return url, nil
			}
			return replacedURL, nil
		},
		func(error) { problems.Add(1) })
	t.Cleanup(func() { _ = r.stop(start.Add(7 * time.Second)) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not reconnect")
	}
	if err := r.stop(start.Add(7 * time.Second)); err != nil {
		t.Fatal(err)
	}
	frames := readFrameIndex(t, dir)
	acks := firstAcks.Load() + nextAcks.Load()
	if resolves.Load() != 2 || acks != 6 || len(frames) != 6 || r.meta.Frames != 6 || r.meta.Reconnects != 1 || problems.Load() != 1 {
		t.Fatalf("resolves %d, acks %d, frames %v, manifest %+v, problems %d", resolves.Load(), acks, frames, r.meta, problems.Load())
	}
	for i, frame := range frames {
		if frame.N != i+1 {
			t.Fatalf("index restarted after disconnect: %v", frames)
		}
	}
}

func TestScreencastKeepsRecordingAtOneFPSAfterFrameCap(t *testing.T) {
	url, acks, done := fakeScreencast(t, 6, 0)
	start := time.Unix(1000, 0).UTC()
	times := []time.Duration{0, 200 * time.Millisecond, 900 * time.Millisecond, time.Second, 1900 * time.Millisecond, 2 * time.Second}
	var ticks atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	r := &screencast{ctx: ctx, cancel: cancel, done: make(chan struct{}), dir: t.TempDir(),
		clock:   func() time.Time { return start.Add(times[ticks.Add(1)-1]) },
		elapsed: func() time.Duration { return times[ticks.Load()-1] },
		resolve: func(context.Context) (string, error) { return url, nil }, trouble: func(err error) { t.Error(err) },
		meta: screencastManifest{StartedWall: start, Frames: screencastFrameCap}}
	go r.run()
	t.Cleanup(func() { _ = r.stop(start.Add(3 * time.Second)) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("frames were not acknowledged after cap")
	}
	if err := r.stop(start.Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	frames := readFrameIndex(t, r.dir)
	if acks.Load() != 6 || len(frames) != 3 || r.meta.Frames != screencastFrameCap+3 {
		t.Fatalf("acks %d, kept %v, total %d", acks.Load(), frames, r.meta.Frames)
	}
	for i, frame := range frames {
		if frame.N != screencastFrameCap+i+1 || frame.WallMS != start.Add(time.Duration(i)*time.Second).UnixMilli() {
			t.Errorf("frame %d = %+v", i, frame)
		}
	}
}

func TestFrameConcatListKeepsHoldsAndRepeatsLastFrame(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	frames := []screencastFrame{{N: 1, WallMS: start.Add(time.Second).UnixMilli()}, {N: 2, WallMS: start.Add(61 * time.Second).UnixMilli()}}
	list, err := frameConcatList("/tmp/leg's", frames, screencastManifest{StartedWall: start, StoppedWall: start.Add(70 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	want := "file '/tmp/leg'\\''s/frames/000001.jpg'\nduration 61.000\nfile '/tmp/leg'\\''s/frames/000002.jpg'\nduration 9.000\nfile '/tmp/leg'\\''s/frames/000002.jpg'\n"
	if list != want {
		t.Fatalf("concat list:\n%s\nwant:\n%s", list, want)
	}
	if _, err := frameConcatList("/tmp", nil, screencastManifest{}); err == nil {
		t.Fatal("empty recording accepted")
	}
	if _, err := frameConcatList("/tmp", frames, screencastManifest{StartedWall: start, StoppedWall: start}); err == nil {
		t.Fatal("backwards stop time accepted")
	}
}

func TestScreencastArgs(t *testing.T) {
	want := []string{"-y", "-f", "concat", "-safe", "0", "-i", "list.txt", "-vf",
		"scale=1320:780:force_original_aspect_ratio=decrease,pad=1320:780:(ow-iw)/2:(oh-ih)/2,format=yuv420p,fps=5",
		"-r", "5", "-fps_mode", "cfr", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-g", "5", "-movflags", "+faststart", "-t", "65.000", "out.mp4"}
	if got := screencastArgs("list.txt", "out.mp4", 65*time.Second); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v", got)
	}
}

func TestScreencastStopWaitsForFinalFrame(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "frames"), 0o750); err != nil {
		t.Fatal(err)
	}
	index, err := os.Create(filepath.Join(dir, "frames.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = index.Close() }()
	start := time.Unix(1000, 0).UTC()
	entered, release, saved := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	done := make(chan struct{})
	close(done)
	r := &screencast{dir: dir, done: done, cancel: func() {}, clock: func() time.Time {
		close(entered)
		<-release
		return start.Add(time.Second)
	}, elapsed: func() time.Duration { return time.Second }}
	framesAtStop := 0
	r.cancel = func() { framesAtStop = r.meta.Frames }
	go func() { saved <- r.keepFrame(index, base64.StdEncoding.EncodeToString([]byte("jpeg"))) }()
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- r.stop(start.Add(500 * time.Millisecond)) }()
	close(release)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	// Wall time is for display and can precede the last frame after a clock
	// step. The elapsed duration must still cover that frame once stop returns.
	if !r.stopped.Equal(start.Add(500*time.Millisecond)) || r.stoppedDuration < r.lastOffset.Milliseconds() || r.meta.Frames != 1 || framesAtStop != 1 {
		t.Fatalf("stop %s, duration %dms, final offset %s, frames %d, frames at stop %d", r.stopped, r.stoppedDuration, r.lastOffset, r.meta.Frames, framesAtStop)
	}
	if frames := readFrameIndex(t, dir); len(frames) != 1 || frames[0].OffsetMS == nil || *frames[0].OffsetMS > r.stoppedDuration {
		t.Fatalf("stop did not cover saved final frame: %+v", frames)
	}
	if err := r.keepFrame(index, "ignored after stop"); err != nil || r.meta.Frames != 1 {
		t.Fatalf("frame accepted after stop: %v", err)
	}
}

func TestScreencastRetriesInitialResolutionAndNotesUnexpectedCancellation(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	ctx, cancel := context.WithCancel(context.Background())
	var resolves atomic.Int32
	retried := make(chan struct{})
	r := newScreencast(ctx, t.TempDir(), start, func() time.Time { return start.Add(time.Second) }, func() time.Duration { return time.Second },
		func(ctx context.Context) (string, error) {
			if resolves.Add(1) == 1 {
				return "", errors.New("page restarting")
			}
			close(retried)
			<-ctx.Done()
			return "", ctx.Err()
		}, nil)
	t.Cleanup(func() { _ = r.stop(start.Add(time.Second)) })
	select {
	case <-retried:
	case <-time.After(5 * time.Second):
		t.Fatal("initial page resolution was not retried")
	}
	cancel() // the engine disappears before stop is requested
	<-r.done
	if r.meta.Reconnects != 1 || len(r.meta.Problems) != 2 || !strings.Contains(r.meta.Problems[1], "capture ended before requested stop") {
		t.Fatalf("manifest %+v", r.meta)
	}
}

func TestBuildRecordingOnlyDeletesFramesAfterDurationCheck(t *testing.T) {
	for _, duration := range []string{"69", "3", "invalid"} {
		t.Run(duration, func(t *testing.T) {
			s := New(NewStore(t.TempDir()), nil)
			s.media = &fakeMedia{}
			s.runner = &videoProbeRunner{duration: duration}
			dir := s.store.legDir("run_s1", "head")
			if err := os.MkdirAll(filepath.Join(dir, "frames"), 0o750); err != nil {
				t.Fatal(err)
			}
			start := time.Unix(1000, 0).UTC()
			meta, _ := json.Marshal(screencastManifest{StartedWall: start, StoppedWall: start.Add(70 * time.Second), Frames: 1})
			for name, data := range map[string][]byte{"manifest.json": meta, "frames.jsonl": []byte(`{"n":1,"wall_ms":1000000}` + "\n"), "frames/000001.jpg": []byte("jpeg")} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := s.buildRecording(context.Background(), "run_s1", "head")
			_, statErr := os.Stat(filepath.Join(dir, "frames"))
			if duration == "69" {
				if err != nil || !os.IsNotExist(statErr) {
					t.Fatalf("successful build: %v, frames stat: %v", err, statErr)
				}
			} else if err == nil || statErr != nil {
				t.Fatalf("bad duration: %v, frames stat: %v", err, statErr)
			} else if _, err := os.Stat(recordingPath(filepath.Join(s.store.root, "run_s1"), "head")); !os.IsNotExist(err) {
				t.Fatalf("invalid-duration mp4 left available for clips: %v", err)
			}
		})
	}
}

func TestRecordingLifecycleBuildsOnceWithoutEngineRecorder(t *testing.T) {
	url, _, done := fakeScreencast(t, 1, 0)
	svc, engine, _, runDir := driveSvc(t)
	run, err := svc.store.Get("run_s1")
	if err != nil {
		t.Fatal(err)
	}
	oldClient := httpClient
	httpClient = &http.Client{Transport: pageTargetsTransport{t, `[{"id":"app","type":"page","webSocketDebuggerUrl":"` + url + `"}]`}}
	t.Cleanup(func() { httpClient = oldClient })
	engine.out["tab"] = "→ [t1] App - http://app.test/\n"
	engine.hook = func(args []string) {
		if len(args) == 3 && args[0] == "tab" && args[2] == "--json" {
			engine.out["tab"] = `{"data":{"tabs":[{"tabId":"t1","targetId":"app"}]}}`
		}
	}
	svc.engineFor(run.ID, run.Legs[0]).tab = "t1"
	var wall atomic.Int64
	wall.Store(time.Unix(1000, 0).UnixMilli())
	svc.clock = func() time.Time { return time.UnixMilli(wall.Load()).UTC() }
	svc.runner = &videoProbeRunner{duration: "70"}
	media := &fakeMedia{}
	svc.media = media
	leg := svc.startRecording(context.Background(), run.ID, run.Legs[0])
	if leg.RecordStartedAt.IsZero() {
		t.Fatal("recording did not start")
	}
	t.Cleanup(func() {
		svc.recorderMu.Lock()
		r := svc.recorders[run.ID+"/head"]
		svc.recorderMu.Unlock()
		if r != nil {
			_ = r.stop(svc.clock())
		}
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recording received no frame")
	}
	run, err = svc.update(run.ID, func(run *Run) { run.Legs[0] = leg })
	if err != nil {
		t.Fatal(err)
	}
	svc.recorders[run.ID+"/head"].mu.Lock()
	svc.recorders[run.ID+"/head"].elapsed = func() time.Duration { return 70 * time.Second }
	svc.recorders[run.ID+"/head"].mu.Unlock()
	wall.Add(70000)
	run = svc.stopRecording(context.Background(), run)
	if !run.Legs[0].RecordStoppedAt.Equal(svc.clock()) || len(run.Gaps) != 0 || len(media.calls) != 1 {
		t.Fatalf("stopped run %+v; media %v", run, media.calls)
	}
	if _, err := os.Stat(recordingPath(runDir, "head")); err != nil {
		t.Fatal(err)
	}
	svc.stopRecording(context.Background(), run)
	if len(media.calls) != 1 {
		t.Fatal("recording built twice")
	}
	for _, args := range engine.calls {
		if args[0] == "record" {
			t.Fatalf("live engine encoder invoked: %v", args)
		}
	}
}

func TestRecordingTroubleIsLoggedAndNotedWithoutFailingRun(t *testing.T) {
	svc, engine, _, runDir := driveSvc(t)
	run, _ := svc.store.Get("run_s1")
	engine.fail["tab"] = errors.New("page unavailable")
	leg := svc.startRecording(context.Background(), run.ID, run.Legs[0])
	if !leg.RecordStartedAt.IsZero() {
		t.Fatal("missing page started recording")
	}
	run, _ = svc.store.Get(run.ID)
	log, err := os.ReadFile(filepath.Join(runDir, "head", "engine.log"))
	if err != nil || !strings.Contains(string(log), "page unavailable") || len(run.Gaps) != 1 || run.Gaps[0] != "The PR build's recording was not available: agent-browser tab: page unavailable." {
		t.Fatalf("log %q, gaps %v, error %v", log, run.Gaps, err)
	}
	// A daemon restart can leave persisted recording times without an in-memory
	// capture. Closing that run still succeeds and explains the missing video.
	run, err = svc.update(run.ID, func(run *Run) { run.Legs[0].RecordStartedAt = svc.clock() })
	if err != nil {
		t.Fatal(err)
	}
	run = svc.stopRecording(context.Background(), run)
	if run.Legs[0].RecordStoppedAt.IsZero() || len(run.Gaps) != 2 {
		t.Fatalf("stop did not settle missing recorder: %+v", run)
	}
}

func TestRecordingGapsNameBothBuildsAndReachCommentsAsLimits(t *testing.T) {
	svc, _, _, runDir := driveSvc(t)
	run, _ := svc.store.Get("run_s1")
	for _, leg := range []string{"head", "base"} {
		svc.recordingProblem(run.ID, leg, errors.New("page unavailable"))
	}
	run, _ = svc.store.Get(run.ID)
	want := []string{
		"The PR build's recording was not available: page unavailable.",
		"The main build's recording was not available: page unavailable.",
	}
	if !slices.Equal(run.Gaps, want) {
		t.Fatalf("gaps %q, want %q", run.Gaps, want)
	}
	v := commentVerdict(commentChecks(t))
	md := renderVerdict(run, v, false, nil, runDir, svc.clock(), nil)
	before, limits, ok := strings.Cut(md, "## Limits\n")
	if !ok || strings.Contains(before, want[0]) || strings.Contains(before, want[1]) {
		t.Fatalf("recording gaps are not verdict limits:\n%s", md)
	}
	for _, body := range []string{limits, renderComment(run, v, evidence{}, svc.clock()), summaryCommentBody(run, v, svc.clock(), true)} {
		for _, gap := range want {
			if !strings.Contains(body, gap) {
				t.Errorf("missing %q:\n%s", gap, body)
			}
		}
	}
}

func recordingFixture(t *testing.T, svc *Service, runID string, manifest bool) screencastManifest {
	t.Helper()
	dir := svc.store.legDir(runID, "head")
	start := time.Unix(1000, 0).UTC()
	meta := screencastManifest{StartedWall: start, StoppedWall: start.Add(70 * time.Second), Frames: 2, Reconnects: 1, Problems: []string{"screencast disconnected"}}
	writeFile(t, filepath.Join(dir, "frames", "000001.jpg"), "jpeg")
	writeFile(t, filepath.Join(dir, "frames", "000002.jpg"), "jpeg")
	writeFile(t, filepath.Join(dir, "frames.jsonl"), "{\"n\":1,\"wall_ms\":1000000}\n{\"n\":2,\"wall_ms\":1061000}\n")
	if manifest {
		data, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "manifest.json"), string(data))
	}
	return meta
}

func TestStopRecordingRecoversOrphanFrames(t *testing.T) {
	for _, manifest := range []bool{false, true} {
		t.Run(fmt.Sprint(manifest), func(t *testing.T) {
			svc, _, _, runDir := driveSvc(t)
			run, err := svc.store.Get("run_s1")
			if err != nil {
				t.Fatal(err)
			}
			meta := recordingFixture(t, svc, run.ID, manifest)
			duration := "61"
			if manifest {
				duration = "70"
			}
			svc.runner = &videoProbeRunner{duration: duration}
			media := &fakeMedia{}
			svc.media = media
			run.Legs[0].RecordStartedAt = meta.StartedWall
			if err := svc.store.Save(run); err != nil {
				t.Fatal(err)
			}
			run = svc.stopRecording(context.Background(), run)
			if len(run.Gaps) != 0 || len(media.calls) != 1 || run.Legs[0].RecordStoppedAt.IsZero() {
				t.Fatalf("run %+v, calls %v", run, media.calls)
			}
			if !fileExists(recordingPath(runDir, "head")) || pathExists(filepath.Join(runDir, "head", "frames")) {
				t.Fatal("orphan recording was not encoded and cleared")
			}
			wantStop := meta.StoppedWall
			if !manifest {
				wantStop = meta.StartedWall.Add(61 * time.Second)
			}
			if !run.Legs[0].RecordStoppedAt.Equal(wantStop) {
				t.Fatalf("stop %s, want %s", run.Legs[0].RecordStoppedAt, wantStop)
			}
		})
	}
}

func TestStopRecordingCaptureTroubleWithPlayableVideoHasNoGap(t *testing.T) {
	svc, _, _, runDir := driveSvc(t)
	run, _ := svc.store.Get("run_s1")
	meta := recordingFixture(t, svc, run.ID, true)
	svc.runner = &videoProbeRunner{duration: "70"}
	svc.media = &fakeMedia{}
	done := make(chan struct{})
	close(done)
	r := &screencast{done: done, cancel: func() {}, meta: meta, elapsed: func() time.Duration { return 70 * time.Second }, trouble: func(err error) { svc.engineLog(run.ID, "head", "recording: "+err.Error()) }}
	r.meta.Problems = nil
	r.problem(errors.New("screencast disconnected"))
	svc.recorders = map[string]*screencast{run.ID + "/head": r}
	run = svc.stopRecording(context.Background(), run)
	log, err := os.ReadFile(filepath.Join(runDir, "head", "engine.log"))
	if err != nil || !strings.Contains(string(log), "disconnected") || len(run.Gaps) != 0 || !fileExists(recordingPath(runDir, "head")) {
		t.Fatalf("run %+v, log %q, err %v", run, log, err)
	}
}

func TestScreencastBackwardWallClockUsesElapsedDurations(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "frames"), 0o750); err != nil {
		t.Fatal(err)
	}
	index, err := os.Create(filepath.Join(dir, "frames.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = index.Close() }()
	start := time.Unix(1000, 0).UTC()
	wall, elapsed := start, time.Duration(0)
	done := make(chan struct{})
	close(done)
	r := &screencast{dir: dir, done: done, cancel: func() {}, clock: func() time.Time { return wall }, elapsed: func() time.Duration { return elapsed }, meta: screencastManifest{StartedWall: start}}
	for i := range 3 {
		wall = start.Add(-time.Duration(i) * time.Minute)
		elapsed = time.Duration(i) * time.Second
		if err := r.keepFrame(index, base64.StdEncoding.EncodeToString([]byte("jpeg"))); err != nil {
			t.Fatal(err)
		}
	}
	elapsed = 3 * time.Second
	if err := r.stop(wall.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	ms := r.stoppedDuration
	r.meta.StoppedWall = r.stopped
	r.meta.DurationMS = &ms
	frames := readFrameIndex(t, dir)
	if len(frames) != 3 {
		t.Fatalf("kept %d frames", len(frames))
	}
	list, err := frameConcatList(dir, frames, r.meta)
	if err != nil || strings.Count(list, "duration 1.000") != 3 || recordingLength(r.meta) != 3*time.Second {
		t.Fatalf("concat %q, meta %+v, error %v", list, r.meta, err)
	}
}

func TestScreencastFrameWriteTroubleIsNormalizedAndBounded(t *testing.T) {
	dir := t.TempDir()
	index, err := os.Create(filepath.Join(dir, "frames.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = index.Close() }()
	logs := []string{}
	r := &screencast{dir: dir, clock: time.Now, elapsed: func() time.Duration { return time.Second }, trouble: func(err error) { logs = append(logs, err.Error()) }}
	for i := range 100 {
		r.meta.Frames = i // each failed write has a different destination path
		err := r.keepFrame(index, base64.StdEncoding.EncodeToString([]byte("jpeg")))
		if err == nil {
			t.Fatal("write unexpectedly succeeded")
		}
		r.problem(fmt.Errorf("save screencast frame: %w", err))
	}
	if len(logs) != 1 || strings.Contains(logs[0], ".jpg") {
		t.Fatalf("frame write logs %v", logs)
	}
	for i := range 100 {
		r.problem(fmt.Errorf("other trouble %d", i))
	}
	if len(logs) != 20 || len(r.meta.Problems) != 20 {
		t.Fatalf("logs %d, problems %d", len(logs), len(r.meta.Problems))
	}
}

func TestRecordingEncodeTimeoutScalesWithFrames(t *testing.T) {
	if got := recordingEncodeTimeout(8000); got != videoTimeout+800*time.Second {
		t.Fatalf("8000-frame timeout %s", got)
	}
	if recordingEncodeTimeout(8000) <= recordingEncodeTimeout(1) {
		t.Fatal("timeout did not scale")
	}
}
