package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/computeruse"
)

type remoteComputerUseService struct {
	ComputerUse
	allowCalls []bool
	target     *computeruse.SetRemoteTargetRequest
	cleared    bool
	call       *computeruse.RemoteCallRequest
	callErr    error
}

func (s *remoteComputerUseService) RemoteSettings(context.Context) (computeruse.RemoteSettings, error) {
	return computeruse.RemoteSettings{AllowRemote: true, Target: &computeruse.RemoteTarget{HostID: "h", Label: "Mac", URL: "https://h.useao.dev"}}, nil
}

func (s *remoteComputerUseService) SetAllowRemote(_ context.Context, allow bool) (computeruse.RemoteSettings, error) {
	s.allowCalls = append(s.allowCalls, allow)
	return computeruse.RemoteSettings{AllowRemote: allow}, nil
}

func (s *remoteComputerUseService) SetRemoteTarget(_ context.Context, request computeruse.SetRemoteTargetRequest) (computeruse.RemoteSettings, error) {
	s.target = &request
	return computeruse.RemoteSettings{Target: &computeruse.RemoteTarget{HostID: request.HostID, Label: request.Label, URL: request.URL}}, nil
}

func (s *remoteComputerUseService) ClearRemoteTarget(context.Context, string) (computeruse.RemoteSettings, error) {
	s.cleared = true
	return computeruse.RemoteSettings{}, nil
}

func (s *remoteComputerUseService) RemoteOffer(context.Context) (computeruse.RemoteOffer, error) {
	return computeruse.RemoteOffer{Offered: true, Platform: "darwin"}, nil
}

func (s *remoteComputerUseService) ServeRemoteCall(_ context.Context, request computeruse.RemoteCallRequest) (computeruse.RemoteCallResult, error) {
	s.call = &request
	if s.callErr != nil {
		return computeruse.RemoteCallResult{}, s.callErr
	}
	return computeruse.RemoteCallResult{Tool: request.Tool, Result: map[string]any{}, Images: []computeruse.RemoteImage{}}, nil
}

func TestComputerUseRemoteAllowRequiresBoolean(t *testing.T) {
	svc := &remoteComputerUseService{}
	c := &ComputerUseController{Svc: svc}
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"yes"}`, `{"enabled":true,"other":1}`} {
		w := httptest.NewRecorder()
		c.setAllowRemote(w, httptest.NewRequest(http.MethodPut, "/api/v1/computer-use/remote/allow", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || len(svc.allowCalls) != 0 {
			t.Fatalf("body=%s status=%d calls=%v", body, w.Code, svc.allowCalls)
		}
	}
	w := httptest.NewRecorder()
	c.setAllowRemote(w, httptest.NewRequest(http.MethodPut, "/api/v1/computer-use/remote/allow", strings.NewReader(`{"enabled":true}`)))
	var got computeruse.RemoteSettings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || !got.AllowRemote {
		t.Fatalf("status=%d response=%s err=%v", w.Code, w.Body, err)
	}
}

// The credential goes in with the target and never comes back out.
func TestComputerUseRemoteTargetTakesTheCredentialWithoutEchoingIt(t *testing.T) {
	svc := &remoteComputerUseService{}
	c := &ComputerUseController{Svc: svc}
	w := httptest.NewRecorder()
	c.setRemoteTarget(w, httptest.NewRequest(http.MethodPut, "/api/v1/computer-use/remote/target",
		strings.NewReader(`{"hostId":"h","label":"Mac","url":"https://h.useao.dev","credential":"aod_secret"}`)))
	if w.Code != http.StatusOK || svc.target == nil || svc.target.Credential != "aod_secret" {
		t.Fatalf("status=%d target=%+v", w.Code, svc.target)
	}
	if strings.Contains(w.Body.String(), "aod_secret") {
		t.Fatalf("response echoes the credential: %s", w.Body)
	}

	w = httptest.NewRecorder()
	c.clearRemoteTarget(w, httptest.NewRequest(http.MethodDelete, "/api/v1/computer-use/remote/target", nil))
	if w.Code != http.StatusOK || !svc.cleared {
		t.Fatalf("clear status=%d cleared=%v", w.Code, svc.cleared)
	}
}

func TestRemoteComputerUseCallPassesTheServiceAnswerThrough(t *testing.T) {
	svc := &remoteComputerUseService{}
	c := &ComputerUseController{Svc: svc}

	w := httptest.NewRecorder()
	c.remoteCall(w, httptest.NewRequest(http.MethodPost, "/api/v1/remote-computer-use/call", strings.NewReader(`{"tool":"list_windows","sessionId":"ao-1","caller":"abc"}`)))
	if w.Code != http.StatusOK || svc.call == nil || svc.call.Tool != "list_windows" || svc.call.Caller != "abc" {
		t.Fatalf("status=%d call=%+v", w.Code, svc.call)
	}

	svc.callErr = apierr.Forbidden("COMPUTER_USE_REMOTE_OFF", "off")
	w = httptest.NewRecorder()
	c.remoteCall(w, httptest.NewRequest(http.MethodPost, "/api/v1/remote-computer-use/call", strings.NewReader(`{"tool":"list_windows"}`)))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "COMPUTER_USE_REMOTE_OFF") {
		t.Fatalf("refused call: status=%d body=%s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	c.remoteCall(w, httptest.NewRequest(http.MethodPost, "/api/v1/remote-computer-use/call", strings.NewReader(`not json`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad body: status=%d", w.Code)
	}

	w = httptest.NewRecorder()
	c.remoteOffer(w, httptest.NewRequest(http.MethodGet, "/api/v1/remote-computer-use/status", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"offered":true`) {
		t.Fatalf("offer: status=%d body=%s", w.Code, w.Body)
	}
}
