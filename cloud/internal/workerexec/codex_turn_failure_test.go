package workerexec

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// A failed Codex turn reports Codex's own reason, not only its status, so the
// timeline says why the turn failed and, for a lapsed sign-in, what to do.
func TestCodexTurnFailureSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		turnErr *codexTurnError
		want    []string
		signIn  bool
	}{
		{name: "no reason", want: []string{"Codex turn ended with failed"}},
		{name: "empty reason", turnErr: &codexTurnError{}, want: []string{"Codex turn ended with failed"}},
		{name: "reason and details", turnErr: &codexTurnError{Message: "model not supported", AdditionalDetails: "gpt-5.6-sol needs a newer Codex"},
			want: []string{"Codex turn failed: model not supported (gpt-5.6-sol needs a newer Codex)"}},
		{name: "unauthorized", turnErr: &codexTurnError{Message: "unexpected status 401", CodexErrorInfo: json.RawMessage(`"unauthorized"`)},
			want: []string{"Codex turn failed: unexpected status 401", "Sign in to Codex again for Cloud"}, signIn: true},
		{name: "refresh token reused", turnErr: &codexTurnError{Message: "Your access token could not be refreshed because your refresh token was already used. Please log out and sign in again."},
			want: []string{"refresh token was already used", "Sign in to Codex again for Cloud"}, signIn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := codexTurnFailure("failed", tc.turnErr).Error()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("codexTurnFailure = %q, want it to contain %q", got, want)
				}
			}
			if strings.Contains(got, "Sign in to Codex again") != tc.signIn {
				t.Fatalf("sign-in guidance = %v in %q", !tc.signIn, got)
			}
		})
	}
	long := codexTurnFailure("failed", &codexTurnError{Message: strings.Repeat("x", 2000)}).Error()
	if len(long) > codexTurnErrorMaxLen+100 {
		t.Fatalf("a long provider error was not bounded: %d characters", len(long))
	}
}

// A turn/completed without an error of its own reports the last final error
// notification Codex sent for the thread.
func TestCodexSessionFallsBackToTheLastErrorNotification(t *testing.T) {
	session := &codexSession{lastError: &codexTurnError{Message: "stream disconnected before completion"}}
	if got := session.reason(nil); got == nil || got.Message != "stream disconnected before completion" {
		t.Fatalf("reason(nil) = %+v", got)
	}
	own := &codexTurnError{Message: "context window exceeded"}
	if got := session.reason(own); got != own {
		t.Fatalf("reason(own) = %+v, want the turn's own error", got)
	}
}

// The worker checks a requested model against the models this Codex lists for
// its sign-in, across pages, by id and by model name.
func TestCodexListedModelsReadsEveryPage(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	defer requestReader.Close()
	defer requestWriter.Close()
	defer responseReader.Close()
	defer responseWriter.Close()
	conn := newCodexRPC(requestWriter, responseReader)
	go func() {
		reader := bufio.NewReader(requestReader)
		pages := []string{
			`{"data":[{"id":"gpt-6-luna","model":"gpt-6-luna","isDefault":true}],"nextCursor":"page-2"}`,
			`{"data":[{"id":"terra-preset","model":"gpt-5.6-terra"}]}`,
		}
		for index := range pages {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(line, &request) != nil || request.Method != "model/list" {
				return
			}
			_, _ = responseWriter.Write([]byte(fmt.Sprintf(`{"id":%d,"result":%s}`+"\n", request.ID, pages[index])))
		}
	}()
	listed, defaultModel, ok := codexListedModels(context.Background(), conn)
	if !ok {
		t.Fatal("model list unavailable")
	}
	if defaultModel != "gpt-6-luna" {
		t.Fatalf("default model = %q, want gpt-6-luna", defaultModel)
	}
	for _, model := range []string{"gpt-6-luna", "terra-preset", "gpt-5.6-terra"} {
		if !listed[model] {
			t.Fatalf("%s is not listed: %v", model, listed)
		}
	}
	if listed["gpt-5.6-sol"] {
		t.Fatal("an unlisted model was listed")
	}
}

// A model this Codex does not list runs on Codex's default model, for the
// thread as well as the turn, with a notice; a listed model is kept.
func TestCodexTurnModelFallsBackToTheDefault(t *testing.T) {
	serve := func(t *testing.T) *codexRPC {
		requestReader, requestWriter := io.Pipe()
		responseReader, responseWriter := io.Pipe()
		t.Cleanup(func() {
			requestReader.Close()
			requestWriter.Close()
			responseReader.Close()
			responseWriter.Close()
		})
		conn := newCodexRPC(requestWriter, responseReader)
		go func() {
			line, err := bufio.NewReader(requestReader).ReadBytes('\n')
			if err != nil {
				return
			}
			var request struct {
				ID int64 `json:"id"`
			}
			if json.Unmarshal(line, &request) != nil {
				return
			}
			_, _ = responseWriter.Write([]byte(fmt.Sprintf(`{"id":%d,"result":{"data":[{"id":"gpt-5.6-terra","model":"gpt-5.6-terra"},{"id":"gpt-6-luna","model":"gpt-6-luna","isDefault":true}]}}`+"\n", request.ID)))
		}()
		return conn
	}
	if model, notice := codexTurnModel(context.Background(), serve(t), "gpt-5.6-sol"); model != "gpt-6-luna" || !strings.Contains(notice, "gpt-5.6-sol is not available") {
		t.Fatalf("unlisted model = %q notice %q, want Codex's default with a notice", model, notice)
	}
	if model, notice := codexTurnModel(context.Background(), serve(t), "gpt-5.6-terra"); model != "gpt-5.6-terra" || notice != "" {
		t.Fatalf("listed model = %q notice %q, want it kept", model, notice)
	}
	if model, notice := codexTurnModel(context.Background(), nil, ""); model != "" || notice != "" {
		t.Fatalf("no requested model = %q notice %q", model, notice)
	}
}
