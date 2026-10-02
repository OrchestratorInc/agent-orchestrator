package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestControlHandlerRequiresControlKey(t *testing.T) {
	t.Parallel()

	lease := NewLease(45 * time.Second)
	handler := NewControlHandler(ControlIdentity{
		InstanceID:    "instance-1",
		RunnerVersion: "runner-test",
		EngineVersion: "v7.3.8",
	}, "control-secret", lease)

	for _, path := range []string{"/ao/internal/identity", "/ao/internal/lease"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if path == "/ao/internal/lease" {
			req = httptest.NewRequest(http.MethodPost, path, nil)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("%s without key status = %d, want %d", path, res.Code, http.StatusUnauthorized)
		}
	}
}

func TestControlIdentityIsSafeAndLeaseRenews(t *testing.T) {
	t.Parallel()

	lease := NewLease(45 * time.Second)
	before := lease.Deadline()
	handler := NewControlHandler(ControlIdentity{
		InstanceID:    "instance-1",
		RunnerVersion: "runner-test",
		EngineVersion: "v7.3.8",
	}, "control-secret", lease)

	identityReq := httptest.NewRequest(http.MethodGet, "/ao/internal/identity", nil)
	identityReq.Header.Set("Authorization", "Bearer control-secret")
	identityRes := httptest.NewRecorder()
	handler.ServeHTTP(identityRes, identityReq)
	if identityRes.Code != http.StatusOK {
		t.Fatalf("identity status = %d, want 200", identityRes.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(identityRes.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["credentialProtocol"] != float64(credentialProtocolVersion) {
		t.Fatal("credential protocol version was not advertised")
	}
	for _, forbidden := range []string{"port", "pid", "token", "path", "controlKey"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("identity exposed forbidden field %q", forbidden)
		}
	}

	time.Sleep(time.Millisecond)
	leaseReq := httptest.NewRequest(http.MethodPost, "/ao/internal/lease", nil)
	leaseReq.Header.Set("Authorization", "Bearer control-secret")
	leaseRes := httptest.NewRecorder()
	handler.ServeHTTP(leaseRes, leaseReq)
	if leaseRes.Code != http.StatusNoContent {
		t.Fatalf("lease status = %d, want 204", leaseRes.Code)
	}
	if !lease.Deadline().After(before) {
		t.Fatalf("lease deadline did not advance")
	}
}

func TestLeaseExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	lease := NewLeaseAt(45*time.Second, now)
	if lease.Expired(now.Add(44 * time.Second)) {
		t.Fatal("lease expired before its deadline")
	}
	if !lease.Expired(now.Add(45 * time.Second)) {
		t.Fatal("lease did not expire at its deadline")
	}
	lease.Renew(now.Add(30 * time.Second))
	if lease.Expired(now.Add(60 * time.Second)) {
		t.Fatal("renewed lease expired too early")
	}
}
