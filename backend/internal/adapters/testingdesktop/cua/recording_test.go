package cua

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const validMovieInfo = "Duration: 3.368 seconds (2021/600)\nTrack count: 1\nTrack 1: Video 'vide'\n\tDimensions: 1280 x 800\n\tSystem support for decoding this track: Yes\nMovie analyzed with 0 error.\n"

type fakeRecording struct {
	process *recordingProcess
	args    []string
	env     []string
	signals []os.Signal
	path    string
	info    string
	finish  bool
	staged  string
}

func prepareRecording(t *testing.T, f *fixture) *fakeRecording {
	t.Helper()
	r := &fakeRecording{process: &recordingProcess{pid: 42, done: make(chan struct{})}, info: validMovieInfo, finish: true}
	f.adapter.stagingDir = filepath.Join(f.adapter.cfg.DataDir, "native-staging")
	if err := os.MkdirAll(f.adapter.stagingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	r.staged = filepath.Join(f.adapter.stagingDir, "b97e0c9f-1aac-4fe5-bd3e-18c05c7db732.mov")
	f.adapter.startRecorder = func(args, env []string, _, _ string) (*recordingProcess, error) {
		r.args, r.env = args, env
		r.path = args[len(args)-1]
		if err := os.WriteFile(r.staged, []byte("native movie staging"), 0o600); err != nil {
			return nil, err
		}
		return r.process, nil
	}
	r.process.signal = func(signal os.Signal) error {
		r.signals = append(r.signals, signal)
		if r.finish {
			if err := os.Rename(r.staged, r.path); err != nil {
				return err
			}
			close(r.process.done)
		}
		return nil
	}
	provider := f.runner.hook
	f.runner.hook = func(executable string, args []string) (Output, error) {
		if executable == "/usr/bin/avmediainfo" {
			if !reflect.DeepEqual(args, []string{r.path}) && !reflect.DeepEqual(args, []string{strings.TrimSuffix(r.path, ".mov") + "-recovered.mov"}) {
				t.Fatalf("analyzed wrong movie: %v", args)
			}
			return Output{Stdout: []byte(r.info)}, nil
		}
		return provider(executable, args)
	}
	return r
}

func startFakeRecording(t *testing.T, f *fixture) RecordingResult {
	t.Helper()
	result, err := f.adapter.StartRecording(context.Background(), f.target, filepath.Join(f.adapter.cfg.DataDir, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRecordingWindowOnlyAndSIGINTFinalization(t *testing.T) {
	t.Setenv("AO_FORBIDDEN", "secret")
	f := newFixture(t)
	r := prepareRecording(t, f)
	start := startFakeRecording(t, f)
	if !reflect.DeepEqual(r.args, []string{"-v", "-o", "-x", "-l456", start.Path}) || start.Duration != 0 || start.Gap != "" {
		t.Fatalf("wrong window recorder receipt or arguments: %+v %v", start, r.args)
	}
	for _, entry := range r.env {
		if strings.HasPrefix(entry, "AO_") {
			t.Fatal("inherited AO setting reached recorder")
		}
	}
	root, err := filepath.EvalSymlinks(filepath.Join(f.adapter.cfg.DataDir, "evidence"))
	if err != nil || filepath.Dir(start.Path) != root {
		t.Fatalf("movie escaped evidence dir: %s", start.Path)
	}
	final, err := f.adapter.StopRecording(context.Background(), f.target)
	if err != nil || final.Duration != 3368*time.Millisecond || final.Width != 1280 || final.Height != 800 || final.Gap != "" {
		t.Fatalf("movie was not validated: %+v %v", final, err)
	}
	if !reflect.DeepEqual(r.signals, []os.Signal{os.Interrupt}) {
		t.Fatalf("expected owned recorder SIGINT, got %v", r.signals)
	}
	if final.StagingPath != r.staged || final.StagingCleanup != "verified absent after final move" {
		t.Fatalf("staging UUID not verified: %+v", final)
	}
	again, err := f.adapter.StopRecording(context.Background(), f.target)
	if err != nil || again != final || len(r.signals) != 1 {
		t.Fatalf("stop was not idempotent: %+v %v", again, err)
	}
}

func TestRecordingStopAfterTargetClosed(t *testing.T) {
	f := newFixture(t)
	r := prepareRecording(t, f)
	startFakeRecording(t, f)
	f.adapter.started = func(_ context.Context, pid int) (time.Time, error) {
		if pid == f.target.ElectronPID || pid == f.adapter.driver.pid {
			return time.Time{}, errors.New("target and Driver already gone")
		}
		return f.born, nil
	}
	r.process.err = errors.New("stream ended when window closed")
	result, err := f.adapter.StopRecording(context.Background(), f.target)
	if err != nil || result.Duration <= 0 || result.Gap != "" {
		t.Fatalf("closed target prevented movie finalization: %+v %v", result, err)
	}
}

func TestRecordingRefusesHiddenTargetAndChangedRecorder(t *testing.T) {
	f := newFixture(t)
	r := prepareRecording(t, f)
	f.hidden = true
	result, err := f.adapter.StartRecording(context.Background(), f.target, filepath.Join(f.adapter.cfg.DataDir, "evidence"))
	if !errors.Is(err, ErrRefused) || result.Gap == "" || len(r.args) != 0 {
		t.Fatalf("hidden window reached recorder: %+v %v", result, err)
	}
	f.hidden = false
	startFakeRecording(t, f)
	f.adapter.started = func(_ context.Context, _ int) (time.Time, error) { return f.born.Add(time.Microsecond), nil }
	result, err = f.adapter.StopRecording(context.Background(), f.target)
	if !errors.Is(err, ErrRefused) || result.Gap == "" || len(r.signals) != 0 {
		t.Fatalf("changed recorder received a signal: %+v %v", result, err)
	}
}

func TestRecordingCancellationRetainsOwnership(t *testing.T) {
	f := newFixture(t)
	r := prepareRecording(t, f)
	r.finish = false
	startFakeRecording(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := f.adapter.StopRecording(ctx, f.target)
	if !errors.Is(err, context.Canceled) || result.Gap == "" || len(r.signals) != 1 {
		t.Fatalf("cancellation lost recorder cleanup: %+v %v", result, err)
	}
	if err := os.Rename(r.staged, r.path); err != nil {
		t.Fatal(err)
	}
	close(r.process.done)
	result, err = f.adapter.StopRecording(context.Background(), f.target)
	if err != nil || result.Duration <= 0 {
		t.Fatalf("retry lost movie: %+v %v", result, err)
	}
}

func TestRecordingRejectsInvalidMovieAndForeignStop(t *testing.T) {
	f := newFixture(t)
	r := prepareRecording(t, f)
	startFakeRecording(t, f)
	foreign := f.target
	foreign.WindowID = "999"
	if result, err := f.adapter.StopRecording(context.Background(), foreign); !errors.Is(err, ErrRefused) || result.Gap == "" || len(r.signals) != 0 {
		t.Fatalf("foreign target stopped recorder: %+v %v", result, err)
	}
	r.info = strings.ReplaceAll(validMovieInfo, "1280 x 800", "2880 x 1800")
	result, err := f.adapter.StopRecording(context.Background(), f.target)
	if !errors.Is(err, ErrRefused) || result.Gap == "" || result.Duration != 0 {
		t.Fatalf("display dimensions accepted as window video: %+v %v", result, err)
	}
	for _, info := range []string{"", strings.ReplaceAll(validMovieInfo, "3.368", "0"), strings.ReplaceAll(validMovieInfo, "Track count: 1", "Track count: 2"), strings.ReplaceAll(validMovieInfo, "0 error", "1 error"), strings.ReplaceAll(validMovieInfo, "track: Yes", "track: No")} {
		if _, _, _, err := parseMovieInfo(info); err == nil {
			t.Fatalf("invalid movie metadata accepted: %q", info)
		}
	}
}

func TestCloseStopsRecorderAndDriverEvenForInvalidMovie(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "movie gap"}[invalid], func(t *testing.T) {
			f := newFixture(t)
			r := prepareRecording(t, f)
			startFakeRecording(t, f)
			if invalid {
				r.info = "invalid movie"
			}
			stopped := false
			provider := f.runner.hook
			f.runner.hook = func(executable string, args []string) (Output, error) {
				if len(args) == 5 && args[3] == "end_session" {
					return jsonOutput(map[string]any{}), nil
				}
				if len(args) == 7 && args[4] == "stop" {
					if args[5] != "--expected-pid" || args[6] != "99" {
						t.Fatal("Driver stop lost owned PID")
					}
					stopped = true
					return Output{}, os.Remove(f.adapter.pidFile())
				}
				return provider(executable, args)
			}
			f.adapter.started = func(_ context.Context, pid int) (time.Time, error) {
				if pid == 99 && stopped {
					return time.Time{}, errors.New("owned Driver stopped")
				}
				return f.born, nil
			}
			err := f.adapter.Close(context.Background())
			if (err != nil) != invalid || !stopped || f.adapter.driver.pid != 0 || len(r.signals) != 1 {
				t.Fatalf("Close left recorder/Driver behind: invalid=%t err=%v driver=%+v signals=%v", invalid, err, f.adapter.driver, r.signals)
			}
		})
	}
}
