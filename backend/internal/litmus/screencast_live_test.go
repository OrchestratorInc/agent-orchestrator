package litmus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const (
	liveScreencastMinutes = 25
	syntheticEncodeFrames = 8000
	syntheticFrameWidth   = 1320
	syntheticFrameHeight  = 780
)

// TestLiveScreencastEncode25Minutes exercises the real ffmpeg path without a browser. It writes a full-size,
// synthetic 25-minute leg at roughly 5 fps, then measures the encode and validates the resulting MP4.
// LITMUS_SCREENCAST_ENCODE=1 go test -run '^TestLiveScreencastEncode25Minutes$' -timeout 10m -v ./internal/litmus/
func TestLiveScreencastEncode25Minutes(t *testing.T) {
	if os.Getenv("LITMUS_SCREENCAST_ENCODE") != "1" {
		t.Skip("set LITMUS_SCREENCAST_ENCODE=1")
	}
	svc := New(NewStore(t.TempDir()), nil)
	runID, leg := "run_encode", "head"
	dir := svc.store.legDir(runID, leg)
	is(t, "")(nil, os.MkdirAll(filepath.Join(dir, "frames"), 0o750))
	frames := syntheticJPEGFrames(t)
	config, err := jpeg.DecodeConfig(bytes.NewReader(frames[0]))
	is(t, "")(nil, err)
	if config.Width != syntheticFrameWidth || config.Height != syntheticFrameHeight {
		t.Fatalf("synthetic frame = %dx%d, want %dx%d", config.Width, config.Height, syntheticFrameWidth, syntheticFrameHeight)
	}
	start := time.Unix(1000, 0).UTC()
	duration := time.Duration(liveScreencastMinutes) * time.Minute
	index, err := os.OpenFile(filepath.Join(dir, "frames.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	is(t, "")(nil, err)
	for i := 0; i < syntheticEncodeFrames; i++ {
		data := frames[i%len(frames)]
		is(t, "")(nil, os.WriteFile(filepath.Join(dir, "frames", fmt.Sprintf("%06d.jpg", i+1)), data, 0o600))
		offsetMS := int64(i) * duration.Milliseconds() / int64(syntheticEncodeFrames-1)
		frame, err := json.Marshal(map[string]any{
			"n": i + 1, "wall_ms": start.Add(time.Duration(offsetMS) * time.Millisecond).UnixMilli(), "offset_ms": offsetMS,
		})
		is(t, "")(nil, err)
		_, err = index.Write(append(frame, '\n'))
		is(t, "")(nil, err)
	}
	is(t, "")(nil, index.Close())
	meta, err := json.Marshal(map[string]any{
		"startedWall": start, "stoppedWall": start.Add(duration), "frames": syntheticEncodeFrames, "duration_ms": duration.Milliseconds(),
	})
	is(t, "")(nil, err)
	is(t, "")(nil, os.WriteFile(filepath.Join(dir, "manifest.json"), meta, 0o600))
	started := time.Now()
	is(t, "")(nil, svc.buildRecording(context.Background(), runID, leg))
	encodeSeconds := time.Since(started).Seconds()
	path := recordingPath(filepath.Join(svc.store.root, runID), leg)
	if videoDuration := liveFFProbeDuration(t, path); math.Abs(videoDuration-duration.Seconds()) > 2 {
		t.Fatalf("synthetic encode duration %.3fs, expected %.3fs", videoDuration, duration.Seconds())
	}
	liveFFmpegDecode(t, path)
	t.Logf("synthetic 25-minute encode: %.3fs for %d JPEG frames at %dx%d; clean decode", encodeSeconds, syntheticEncodeFrames, syntheticFrameWidth, syntheticFrameHeight)
}

func syntheticJPEGFrames(t *testing.T) [][]byte {
	t.Helper()
	const variants = 120
	img := image.NewRGBA(image.Rect(0, 0, syntheticFrameWidth, syntheticFrameHeight))
	frames := make([][]byte, 0, variants)
	for frame := 0; frame < variants; frame++ {
		phase := frame * 7
		for y := 0; y < syntheticFrameHeight; y++ {
			row := img.Pix[y*img.Stride:]
			for x := 0; x < syntheticFrameWidth; x++ {
				p := x * 4
				stripe := (x/24 + y/18 + frame) % 2
				row[p] = uint8((x*3 + phase + y/3) % 256)
				row[p+1] = uint8((y*2 + phase*2 + x/5) % 256)
				row[p+2] = uint8((x + y + phase*3) % 256)
				if stripe == 0 {
					row[p], row[p+1] = row[p+1], row[p]
				}
				row[p+3] = 255
			}
		}
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 70}); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, encoded.Bytes())
	}
	return frames
}

// TestLiveScreencastSparseEncode exercises real ffmpeg without a browser. JPEG EOF estimation and output trim must
// preserve a 60-second hold and a 5-second tail.
// LITMUS_SCREENCAST_ENCODE=1 go test -run '^TestLiveScreencastSparseEncode$' -v ./internal/litmus/
func TestLiveScreencastSparseEncode(t *testing.T) {
	if os.Getenv("LITMUS_SCREENCAST_ENCODE") != "1" {
		t.Skip("set LITMUS_SCREENCAST_ENCODE=1")
	}
	svc := New(NewStore(t.TempDir()), nil)
	dir := svc.store.legDir("run_sparse", "head")
	is(t, "")(nil, os.MkdirAll(filepath.Join(dir, "frames"), 0o750))
	for i, ink := range []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}} {
		img := image.NewRGBA(image.Rect(0, 0, 16, 16))
		for y := range 16 {
			for x := range 16 {
				img.SetRGBA(x, y, ink)
			}
		}
		file, err := os.Create(filepath.Join(dir, "frames", fmt.Sprintf("%06d.jpg", i+1)))
		is(t, "")(nil, err)
		err = jpeg.Encode(file, img, nil)
		is(t, "")(nil, file.Close())
		is(t, "")(nil, err)
	}
	start := time.Unix(1000, 0).UTC()
	meta, _ := json.Marshal(screencastManifest{StartedWall: start, StoppedWall: start.Add(65 * time.Second), Frames: 2})
	is(t, "")(nil, os.WriteFile(filepath.Join(dir, "manifest.json"), meta, 0o600))
	is(t, "")(nil, os.WriteFile(filepath.Join(dir, "frames.jsonl"), []byte("{\"n\":1,\"wall_ms\":1000000}\n{\"n\":2,\"wall_ms\":1060000}\n"), 0o600))
	is(t, "")(nil, svc.buildRecording(context.Background(), "run_sparse", "head"))
	path := recordingPath(filepath.Join(svc.store.root, "run_sparse"), "head")
	if duration := liveFFProbeDuration(t, path); math.Abs(duration-65) > 0.2 {
		t.Fatalf("sparse encode duration %.3fs, expected 65s", duration)
	}
	liveFFmpegDecode(t, path)
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		bin = homebrewFFmpeg
	}
	for _, sample := range []struct {
		at   string
		blue bool
	}{{"59.8", false}, {"60.0", true}, {"64.8", true}} {
		pixel, err := exec.Command(bin, "-v", "error", "-ss", sample.at, "-i", path, "-frames:v", "1", "-vf", "scale=1:1", "-pix_fmt", "rgb24", "-f", "rawvideo", "-").Output()
		if err != nil || len(pixel) != 3 || (pixel[2] > pixel[0]) != sample.blue {
			t.Fatalf("color at %ss: %v, error %v", sample.at, pixel, err)
		}
	}
	t.Log("65.000s at 5 fps; 60s hold and 5s tail preserved; clean decode")
}

// TestLiveScreencastElectron25Minutes is the long proof for the real Electron target. It keeps the own
// screencast recorder running while agent-browser screenshots happen every five seconds and a theme pair is taken
// every minute, then checks that the encoded file covers the whole recording and decodes without ffmpeg errors.
//
// LITMUS_LIVE=1 go test -run TestLiveScreencastElectron25Minutes -timeout 60m -v ./internal/litmus/
func TestLiveScreencastElectron25Minutes(t *testing.T) {
	if os.Getenv("LITMUS_LIVE") != "1" {
		t.Skip("set LITMUS_LIVE=1")
	}
	src, _ := filepath.Abs("../../..")
	data, err := os.MkdirTemp("/tmp", "lv-sc")
	is(t, "")(nil, err)
	svc := New(NewStore(data), nil).WithRepos(func(context.Context, ...string) (string, error) { return src, nil })
	r, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	if svc.binary, err = resolveBinary(filepath.Join(src, "frontend", "agent-browser", "agent-browser"), ""); err != nil {
		t.Fatal(err)
	}
	t.Logf("proof data and recording: %s", data)
	t.Cleanup(func() { _, _ = svc.Cleanup(context.Background(), "live") })

	const sha = "e2204e0f361867a28310e71421a4227ab1d09544"
	leg, err := svc.prepareLeg(context.Background(), DefaultRepo, r, "head", sha)
	is(t, "")(nil, err)
	// The normal run budget starts at prepare. Give this opt-in proof enough room for the build/launch tail plus the
	// full 25-minute recording window, so the screenshot calls are not rejected by the run deadline near the end.
	is(t, "")(nil, svc.store.Save(Run{ID: "run_live", SessionID: "live", Repo: DefaultRepo, PRNumber: 6394, HeadSHA: sha, State: RunPrepared, CreatedAt: time.Now(), Deadline: time.Now().Add((liveScreencastMinutes + 10) * time.Minute), Legs: []Leg{leg}}))
	is(t, "")(svc.CheckAdd(context.Background(), "live", "open chat", "the sample chat opens", ""))
	is(t, "")(svc.CheckAdd(context.Background(), "live", "model menu", "the model control opens", ""))
	run, err := svc.Launch(context.Background(), "live", "head")
	is(t, "")(nil, err)
	if len(run.Legs) != 1 || run.Legs[0].RecordStartedAt.IsZero() {
		t.Fatalf("Electron launch did not start recording: %+v", run.Legs)
	}
	e := svc.engineFor(run.ID, run.Legs[0])
	proofCtx, cancel := context.WithTimeout(context.Background(), (liveScreencastMinutes+2)*time.Minute)
	defer cancel()
	deadline := time.NewTimer(liveScreencastMinutes * time.Minute)
	shots := time.NewTicker(5 * time.Second)
	themes := time.NewTicker(time.Minute)
	defer deadline.Stop()
	defer shots.Stop()
	defer themes.Stop()
	themeN, shotN := 0, 0
	for {
		select {
		case <-proofCtx.Done():
			t.Fatal(proofCtx.Err())
		case <-deadline.C:
			goto stopped
		case <-shots.C:
			shotN++
			if _, err := svc.Drive(proofCtx, "live", "screenshot", []string{"--leg", "head"}); err != nil {
				t.Fatalf("screenshot during recording: %v", err)
			}
		case <-themes.C:
			themeN++
			e.mu.Lock()
			pair, note := svc.themePair(proofCtx, e, fmt.Sprintf("screencast-proof-%02d", themeN))
			e.mu.Unlock()
			if len(pair) == 0 {
				t.Fatalf("theme pair %d produced no screenshots: %s", themeN, note)
			}
			if note != "" {
				t.Logf("theme pair %d: %s", themeN, note)
			}
		}
	}

stopped:
	run, err = svc.store.Get("run_live")
	is(t, "")(nil, err)
	started := run.Legs[0].RecordStartedAt
	svc.stopRecording(proofCtx, run)
	run, err = svc.store.Get("run_live")
	is(t, "")(nil, err)
	stoppedAt := run.Legs[0].RecordStoppedAt
	if started.IsZero() || stoppedAt.IsZero() || !stoppedAt.After(started) {
		t.Fatalf("recording wall interval: started %s, stopped %s", started, stoppedAt)
	}
	path := recordingPath(filepath.Join(svc.store.root, run.ID), "head")
	duration := liveFFProbeDuration(t, path)
	wall := stoppedAt.Sub(started).Seconds()
	if delta := math.Abs(duration - wall); delta > wall*0.02 {
		t.Fatalf("recording duration %.3fs differs from wall time %.3fs by %.3fs (>2%%)", duration, wall, delta)
	}
	liveFFmpegDecode(t, path)
	t.Logf("Electron screencast proof: %.3fs video, %.3fs wall, %.3fs difference; %d screenshots, %d theme pairs", duration, wall, math.Abs(duration-wall), shotN, themeN)
}

// TestLiveScreencastStandalone checks the recorder against any already-running CDP page. It is useful for a long
// recorder run without preparing an AO target: the supplied page WebSocket is used both to resolve screencasts and to
// issue Page.captureScreenshot every five seconds.
//
// LITMUS_SCREENCAST_STANDALONE=1 LITMUS_SCREENCAST_PAGE_WS=ws://127.0.0.1:9222/devtools/page/<id> LITMUS_SCREENCAST_MINUTES=25 go test -run TestLiveScreencastStandalone -timeout 35m -v ./internal/litmus/
func TestLiveScreencastStandalone(t *testing.T) {
	if os.Getenv("LITMUS_SCREENCAST_STANDALONE") != "1" {
		t.Skip("set LITMUS_SCREENCAST_STANDALONE=1")
	}
	pageURL := strings.TrimSpace(os.Getenv("LITMUS_SCREENCAST_PAGE_WS"))
	if pageURL == "" {
		t.Fatal("LITMUS_SCREENCAST_PAGE_WS is required")
	}
	minutesText := strings.TrimSpace(os.Getenv("LITMUS_SCREENCAST_MINUTES"))
	if minutesText == "" {
		t.Fatal("LITMUS_SCREENCAST_MINUTES is required")
	}
	minutes, err := strconv.Atoi(minutesText)
	if err != nil || minutes <= 0 {
		t.Fatalf("LITMUS_SCREENCAST_MINUTES must be a positive integer, got %q", minutesText)
	}

	data, err := os.MkdirTemp("/tmp", "litmus-sc-proof-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("proof data: %s", data)
	svc := New(NewStore(data), nil)
	const runID, leg = "run_standalone", "head"
	legDir := filepath.Join(svc.store.root, runID, leg)
	if err := os.MkdirAll(legDir, 0o750); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	recorder := newScreencast(context.Background(), legDir, started, time.Now, nil, func(context.Context) (string, error) {
		return pageURL, nil
	}, func(err error) { t.Logf("screencast trouble: %v", err) })
	recordingStopped := false
	defer func() {
		if !recordingStopped {
			_ = recorder.stop(time.Now())
		}
	}()

	deadline := time.NewTimer(time.Duration(minutes) * time.Minute)
	shots := time.NewTicker(5 * time.Second)
	defer deadline.Stop()
	defer shots.Stop()
	for {
		select {
		case <-deadline.C:
			goto stopped
		case <-shots.C:
			shotCtx, shotCancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := captureLiveScreenshot(shotCtx, pageURL)
			shotCancel()
			if err != nil {
				t.Fatalf("Page.captureScreenshot: %v", err)
			}
		}
	}

stopped:
	stoppedAt := time.Now()
	err = recorder.stop(stoppedAt)
	recordingStopped = true
	if err != nil {
		t.Fatalf("stop screencast: %v", err)
	}
	if err := svc.buildRecording(context.Background(), runID, leg); err != nil {
		t.Fatalf("build recording: %v", err)
	}
	path := recordingPath(filepath.Join(svc.store.root, runID), leg)
	duration := liveFFProbeDuration(t, path)
	liveFFmpegDecode(t, path)
	t.Logf("standalone ffprobe duration: %.3fs; mp4: %s", duration, path)
}

func captureLiveScreenshot(ctx context.Context, pageURL string) error {
	conn, _, err := websocket.Dial(ctx, pageURL, nil)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(32 << 20)
	page := &pageCDP{conn: conn}
	var result struct{ Data string }
	if err := page.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true}, &result); err != nil {
		return err
	}
	if result.Data == "" {
		return fmt.Errorf("Page.captureScreenshot returned no image data")
	}
	return nil
}

func liveFFProbeDuration(t *testing.T, path string) float64 {
	t.Helper()
	bin := "ffprobe"
	if _, err := exec.LookPath(bin); err != nil {
		bin = homebrewFFprobe
	}
	out, err := exec.Command(bin, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || duration <= 0 {
		t.Fatalf("ffprobe duration %q: %v", strings.TrimSpace(string(out)), err)
	}
	return duration
}

func liveFFmpegDecode(t *testing.T, path string) {
	t.Helper()
	bin := "ffmpeg"
	if _, err := exec.LookPath(bin); err != nil {
		bin = homebrewFFmpeg
	}
	out, err := exec.Command(bin, "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("ffmpeg decode %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
}
