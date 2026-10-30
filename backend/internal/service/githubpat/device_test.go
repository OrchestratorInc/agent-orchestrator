package githubpat

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// deviceFlowClient fakes GitHub's device-flow endpoints. Each token poll
// returns the next queued response.
func deviceFlowClient(t *testing.T, polls ...string) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/login/device/code":
			if err := request.ParseForm(); err != nil || request.PostForm.Get("client_id") != "test-client" {
				t.Errorf("device code request form = %v, err %v", request.PostForm, err)
			}
			return jsonResp(http.StatusOK, `{"device_code":"dev-1","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`), nil
		case "/login/oauth/access_token":
			if len(polls) == 0 {
				t.Error("unexpected extra token poll")
				return jsonResp(http.StatusOK, `{"error":"authorization_pending"}`), nil
			}
			body := polls[0]
			polls = polls[1:]
			return jsonResp(http.StatusOK, body), nil
		case "/user":
			return jsonResp(http.StatusOK, `{"login":"octocat"}`), nil
		}
		t.Errorf("unexpected request %s", request.URL)
		return jsonResp(http.StatusNotFound, `{}`), nil
	})}
}

func newDeviceTestService(t *testing.T, client *http.Client) *Service {
	t.Helper()
	service := newWithClient(t.TempDir(), client, "https://api.github.example")
	service.oauthURL = "https://github.example"
	service.clientID = "test-client"
	return service
}

func waitForDeviceLogin(t *testing.T, service *Service, want DeviceLoginState) DeviceLogin {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if login := service.DeviceLoginStatus(); login.State == want {
			return login
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("device login state = %q, want %q", service.DeviceLoginStatus().State, want)
	return DeviceLogin{}
}

func TestDeviceLoginStoresTokenOnceApproved(t *testing.T) {
	service := newDeviceTestService(t, deviceFlowClient(t,
		`{"error":"authorization_pending"}`,
		`{"access_token":"gho_device","token_type":"bearer","scope":"repo,read:org,workflow"}`,
	))

	started, err := service.StartDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}
	if started.State != DeviceLoginPending || started.UserCode != "ABCD-1234" || started.VerificationURI != "https://github.com/login/device" {
		t.Fatalf("started = %+v", started)
	}
	again, err := service.StartDeviceLogin(context.Background())
	if err != nil || again.UserCode != started.UserCode {
		t.Fatalf("second start = %+v, %v; want the pending login reused", again, err)
	}

	done := waitForDeviceLogin(t, service, DeviceLoginSucceeded)
	if done.Login != "octocat" || done.UserCode != "" {
		t.Fatalf("finished = %+v", done)
	}
	token, err := service.GetToken(context.Background())
	if err != nil || token != "gho_device" {
		t.Fatalf("stored token = %q, %v", token, err)
	}
}

func TestDeviceLoginResetsWhenDeclinedOnGitHub(t *testing.T) {
	service := newDeviceTestService(t, deviceFlowClient(t, `{"error":"access_denied"}`))
	if _, err := service.StartDeviceLogin(context.Background()); err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}

	idle := waitForDeviceLogin(t, service, DeviceLoginIdle)
	if idle.Error != "" {
		t.Fatalf("error = %q, want none", idle.Error)
	}
	if service.HasPAT(context.Background()) {
		t.Fatal("a denied sign-in stored a token")
	}
}

func TestDeviceLoginNeedsClientID(t *testing.T) {
	service := newWithClient(t.TempDir(), deviceFlowClient(t), "https://api.github.example")
	if _, err := service.StartDeviceLogin(context.Background()); !errors.Is(err, ErrDeviceLoginUnavailable) {
		t.Fatalf("err = %v, want ErrDeviceLoginUnavailable", err)
	}
}
