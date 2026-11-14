package apispec_test

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	yaml "gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type stubDeviceRoster struct{}

func (stubDeviceRoster) List() []mobilebridge.PushDevice { return nil }
func (stubDeviceRoster) Delete(string) error             { return nil }

type stubDeviceLive struct{}

func (stubDeviceLive) Live() map[string]bool { return nil }

func (stubDeviceLive) LastSeen(string) (time.Time, bool) { return time.Time{}, false }

func TestRouteSpecParity(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{
		Mobile:       &controllers.MobileController{},
		DeviceRoster: stubDeviceRoster{},
		DeviceLive:   stubDeviceLive{},
	}
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{})

	mounted := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/v1/") && route != "/api/v1/openapi.yaml" {
			mounted[strings.ToUpper(method)+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(mounted) == 0 {
		t.Fatal("no /api/v1 routes mounted — router wiring changed?")
	}

	for r := range mounted {
		mp := strings.SplitN(r, " ", 2)
		if apispec.Default().Operation(mp[0], mp[1]) == nil {
			t.Errorf("mounted route %s has no OpenAPI operation", r)
		}
	}

	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.Default().YAML(), &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	httpMethods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	for path, item := range doc.Paths {
		for method := range item {
			if !httpMethods[method] {
				continue
			}
			key := strings.ToUpper(method) + " " + path
			if !mounted[key] {
				t.Errorf("spec operation %s has no mounted route", key)
			}
		}
	}
}
