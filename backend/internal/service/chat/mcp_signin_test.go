package chat_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type mcpSigner struct {
	*fakeConversation
	page string
}

func (s *mcpSigner) SignInMCPServer(context.Context, string) (string, error) { return s.page, nil }

func TestSignInMCPServerIsRefusedForADriverWithoutIt(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.SignInMCPServer(context.Background(), testSession, "tracker"); !errors.Is(err, chatsvc.ErrMCPSignInUnsupported) {
		t.Fatalf("err = %v, want ErrMCPSignInUnsupported", err)
	}
}

func TestFinishMCPSignInRequestsOnlyTheReturnPageOfTheOpenSignIn(t *testing.T) {
	var got atomic.Value
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.URL.String())
		if r.URL.Query().Get("state") == "stale" {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(listener.Close)
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got.Store("other listener") }))
	t.Cleanup(other.Close)

	back := listener.URL + "/callback"
	signer := &mcpSigner{fakeConversation: newFakeConversation(), page: "https://mcp.example/authorize?state=s1&redirect_uri=" + url.QueryEscape(back)}
	h := newHarnessWithConversation(t, signer)
	ctx := context.Background()

	if err := h.svc.FinishMCPSignIn(ctx, testSession, "tracker", back+"?code=c1&state=s1"); !errors.Is(err, chatsvc.ErrMCPSignInAddress) {
		t.Fatalf("finish before a sign-in = %v, want ErrMCPSignInAddress", err)
	}
	started, err := h.svc.SignInMCPServer(ctx, testSession, "tracker")
	if err != nil || started.AuthorizationURL != signer.page || started.CallbackURL != back {
		t.Fatalf("SignInMCPServer = %+v, %v", started, err)
	}
	for _, address := range []string{
		other.URL + "/callback?code=c1&state=s1",
		listener.URL + "/other?code=c1&state=s1",
		back,
		"https://mcp.example/callback?code=c1",
		"not an address",
	} {
		if err := h.svc.FinishMCPSignIn(ctx, testSession, "tracker", address); !errors.Is(err, chatsvc.ErrMCPSignInAddress) {
			t.Fatalf("finish %q = %v, want ErrMCPSignInAddress", address, err)
		}
	}
	if got.Load() != nil {
		t.Fatalf("a refused address was requested: %v", got.Load())
	}
	if err := h.svc.FinishMCPSignIn(ctx, testSession, "other-server", back+"?code=c1&state=s1"); !errors.Is(err, chatsvc.ErrMCPSignInAddress) {
		t.Fatalf("finish for a server with no open sign-in = %v", err)
	}
	if err := h.svc.FinishMCPSignIn(ctx, testSession, "tracker", "  "+back+"?code=c1&state=s1 "); err != nil {
		t.Fatalf("FinishMCPSignIn: %v", err)
	}
	if got.Load() != "/callback?code=c1&state=s1" {
		t.Fatalf("listener got %v", got.Load())
	}
	if err := h.svc.FinishMCPSignIn(ctx, testSession, "tracker", back+"?code=c1&state=stale"); !errors.Is(err, chatsvc.ErrMCPSignInClosed) {
		t.Fatalf("a refusal by the listener = %v, want ErrMCPSignInClosed", err)
	}
	listener.Close()
	if err := h.svc.FinishMCPSignIn(ctx, testSession, "tracker", back+"?code=c1&state=s1"); !errors.Is(err, chatsvc.ErrMCPSignInClosed) {
		t.Fatalf("a closed listener = %v, want ErrMCPSignInClosed", err)
	}
}

func TestASignInThatReturnsOffThisMachineHasNoPastedFinish(t *testing.T) {
	signer := &mcpSigner{fakeConversation: newFakeConversation(), page: "https://mcp.example/authorize?redirect_uri=" + url.QueryEscape("https://elsewhere.example/callback")}
	h := newHarnessWithConversation(t, signer)
	started, err := h.svc.SignInMCPServer(context.Background(), testSession, "tracker")
	if err != nil || started.CallbackURL != "" {
		t.Fatalf("SignInMCPServer = %+v, %v", started, err)
	}
	if err := h.svc.FinishMCPSignIn(context.Background(), testSession, "tracker", "https://elsewhere.example/callback?code=c1"); !errors.Is(err, chatsvc.ErrMCPSignInAddress) {
		t.Fatalf("err = %v, want ErrMCPSignInAddress", err)
	}
}
