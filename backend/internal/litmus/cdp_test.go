package litmus

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type pageTargetsTransport struct {
	t    *testing.T
	body string
}

func (f pageTargetsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != "/json/list" {
		f.t.Fatalf("unexpected CDP discovery request %s", req.URL)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(f.body)), Header: http.Header{}}, nil
}

type scriptedPageTargetsTransport struct {
	t       *testing.T
	bodies  []string
	entered chan struct{}
	release chan struct{}
	blockAt int
	calls   int
}

func (f *scriptedPageTargetsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != "/json/list" {
		f.t.Fatalf("unexpected CDP discovery request %s", req.URL)
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if f.calls >= len(f.bodies) {
		f.t.Fatalf("unexpected CDP discovery request %d", f.calls+1)
	}
	body := f.bodies[f.calls]
	f.calls++
	if f.entered != nil && f.calls-1 == f.blockAt {
		close(f.entered)
		select {
		case <-f.release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func TestPageURLMatchesPinnedTabTargetID(t *testing.T) {
	for _, tc := range []struct{ name, tabs, targets, want, errorText string }{
		{
			name: "reordered targets and duplicate URLs",
			tabs: `{"success":true,"data":{"tabs":[{"tabId":"t2","targetId":"browser"},{"tabId":"t1","targetId":"app"}]}}`,
			targets: `[{"id":"browser","type":"page","url":"http://app.test/","webSocketDebuggerUrl":"ws://browser"},
{"id":"app","type":"page","url":"http://app.test/","webSocketDebuggerUrl":"ws://app"}]`,
			want: "ws://app",
		},
		{
			name:      "pinned tab gone",
			tabs:      `{"success":true,"data":{"tabs":[{"tabId":"t2","targetId":"browser"}]}}`,
			targets:   `[{"id":"browser","type":"page","webSocketDebuggerUrl":"ws://browser"}]`,
			errorText: "pinned app tab",
		},
		{
			name:      "pinned target gone",
			tabs:      `{"success":true,"data":{"tabs":[{"tabId":"t1","targetId":"app"}]}}`,
			targets:   `[{"id":"browser","type":"page","webSocketDebuggerUrl":"ws://browser"}]`,
			errorText: "pinned app target",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, f, _, _ := driveSvc(t)
			run, _ := svc.store.Get("run_s1")
			e := svc.engineFor(run.ID, run.Legs[0])
			f.out["tab"] = "→ [t1] App - http://app.test/\n  [t2] Browser - http://app.test/\n"
			f.hook = func(args []string) {
				if len(args) == 3 && args[0] == "tab" && args[2] == "--json" {
					f.out["tab"] = tc.tabs
				}
			}
			oldClient := httpClient
			httpClient = &http.Client{Transport: pageTargetsTransport{t, tc.targets}}
			t.Cleanup(func() { httpClient = oldClient })
			got, err := e.pageURL(ctx)
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) || got != "" {
					t.Fatalf("missing pinned target returned %q, %v", got, err)
				}
			} else if err != nil || got != tc.want || e.tab != "t1" {
				t.Fatalf("target URL %q, tab %q, error %v; want %q on t1", got, e.tab, err, tc.want)
			}
		})
	}
}

func TestRecordingPageResolverPinsOnceAndReconnectsWithoutEngineLock(t *testing.T) {
	e := newEngine(EngineInfo{Binary: "fake", Endpoint: "http://debug", RunDir: t.TempDir()}, func(_ context.Context, _ string, args, _ []string) (string, error) {
		switch strings.Join(args[2:], " ") {
		case "stream disable":
			return "", nil
		case "tab list":
			return "→ [t1] App - http://app.test/\n", nil
		case "tab list --json":
			return `{"data":{"tabs":[{"tabId":"t1","targetId":"app"}]}}`, nil
		default:
			t.Fatalf("unexpected engine call %v", args)
			return "", nil
		}
	})
	entered := make(chan struct{})
	transport := &scriptedPageTargetsTransport{t: t, bodies: []string{
		`[{"id":"app","type":"page","webSocketDebuggerUrl":"ws://first"}]`,
		`[{"id":"app","type":"page","webSocketDebuggerUrl":"ws://reconnected"}]`,
	}, entered: entered, release: make(chan struct{}), blockAt: 1}
	oldClient := httpClient
	httpClient = &http.Client{Transport: transport}
	t.Cleanup(func() { httpClient = oldClient })

	first, resolve, err := e.recordingPageResolver(context.Background())
	if err != nil || first != "ws://first" || resolve == nil {
		t.Fatalf("initial recording resolver = %q, %v", first, err)
	}
	if transport.calls != 1 {
		t.Fatalf("initial discovery calls = %d, want 1", transport.calls)
	}

	// A reconnect must not wait for the action lock, even while discovery is
	// blocked on a dead tab's debug endpoint.
	e.mu.Lock()
	resolved := make(chan struct{})
	var got string
	var resolveErr error
	go func() {
		got, resolveErr = resolve(context.Background())
		close(resolved)
	}()
	select {
	case <-entered:
	case <-resolved:
		t.Fatal("reconnect did not reach direct CDP discovery")
	}
	close(transport.release)
	select {
	case <-resolved:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not finish")
	}
	e.mu.Unlock()
	if resolveErr != nil || got != "ws://reconnected" {
		t.Fatalf("reconnect resolver = %q, %v", got, resolveErr)
	}
	if transport.calls != 2 {
		t.Fatalf("reconnect discovery calls = %d, want 2", transport.calls)
	}
}

func TestRecordingPageResolverReconnectRespectsCancellation(t *testing.T) {
	e := newEngine(EngineInfo{Binary: "fake", Endpoint: "http://debug", RunDir: t.TempDir()}, func(_ context.Context, _ string, args, _ []string) (string, error) {
		switch strings.Join(args[2:], " ") {
		case "stream disable":
			return "", nil
		case "tab list":
			return "→ [t1] App - http://app.test/\n", nil
		case "tab list --json":
			return `{"data":{"tabs":[{"tabId":"t1","targetId":"app"}]}}`, nil
		default:
			t.Fatalf("unexpected engine call %v", args)
			return "", nil
		}
	})
	transport := &scriptedPageTargetsTransport{t: t, bodies: []string{
		`[{"id":"app","type":"page","webSocketDebuggerUrl":"ws://first"}]`,
	}}
	oldClient := httpClient
	httpClient = &http.Client{Transport: transport}
	t.Cleanup(func() { httpClient = oldClient })
	_, resolve, err := e.recordingPageResolver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.mu.Lock()
	_, err = resolve(ctx)
	e.mu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reconnect error = %v, want context canceled", err)
	}
	if transport.calls != 1 {
		t.Fatalf("canceled reconnect made a discovery request: %d calls", transport.calls)
	}
}
