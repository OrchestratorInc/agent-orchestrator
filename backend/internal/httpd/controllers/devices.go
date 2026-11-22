package controllers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/devices"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DeviceService is the device surface the controller drives.
type DeviceService interface {
	Status(ctx context.Context) devices.Status
	Setup(ctx context.Context) (devices.Status, error)
	Open(ctx context.Context, id domain.SessionID, req devices.OpenRequest) (devices.Lease, error)
	Lease(id domain.SessionID) (devices.Lease, bool)
	LiveLease(ctx context.Context, id domain.SessionID) (devices.Lease, bool)
	Close(ctx context.Context, id domain.SessionID, shutdown bool) error
	Install(ctx context.Context, id domain.SessionID, artifactPath string) (devices.Lease, error)
	Launch(ctx context.Context, id domain.SessionID, appID string) (devices.Lease, error)
	Screenshot(ctx context.Context, id domain.SessionID) ([]byte, error)
	PressButton(ctx context.Context, id domain.SessionID, button string) error
	SetQuality(ctx context.Context, id domain.SessionID, quality ports.DeviceStreamQuality) error
	HubTarget(ctx context.Context, id domain.SessionID) (ports.DeviceHubEndpoint, devices.Lease, context.Context, error)
}

// DeviceSessionReader reads the session a device route names.
type DeviceSessionReader interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

// DevicesController exposes simulators and emulators to sessions, the
// desktop Device tab, and the phone.
type DevicesController struct {
	Svc          DeviceService
	Sessions     DeviceSessionReader
	Capabilities SessionCapabilityValidator
}

// Register adds the device routes.
func (c *DevicesController) Register(r chi.Router) {
	r.Get("/devices", c.status)
	r.Post("/devices/setup", c.setup)
	r.Get("/sessions/{sessionId}/device", c.get)
	r.Post("/sessions/{sessionId}/device/open", c.open)
	r.Post("/sessions/{sessionId}/device/install", c.install)
	r.Post("/sessions/{sessionId}/device/launch", c.launch)
	r.Post("/sessions/{sessionId}/device/close", c.closeDevice)
	r.Post("/sessions/{sessionId}/device/button", c.pressButton)
	r.Post("/sessions/{sessionId}/device/quality", c.setQuality)
	r.Get("/sessions/{sessionId}/device/screenshot", c.screenshot)
	r.Get("/sessions/{sessionId}/device/hub/*", c.hub)
}

func (c *DevicesController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/devices")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, deviceStatusView(c.Svc.Status(r.Context())))
}

func (c *DevicesController) setup(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/devices/setup")
		return
	}
	status, err := c.Svc.Setup(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, deviceStatusView(status))
}

// session loads the routed session. Mutations also need a live session and,
// when the caller presents one, a valid capability: an agent can only drive
// its own session's device.
func (c *DevicesController) session(w http.ResponseWriter, r *http.Request, mutate bool) (domain.SessionID, bool) {
	id := sessionID(r)
	if c.Sessions == nil {
		return id, true
	}
	sess, err := c.Sessions.Get(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return id, false
	}
	if !mutate {
		return id, true
	}
	if sess.IsTerminated {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_TERMINATED", "Session is terminated", nil)
		return id, false
	}
	if capability := strings.TrimSpace(r.Header.Get(browserCapabilityHeader)); capability != "" && c.Capabilities != nil &&
		!c.Capabilities.Valid(id, capability, sess.Metadata.BrowserCapabilityVerifier) {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "DEVICE_CAPABILITY_INVALID", "The capability does not belong to this session", nil)
		return id, false
	}
	return id, true
}

func (c *DevicesController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/device")
		return
	}
	id, ok := c.session(w, r, false)
	if !ok {
		return
	}
	resp := SessionDeviceResponse{}
	if lease, has := c.Svc.LiveLease(r.Context(), id); has {
		view := deviceLeaseView(lease)
		resp.Lease = &view
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *DevicesController) open(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/device/open")
		return
	}
	id, ok := c.session(w, r, true)
	if !ok {
		return
	}
	var in OpenSessionDeviceRequest
	if !decodeOptionalJSON(w, r, &in) {
		return
	}
	if in.Platform != "" && in.Platform != string(ports.DevicePlatformIOS) && in.Platform != string(ports.DevicePlatformAndroid) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PLATFORM_INVALID", "platform must be ios or android", nil)
		return
	}
	lease, err := c.Svc.Open(r.Context(), id, devices.OpenRequest{Platform: ports.DevicePlatform(in.Platform), DeviceID: strings.TrimSpace(in.DeviceID)})
	writeLease(w, r, lease, err)
}

func (c *DevicesController) install(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/device/install")
		return
	}
	id, ok := c.session(w, r, true)
	if !ok {
		return
	}
	var in InstallSessionDeviceRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	lease, err := c.Svc.Install(r.Context(), id, in.Path)
	writeLease(w, r, lease, err)
}

func (c *DevicesController) launch(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/device/launch")
		return
	}
	id, ok := c.session(w, r, true)
	if !ok {
		return
	}
	var in LaunchSessionDeviceRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	lease, err := c.Svc.Launch(r.Context(), id, in.AppID)
	writeLease(w, r, lease, err)
}

func (c *DevicesController) closeDevice(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/device/close")
		return
	}
	id, ok := c.session(w, r, true)
	if !ok {
		return
	}
	var in CloseSessionDeviceRequest
	if !decodeOptionalJSON(w, r, &in) {
		return
	}
	if err := c.Svc.Close(r.Context(), id, in.Shutdown); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionDeviceResponse{})
}

func (c *DevicesController) pressButton(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/device/button")
		return
	}
	id, ok := c.session(w, r, true)
	if !ok {
		return
	}
	var in PressSessionDeviceButtonRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.Svc.PressButton(r.Context(), id, in.Button); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	lease, _ := c.Svc.Lease(id)
	writeLease(w, r, lease, nil)
}

func (c *DevicesController) setQuality(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/device/quality")
		return
	}
	id, ok := c.session(w, r, true)
	if !ok {
		return
	}
	var in SetSessionDeviceQualityRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.Svc.SetQuality(r.Context(), id, ports.DeviceStreamQuality(in.Quality)); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	lease, _ := c.Svc.Lease(id)
	writeLease(w, r, lease, nil)
}

func (c *DevicesController) screenshot(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/device/screenshot")
		return
	}
	id, ok := c.session(w, r, false)
	if !ok {
		return
	}
	png, err := c.Svc.Screenshot(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(png) //nolint:gosec // G705: device pixels served as image/png with nosniff, never as a document

}

// hub streams the leased device from the provider's hub. Only the stream,
// its input socket and screenshots for the session's own device pass; the
// hub's token is added here and never reaches the client.
func (c *DevicesController) hub(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "DEVICE_HUB_NOT_FOUND", "Device support is not available", nil)
		return
	}
	id, ok := c.session(w, r, false)
	if !ok {
		return
	}
	endpoint, lease, leaseCtx, err := c.Svc.HubTarget(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// The connection lives only as long as the lease: releasing the device, or
	// another session taking it, closes the stream and the touch socket.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(leaseCtx, cancel)
	defer stop()
	r = r.WithContext(ctx)
	tail, _, err := wildcardPath(r)
	if err != nil || !devices.HubRouteAllowed(lease.Device, tail, r.URL.Query().Get("device")) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "DEVICE_HUB_NOT_FOUND", "Not a device stream route", nil)
		return
	}
	target, err := url.Parse(endpoint.Origin)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if wantsPacedInput(r, tail) {
		pacedInput(w, r, target, tail, endpoint.Headers)
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport:     previewTransport,
		FlushInterval: -1,
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			req.Out.Host = target.Host
			req.Out.URL.Path = "/" + tail
			req.Out.URL.RawPath = ""
			// repeat and pace are for this proxy, not the hub.
			forwarded := req.In.URL.Query()
			forwarded.Del("repeat")
			forwarded.Del("pace")
			req.Out.URL.RawQuery = forwarded.Encode()
			for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Origin", browserCapabilityHeader} {
				req.Out.Header.Del(name)
			}
			for name, value := range endpoint.Headers {
				req.Out.Header.Set(name, value)
			}
		},
		ModifyResponse: func(res *http.Response) error {
			// A still screen never paints in an <img> viewer until the next part
			// starts: send the last frame again once the stream goes quiet. A
			// native viewer paints every part it gets, so it asks for none
			// (repeat=0): the repeats were duplicate full-size frames.
			if strings.HasSuffix(tail, "/stream.mjpeg") && res.StatusCode == http.StatusOK && r.URL.Query().Get("repeat") != "0" {
				res.Body = devices.StillFrameStream(res.Body, 0)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Device stream unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// decodeOptionalJSON accepts an empty body as the zero value.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err == nil && len(bytes.TrimSpace(body)) > 0 {
		err = decodeJSON(&http.Request{Body: io.NopCloser(bytes.NewReader(body))}, out)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return false
	}
	return true
}

func writeLease(w http.ResponseWriter, r *http.Request, lease devices.Lease, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	view := deviceLeaseView(lease)
	envelope.WriteJSON(w, http.StatusOK, SessionDeviceResponse{Lease: &view})
}

func deviceStatusView(s devices.Status) DeviceStatusResponse {
	out := DeviceStatusResponse{
		Enabled:   s.Enabled,
		State:     string(s.State),
		Error:     s.Error,
		Platforms: make([]DevicePlatformStatus, 0, len(s.Platforms)),
		Devices:   make([]DeviceSummary, 0, len(s.Devices)),
		Leases:    make([]DeviceLease, 0, len(s.Leases)),
	}
	for _, p := range s.Platforms {
		out.Platforms = append(out.Platforms, DevicePlatformStatus{Platform: string(p.Platform), Available: p.Available, Reason: p.Reason})
	}
	for _, d := range s.Devices {
		out.Devices = append(out.Devices, deviceSummaryView(d))
	}
	for _, l := range s.Leases {
		out.Leases = append(out.Leases, deviceLeaseView(l))
	}
	return out
}

func deviceSummaryView(d ports.DeviceInfo) DeviceSummary {
	return DeviceSummary{ID: d.ID, Name: d.Name, Platform: string(d.Platform), Version: d.Version, Booted: d.Booted}
}

func deviceLeaseView(l devices.Lease) DeviceLease {
	return DeviceLease{
		SessionID:   l.SessionID,
		Provider:    l.Provider,
		Device:      deviceSummaryView(l.Device),
		Reason:      l.Reason,
		AgentDevice: DeviceAgentControl{Command: l.AgentControl.Command, Args: append([]string{}, l.AgentControl.Args...)},
		OpenedAt:    l.OpenedAt,
	}
}
