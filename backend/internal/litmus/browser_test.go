package litmus

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBrowserCommandsForwardAndKeepEvidence(t *testing.T) {
	for _, args := range [][]string{
		{"swipe", "left", "250"},
		{"click", "#save", "--button", "right"},
		{"network", "route", "**/api", "--body", `{"ok":true}`},
		{"tab", "new", "https://example.com"},
		{"screenshot", "#panel", "panel.png", "--full"},
		{"eval", "document.title = 'first\nsecond'; document.title"},
		{"future-command", "--new-option", "some value"},
	} {
		t.Run(args[0]+"/"+args[1], func(t *testing.T) {
			svc, f, _, dir := driveSvc(t)
			f.out[args[0]] = "upstream result"
			out, err := svc.Drive(ctx, "s1", "browser", append(append([]string{}, args...), "--leg", "head"))
			is(t, "")(nil, err)
			if !slices.ContainsFunc(f.calls, func(got []string) bool { return reflect.DeepEqual(got, args) }) {
				t.Fatalf("command %q missing from %q", args, f.calls)
			}
			rec, ok := out["receipt"].(actionRec)
			if !ok || rec.Action != args[0] || !reflect.DeepEqual(rec.Args, args[1:]) || rec.After == "" {
				t.Fatalf("missing command evidence: %+v", out)
			}
			if _, err := os.Stat(rec.After); err != nil {
				t.Fatal(err)
			}
			receipts, _ := svc.store.Receipts("run_s1")
			if len(receipts) != 1 || receipts[0].Action != args[0] {
				t.Fatalf("lost evidence: %+v", receipts)
			}
			if !strings.Contains(out["text"].(string), "upstream result") || !strings.Contains(out["text"].(string), "receipt 1:") {
				t.Fatalf("CLI lost command output or receipt: %+v", out)
			}
			exists(t, filepath.Join(dir, "actions.jsonl"))
		})
	}
}

func TestBrowserValidationAndLiteralArguments(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	for _, args := range [][]string{nil, {""}, {"connect", "http://other"}, {"--cdp", "9222", "snapshot"}, {"snapshot", "--session=other"}} {
		is(t, "TESTING_ARGS_INVALID")(svc.Drive(ctx, "s1", "browser", args))
	}
	if len(f.calls) != 0 {
		t.Fatalf("routing validation reached engine: %v", f.calls)
	}
	for _, args := range [][]string{{"click"}, {"click", "--new-tab"}} {
		f.fail["click"] = errors.New("upstream: click requires a selector")
		out, err := svc.Drive(ctx, "s1", "browser", args)
		is(t, "TESTING_ENGINE_FAILED")(out, err)
		if rec, ok := out["receipt"].(actionRec); !ok || !strings.Contains(rec.Reason, "requires a selector") {
			t.Fatalf("failed command lost its evidence: %+v", out)
		}
	}
	f.fail = map[string]error{}
	args := []string{"fill", "#input", "--leg=base"}
	is(t, "")(svc.Drive(ctx, "s1", "browser", append(args, "--leg", "")))
	if got := f.find("fill"); !reflect.DeepEqual(got, args) {
		t.Fatalf("literal argument changed: %q", got)
	}
	// A command need not be registered in Litmus or use the CLI wrapper.
	is(t, "")(svc.Drive(ctx, "s1", "cookies", []string{"clear"}))
}

func TestBrowserFailedBatchRetainsPartialWork(t *testing.T) {
	svc, f, _, _ := driveSvc(t)
	f.fail["batch"] = errors.New("second command failed after first ran")
	out, err := svc.Drive(ctx, "s1", "browser", []string{"batch", "--bail", "click #first", "click #missing"})
	is(t, "TESTING_ENGINE_FAILED")(out, err)
	if rec, ok := out["receipt"].(actionRec); !ok || rec.After == "" || rec.Reason == "" {
		t.Fatalf("failed batch has no evidence: %+v", out)
	}
}

func TestBrowserExplicitTabSelectionPersistsForReceipts(t *testing.T) {
	for _, command := range [][]string{{"tab", "t2"}, {"--json", "tab", "t2"}, {"tab", "--json", "t2"}} {
		t.Run(strings.Join(command, "/"), func(t *testing.T) {
			f := &fakeEngine{out: map[string]string{"tab": "→ [t1] App - http://localhost/\n  [t2] Other - http://localhost/other\n"}}
			e := newEngine(EngineInfo{Binary: "/fake/agent-browser", RunDir: t.TempDir(), Session: "s", Namespace: "s"}, f.exec)
			defer e.stop()
			is(t, "")(e.Run(ctx, time.Second, "snapshot"))
			f.hook = func(args []string) {
				if reflect.DeepEqual(args, command) {
					f.out["tab"] = "  [t1] App - http://localhost/\n→ [t2] Other - http://localhost/other\n"
				}
			}
			is(t, "")(e.Run(ctx, time.Second, command...))
			if e.tab != "t2" {
				t.Fatalf("selected tab = %q", e.tab)
			}
			f.calls = nil
			is(t, "")(e.Run(ctx, time.Second, "snapshot"))
			if f.find("tab")[1] != "list" || len(f.calls) != 2 {
				t.Fatalf("evidence reselected the old tab: %v", f.calls)
			}
		})
	}
}
