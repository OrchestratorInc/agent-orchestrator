package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/hostupdate"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

type fakeHostUpdater struct {
	status   hostupdate.Status
	startErr error
	started  bool
	version  string
	checked  bool
	next     string
	checkErr error
}

func (f *fakeHostUpdater) Status(context.Context) hostupdate.Status { return f.status }
func (f *fakeHostUpdater) Check(context.Context) (string, error) {
	f.checked = true
	return f.next, f.checkErr
}
func (f *fakeHostUpdater) Start(_ context.Context, version string) error {
	f.started, f.version = true, version
	if f.startErr == nil {
		f.status.State = hostupdate.StateUpdating
	}
	return f.startErr
}

func hostUpdateServer(t *testing.T, updater *fakeHostUpdater) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{HostUpdate: updater}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoteHostUpdateStatusAndStart(t *testing.T) {
	updater := &fakeHostUpdater{status: hostupdate.Status{Version: "0.15.0", Supported: true, Method: hostupdate.MethodApp, State: hostupdate.StateIdle}}
	srv := hostUpdateServer(t, updater)

	res, err := http.Get(srv.URL + "/api/v1/remote-host/update")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Version   string `json:"version"`
		Supported bool   `json:"supported"`
		Method    string `json:"method"`
		State     string `json:"state"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || body.Version != "0.15.0" || !body.Supported || body.Method != "app" || body.State != "idle" {
		t.Fatalf("GET = %d %+v", res.StatusCode, body)
	}

	res, err = http.Post(srv.URL+"/api/v1/remote-host/update", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted || !updater.started || body.State != "updating" {
		t.Fatalf("POST = %d %+v", res.StatusCode, body)
	}
}

func TestRemoteHostUpdateCheckAndConfirmedVersion(t *testing.T) {
	updater := &fakeHostUpdater{status: hostupdate.Status{Version: "0.15.0", Supported: true, Method: hostupdate.MethodApp, State: hostupdate.StateIdle}, next: "0.16.0"}
	srv := hostUpdateServer(t, updater)

	res, err := http.Post(srv.URL+"/api/v1/remote-host/update/check", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var checked struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(res.Body).Decode(&checked)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || checked.Version != "0.16.0" || updater.started {
		t.Fatalf("check = %d %+v, started %v", res.StatusCode, checked, updater.started)
	}

	res, err = http.Post(srv.URL+"/api/v1/remote-host/update", "application/json", strings.NewReader(`{"version":"0.16.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted || updater.version != "0.16.0" {
		t.Fatalf("start = %d, version %q", res.StatusCode, updater.version)
	}

	res, err = http.Post(srv.URL+"/api/v1/remote-host/update", "application/json", strings.NewReader(`{"version":`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad body = %d", res.StatusCode)
	}

	for checkErr, want := range map[error]string{
		errors.New("the app found no newer version"): "HOST_UPDATE_CHECK_FAILED",
		hostupdate.ErrNoCheck:                        "HOST_UPDATE_NO_CHECK",
		hostupdate.ErrInProgress:                     "HOST_UPDATE_IN_PROGRESS",
	} {
		updater.checkErr = checkErr
		res, err = http.Post(srv.URL+"/api/v1/remote-host/update/check", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		var refused struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(res.Body).Decode(&refused)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusConflict || refused.Code != want || refused.Message != checkErr.Error() {
			t.Errorf("%v: got %d %+v, want 409 %s", checkErr, res.StatusCode, refused, want)
		}
	}
}

func TestRemoteHostUpdateRefusals(t *testing.T) {
	for err, code := range map[error]string{hostupdate.ErrUnsupported: "HOST_UPDATE_UNSUPPORTED", hostupdate.ErrInProgress: "HOST_UPDATE_IN_PROGRESS"} {
		srv := hostUpdateServer(t, &fakeHostUpdater{startErr: err})
		res, reqErr := http.Post(srv.URL+"/api/v1/remote-host/update", "", nil)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		var body struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusConflict || body.Code != code {
			t.Errorf("%v: got %d %q, want 409 %s", err, res.StatusCode, body.Code, code)
		}
	}
}

func TestRemoteHostUpdateRefusesABrowserOrigin(t *testing.T) {
	updater := &fakeHostUpdater{status: hostupdate.Status{Supported: true, State: hostupdate.StateIdle}}
	srv := hostUpdateServer(t, updater)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/remote-host/update", nil)
	req.Header.Set("Origin", "https://attacker.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden || updater.started {
		t.Fatalf("cross-origin POST = %d, started %v", res.StatusCode, updater.started)
	}
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/remote-host/update/check", nil)
	req.Header.Set("Origin", "https://attacker.example")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden || updater.checked {
		t.Fatalf("cross-origin check = %d, checked %v", res.StatusCode, updater.checked)
	}
}
