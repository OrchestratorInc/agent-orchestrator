package hostupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeApp struct {
	mu      sync.Mutex
	calls   []map[string]interface{}
	prepare func() (browserruntime.Result, error)
	install func() (browserruntime.Result, error)
}

func (f *fakeApp) Execute(_ context.Context, session domain.SessionID, action string, args map[string]interface{}) (browserruntime.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	f.mu.Unlock()
	if session == "" || action != appAction {
		return browserruntime.Result{}, errors.New("bad command")
	}
	if args["step"] == "install" {
		return f.install()
	}
	return f.prepare()
}

func (f *fakeApp) steps() []map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]interface{}(nil), f.calls...)
}

func appUpdater(t *testing.T, app *fakeApp, wait time.Duration) *Updater {
	t.Helper()
	return &Updater{version: "0.15.0", kind: kindApp, app: app, resultPath: filepath.Join(t.TempDir(), resultFile), restartWait: wait}
}

func ready(version string) func() (browserruntime.Result, error) {
	return func() (browserruntime.Result, error) {
		return browserruntime.Result{Value: map[string]interface{}{"version": version}}, nil
	}
}

func eventually(t *testing.T, u *Updater, want State) Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := u.Status(context.Background())
		if got.State == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %+v, want %s", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAppUpdateInstallsAndWaitsForRestart(t *testing.T) {
	installed := make(chan struct{})
	app := &fakeApp{prepare: ready("0.16.0"), install: func() (browserruntime.Result, error) {
		close(installed)
		return browserruntime.Result{}, browserruntime.ErrUnavailable
	}}
	u := appUpdater(t, app, time.Hour)
	if got := u.Status(context.Background()); !got.Supported || got.Method != MethodApp || got.State != StateIdle {
		t.Fatalf("status = %+v", got)
	}
	if err := u.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-installed
	if err := u.Start(context.Background()); !errors.Is(err, ErrInProgress) {
		t.Fatalf("second start = %v", err)
	}
	if got := u.Status(context.Background()); got.State != StateUpdating {
		t.Fatalf("status = %+v", got)
	}
	data, err := os.ReadFile(u.resultPath)
	if err != nil || string(data) != `{"from":"0.15.0","to":"0.16.0"}` {
		t.Fatalf("result = %q, %v", data, err)
	}
	if steps := app.steps(); len(steps) != 2 || steps[1]["version"] != "0.16.0" {
		t.Fatalf("steps = %v", steps)
	}
}

func TestAppUpdateReportsTheAppsRefusal(t *testing.T) {
	cases := map[string]error{
		"Orchestrator on that computer has a draft that isn't saved yet. Update it there.": browserruntime.CommandError{Code: "BROWSER_COMMAND_FAILED", Message: "Orchestrator on that computer has a draft that isn't saved yet. Update it there."},
		"Orchestrator's window is closed on that computer. Open it there, then try again.": browserruntime.ErrUnavailable,
	}
	for want, refusal := range cases {
		app := &fakeApp{prepare: func() (browserruntime.Result, error) { return browserruntime.Result{}, refusal }}
		u := appUpdater(t, app, time.Hour)
		if err := u.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := eventually(t, u, StateFailed); got.Error != want {
			t.Errorf("error = %q, want %q", got.Error, want)
		}
	}
}

func TestAppUpdateInstallNotSentClearsResult(t *testing.T) {
	cases := map[string]error{
		"Orchestrator on that computer has a draft that isn't saved yet. Update it there.": browserruntime.CommandError{Code: "BROWSER_COMMAND_FAILED", Message: "Orchestrator on that computer has a draft that isn't saved yet. Update it there."},
		"Orchestrator's window is closed on that computer. Open it there, then try again.": fmt.Errorf("%w: %w", browserruntime.ErrReconnecting, browserruntime.ErrUnavailable),
	}
	for want, refusal := range cases {
		app := &fakeApp{prepare: ready("0.16.0"), install: func() (browserruntime.Result, error) { return browserruntime.Result{}, refusal }}
		u := appUpdater(t, app, time.Hour)
		if err := u.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := eventually(t, u, StateFailed); got.Error != want {
			t.Errorf("error = %q, want %q", got.Error, want)
		}
		if _, err := os.Stat(u.resultPath); !os.IsNotExist(err) {
			t.Fatalf("result left behind: %v", err)
		}
	}
}

func TestAppUpdateKeepsResultWhenTheAppQuitsMidInstall(t *testing.T) {
	installed := make(chan struct{})
	app := &fakeApp{prepare: ready("0.16.0"), install: func() (browserruntime.Result, error) {
		close(installed)
		return browserruntime.Result{}, browserruntime.CommandError{Code: "BROWSER_COMMAND_CANCELED", Message: "Browser runtime link closed"}
	}}
	u := appUpdater(t, app, time.Hour)
	if err := u.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-installed
	time.Sleep(20 * time.Millisecond)
	if got := u.Status(context.Background()); got.State != StateUpdating {
		t.Fatalf("status = %+v", got)
	}
	if _, err := os.Stat(u.resultPath); err != nil {
		t.Fatalf("result removed: %v", err)
	}
}

func TestAppUpdateFailsWhenTheAppDoesNotRestart(t *testing.T) {
	app := &fakeApp{prepare: ready("0.16.0"), install: ready("0.16.0")}
	u := appUpdater(t, app, 0)
	if err := u.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := eventually(t, u, StateFailed); got.Error != "Orchestrator on that computer didn't restart to install 0.16.0." {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestAppUpdateResultAfterRestart(t *testing.T) {
	stuck := appUpdater(t, &fakeApp{}, time.Hour)
	if err := os.WriteFile(stuck.resultPath, []byte(`{"from":"0.15.0","to":"0.16.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stuck.loadAppResult()
	if got := stuck.Status(context.Background()); got.State != StateFailed || got.Error != "The update to 0.16.0 didn't apply on that computer. Update Orchestrator there." {
		t.Fatalf("status = %+v", got)
	}
	app := &fakeApp{prepare: func() (browserruntime.Result, error) {
		return browserruntime.Result{}, browserruntime.CommandError{Message: "The update to 0.16.0 didn't apply on that computer. Update Orchestrator there."}
	}}
	stuck.app = app
	if err := stuck.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, stuck, StateFailed)
	if steps := app.steps(); len(steps) != 1 || steps[0]["notApplied"] != "0.16.0" {
		t.Fatalf("steps = %v", steps)
	}

	updated := appUpdater(t, &fakeApp{}, time.Hour)
	updated.version = "0.16.0"
	if err := os.WriteFile(updated.resultPath, []byte(`{"from":"0.15.0","to":"0.16.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	updated.loadAppResult()
	if got := updated.Status(context.Background()); got.State != StateIdle {
		t.Fatalf("status = %+v", got)
	}
	if _, err := os.Stat(updated.resultPath); !os.IsNotExist(err) {
		t.Fatalf("result left behind: %v", err)
	}
}
