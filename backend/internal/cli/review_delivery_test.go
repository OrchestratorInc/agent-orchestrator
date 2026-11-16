package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestReviewTriggerSendsHardNoInjectPolicy(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, capture := reviewServer(t, http.StatusCreated, `{"created":true}`)
	writeRunFileFor(t, cfg, srv)
	_, stderr, err := executeCLI(t, aliveDeps(), "review", "trigger", "mer-1", "--no-inject")
	if err != nil {
		t.Fatalf("trigger: %v; %s", err, stderr)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(capture.body), &body); err != nil {
		t.Fatal(err)
	}
	if body["noInject"] != true || body["enableAutoInject"] == true {
		t.Fatalf("request = %s, want noInject=true without enabling delivery", capture.body)
	}
}

func TestSessionGetExposesReviewDeliverySetting(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, _ := reviewServer(t, http.StatusOK, `{"session":{"id":"mer-1","autoInjectReview":true}}`)
	writeRunFileFor(t, cfg, srv)
	out, stderr, err := executeCLI(t, aliveDeps(), "session", "get", "mer-1", "--json")
	if err != nil {
		t.Fatalf("get: %v; %s", err, stderr)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatal(err)
	}
	session, _ := body["session"].(map[string]any)
	if session["autoInjectReview"] != true {
		t.Fatalf("session = %s, want autoInjectReview=true", out)
	}
}

func TestReviewAutoInjectOffUsesSessionPolicyRoute(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)
	_, stderr, err := executeCLI(t, aliveDeps(), "review", "auto-inject", "mer-1", "--off")
	if err != nil {
		t.Fatalf("disable: %v; %s", err, stderr)
	}
	if capture.method != http.MethodPatch || capture.path != "/api/v1/sessions/mer-1/auto-inject-review" || capture.body != `{"autoInjectReview":false}` {
		t.Fatalf("request = %s %s %s", capture.method, capture.path, capture.body)
	}
}

func TestReviewAutoInjectValidationAndErrors(t *testing.T) {
	for _, args := range [][]string{{"review", "auto-inject"}, {"review", "auto-inject", "mer-1"}, {"review", "auto-inject", "mer-1", "--on", "--off"}, {"review", "auto-inject", "--off"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			setReviewEnv(t)
			_, _, err := executeCLI(t, aliveDeps(), args...)
			if ExitCode(err) != 2 {
				t.Fatalf("err = %v, want usage error", err)
			}
		})
	}
	t.Run("daemon error", func(t *testing.T) {
		cfg := setReviewEnv(t)
		srv, _ := reviewServer(t, http.StatusNotFound, `{"code":"SESSION_NOT_FOUND","message":"Unknown session","requestId":"request-7"}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, aliveDeps(), "review", "auto-inject", "missing", "--off")
		if err == nil || !strings.Contains(err.Error(), "SESSION_NOT_FOUND") || !strings.Contains(err.Error(), "request-7") {
			t.Fatalf("err = %v, want daemon error details", err)
		}
	})
	t.Run("enable calling session", func(t *testing.T) {
		cfg := setReviewEnv(t)
		t.Setenv("AO_SESSION_ID", "mer-1")
		srv, capture := reviewServer(t, http.StatusOK, `{}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, aliveDeps(), "review", "auto-inject", "--on")
		if err != nil {
			t.Fatal(err)
		}
		if capture.path != "/api/v1/sessions/mer-1/auto-inject-review" || capture.body != `{"autoInjectReview":true}` {
			t.Fatalf("request = %s %s", capture.path, capture.body)
		}
	})
}
