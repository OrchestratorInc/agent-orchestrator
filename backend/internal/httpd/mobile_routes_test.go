package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type fakeMobileBridge struct{}

func (fakeMobileBridge) Status() controllers.MobileStatusResponse {
	return controllers.MobileStatusResponse{}
}

func (fakeMobileBridge) Enable() (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func (fakeMobileBridge) EnableLANOnly() (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func (fakeMobileBridge) EnableTunnelOnly() (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func (fakeMobileBridge) Disable() error { return nil }

func (fakeMobileBridge) Regenerate() (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func (fakeMobileBridge) StartRemoteAccess() (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func (fakeMobileBridge) SetSecurePairing(on bool) (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func (fakeMobileBridge) SetKeepAwake(on bool) (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func (fakeMobileBridge) SetBrowserRemoteAccess(on bool) (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func (fakeMobileBridge) SetKeepAwakeLid(bool, bool) (controllers.MobileStatusResponse, error) {
	return controllers.MobileStatusResponse{}, nil
}

func newTestRouterWithMobile(t *testing.T) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	mountMobile(r, &controllers.MobileController{Bridge: fakeMobileBridge{}})
	return r
}

func TestMobileStatusRouteServedOnLoopbackRouter(t *testing.T) {
	r := newTestRouterWithMobile(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mobile/status", nil)
	req.Host = "192.168.1.9:3011"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mobile status on loopback router: got %d want 200", w.Code)
	}
}
