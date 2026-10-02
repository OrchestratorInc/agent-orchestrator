package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type accountControlAliasCase struct {
	name string
	path string
	body string
}

func accountControlAliasCases(session string, revision, impactRevision int64) []accountControlAliasCase {
	var cases []accountControlAliasCase
	for _, spelling := range []struct{ name, s string }{{"literal", "ſ"}, {"escaped", `\u017f`}} {
		for _, reverse := range []bool{false, true} {
			pair := func(canonical, alias string) string {
				if reverse {
					return alias + "," + canonical
				}
				return canonical + "," + alias
			}
			suffix := fmt.Sprintf("%s/reverse=%t", spelling.name, reverse)
			switchPath := "/sessions/" + session + "/account-switches"
			cases = append(cases, accountControlAliasCase{
				name: "switch-revision/" + suffix, path: switchPath,
				body: `{"operationId":"switch-a",` + pair(fmt.Sprintf(`"expectedRevision":%d`, revision), fmt.Sprintf(`"expectedRevi%sion":%d`, spelling.s, revision+1)) + `,"mode":"managed","accountId":"amc_b","policy":"interrupt"}`,
			}, accountControlAliasCase{
				name: "removal-revision/" + suffix, path: "/accounts-manager/accounts/amc_a/removals",
				body: `{"operationId":"removal-a",` + pair(fmt.Sprintf(`"expectedRevision":%d`, impactRevision), fmt.Sprintf(`"expectedRevi%sion":%d`, spelling.s, impactRevision+1)) + `,"confirmed":true}`,
			})
			for _, canonical := range []bool{false, true} {
				cases = append(cases, accountControlAliasCase{
					name: fmt.Sprintf("history-choice/%s/canonical=%t", suffix, canonical), path: switchPath,
					body: fmt.Sprintf(`{"operationId":"switch-a","expectedRevision":%d,"mode":"managed","accountId":"amc_b","policy":"interrupt",`, revision) + pair(fmt.Sprintf(`"newConversation":%t`, canonical), fmt.Sprintf(`"newConver%sation":%t`, spelling.s, !canonical)) + `}`,
				})
			}
		}
	}
	return cases
}

func assertAccountControlError(t *testing.T, res *httptest.ResponseRecorder, status int) {
	t.Helper()
	var body struct {
		RequestID string `json:"requestId"`
	}
	if res.Code != status || json.Unmarshal(res.Body.Bytes(), &body) != nil || body.RequestID != "control-test-request" {
		t.Errorf("expected correlated %d, got %d %s", status, res.Code, res.Body.String())
	}
}

func TestAccountControlUnicodeAliasesBeforeAdmission(t *testing.T) {
	for _, test := range accountControlAliasCases("session-a", 7, 9) {
		t.Run(test.name, func(t *testing.T) {
			f := accountControlsFixture()
			res := accountControlRequest(t, f, http.MethodPost, test.path, test.body)
			assertAccountControlError(t, res, http.StatusBadRequest)
			if len(f.calls) != 0 {
				t.Errorf("ambiguous field reached service: %v", f.calls)
			}
		})
	}
}

type countedAccountAdmission struct {
	AccountsManagerControls
	calls int
}

func (s *countedAccountAdmission) StartAccountSwitch(ctx context.Context, op domain.AccountsManagerSwitch) (domain.AccountsManagerSwitch, error) {
	s.calls++
	return s.AccountsManagerControls.StartAccountSwitch(ctx, op)
}

func (s *countedAccountAdmission) StartAccountRemoval(ctx context.Context, op, id string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, error) {
	s.calls++
	return s.AccountsManagerControls.StartAccountRemoval(ctx, op, id, revision, confirmed)
}

func TestAccountControlUnicodeAliasesPreserveDurableState(t *testing.T) {
	for index, name := range accountControlAliasCases("session-a", 7, 9) {
		t.Run(name.name, func(t *testing.T) {
			s := newPersistedControlService(t)
			binding := s.seedSession(t, "amc_a")
			impact, err := s.store.AccountsManagerRemovalImpact(t.Context(), "amc_a")
			if err != nil {
				t.Fatal(err)
			}
			test := accountControlAliasCases(string(binding.SessionID), binding.Revision, impact.Revision)[index]
			counted := &countedAccountAdmission{AccountsManagerControls: s}
			res := accountControlRequest(t, counted, http.MethodPost, test.path, test.body)
			assertAccountControlError(t, res, http.StatusBadRequest)
			if counted.calls != 0 {
				t.Errorf("ambiguous request delegated %d times", counted.calls)
			}
			if _, found, err := s.store.GetAccountsManagerSwitch(t.Context(), "switch-a"); err != nil || found {
				t.Errorf("request created a switch journal: found=%t err=%v", found, err)
			}
			if _, found, err := s.store.GetAccountsManagerRemoval(t.Context(), "removal-a"); err != nil || found {
				t.Errorf("request created a removal journal: found=%t err=%v", found, err)
			}
			for _, account := range []string{"amc_a", "amc_b"} {
				if deleting, err := s.store.AccountsManagerAccountDeleting(t.Context(), account); err != nil || deleting {
					t.Errorf("request changed account fence: account=%s deleting=%t err=%v", account, deleting, err)
				}
			}
			after, found, err := s.store.GetAccountsManagerSessionRoute(t.Context(), binding.SessionID, binding.Provider)
			if err != nil || !found || !reflect.DeepEqual(binding, after) {
				t.Errorf("request changed binding or session fence: found=%t err=%v", found, err)
			}
			afterImpact, err := s.store.AccountsManagerRemovalImpact(t.Context(), "amc_a")
			if err != nil || !reflect.DeepEqual(impact, afterImpact) {
				t.Errorf("request changed removal impact: err=%v", err)
			}
		})
	}
}

func TestAccountControlRecoveryRejectsBodyFields(t *testing.T) {
	for _, resource := range []string{"switch", "removal"} {
		for _, action := range []string{"retry", "cancel"} {
			for _, body := range []string{`{"accountId":"amc_b"}`, `{"operationId":"foreign"}`, `{"expectedRevision":7}`, `{"policy":"interrupt"}`, `{"newConversation":true}`} {
				t.Run(resource+"/"+action+"/"+body, func(t *testing.T) {
					f := accountControlsFixture()
					beforeSwitch, beforeRemoval := f.switchOp, f.removal
					path := "/sessions/session-a/account-switches/switch-a/" + action
					if resource == "removal" {
						path = "/accounts-manager/removals/removal-a/" + action
					}
					res := accountControlRequest(t, f, http.MethodPost, path, body)
					assertAccountControlError(t, res, http.StatusBadRequest)
					if len(f.calls) != 1 || !reflect.DeepEqual(f.switchOp, beforeSwitch) || !reflect.DeepEqual(f.removal, beforeRemoval) {
						t.Fatalf("nonempty recovery body reached mutation: %v", f.calls)
					}
				})
			}
		}
	}
}

func TestAccountControlRejectsForeignOperationReads(t *testing.T) {
	for _, resource := range []string{"switch", "removal"} {
		for _, action := range []string{"", "/retry", "/cancel"} {
			t.Run(resource+action, func(t *testing.T) {
				f := accountControlsFixture()
				f.switchOp.ID, f.removal.ID = "foreign", "foreign"
				path := "/sessions/session-a/account-switches/switch-a" + action
				if resource == "removal" {
					path = "/accounts-manager/removals/removal-a" + action
				}
				method, body := http.MethodGet, ""
				if action != "" {
					method, body = http.MethodPost, "{}"
				}
				res := accountControlRequest(t, f, method, path, body)
				assertAccountControlError(t, res, http.StatusNotFound)
				if len(f.calls) != 1 {
					t.Fatalf("foreign operation reached mutation: %v", f.calls)
				}
			})
		}
	}
}

type accountControlIdentityChange struct {
	*fakeAccountControls
	nextSwitch  domain.AccountsManagerSwitch
	nextRemoval domain.AccountsManagerRemoval
}

func (f *accountControlIdentityChange) AccountSwitch(ctx context.Context, id domain.SessionID, op string) (domain.AccountsManagerSwitch, error) {
	result, err := f.fakeAccountControls.AccountSwitch(ctx, id, op)
	f.switchOp = f.nextSwitch
	return result, err
}

func (f *accountControlIdentityChange) AccountRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, error) {
	result, err := f.fakeAccountControls.AccountRemoval(ctx, id)
	f.removal = f.nextRemoval
	return result, err
}

func TestAccountControlRejectsPostMutationIdentityChanges(t *testing.T) {
	for _, identity := range []string{"switch-operation", "switch-session", "removal-operation"} {
		for _, action := range []string{"start", "retry", "cancel"} {
			t.Run(identity+"/"+action, func(t *testing.T) {
				f := accountControlsFixture()
				controls := &accountControlIdentityChange{fakeAccountControls: f, nextSwitch: f.switchOp, nextRemoval: f.removal}
				switch identity {
				case "switch-operation":
					controls.nextSwitch.ID = "foreign"
				case "switch-session":
					controls.nextSwitch.SessionID = "session-b"
				case "removal-operation":
					controls.nextRemoval.ID = "foreign"
				}
				path, body := "/sessions/session-a/account-switches/switch-a/"+action, "{}"
				if identity == "removal-operation" {
					path = "/accounts-manager/removals/removal-a/" + action
				}
				wantCalls := 2
				if action == "start" {
					wantCalls = 1
					f.switchOp, f.removal = controls.nextSwitch, controls.nextRemoval
					path = "/sessions/session-a/account-switches"
					body = `{"operationId":"switch-a","expectedRevision":7,"mode":"managed","accountId":"amc_b","policy":"drain"}`
					if identity == "removal-operation" {
						path, body = "/accounts-manager/accounts/amc_a/removals", `{"operationId":"removal-a","expectedRevision":9,"confirmed":true}`
					}
				}
				res := accountControlRequest(t, controls, http.MethodPost, path, body)
				assertAccountControlError(t, res, http.StatusNotFound)
				if len(f.calls) != wantCalls || strings.Contains(res.Body.String(), "foreign") || strings.Contains(res.Body.String(), "session-b") {
					t.Fatalf("unexpected identity projection: calls=%v response=%s", f.calls, res.Body.String())
				}
			})
		}
	}
}

func TestAccountControlNativeAndEscapedCanonicalFields(t *testing.T) {
	for _, newConversation := range []bool{false, true} {
		t.Run(fmt.Sprintf("new-conversation=%t", newConversation), func(t *testing.T) {
			f := accountControlsFixture()
			f.switchOp.TargetMode, f.switchOp.TargetAccountID = domain.AccountsManagerNative, ""
			body := fmt.Sprintf(`{"operationId":"switch-a","expected\u0052evision":7,"mode":"native","policy":"drain","newConversation":%t}`, newConversation)
			res := accountControlRequest(t, f, http.MethodPost, "/sessions/session-a/account-switches", body)
			if res.Code != http.StatusAccepted || len(f.calls) != 1 || f.switchInput.TargetMode != domain.AccountsManagerNative || f.switchInput.TargetAccountID != "" || f.switchInput.NewConversation != newConversation || f.switchInput.SourceRevision != 7 {
				t.Fatalf("exact decoded tag or native choice changed: status=%d input=%+v", res.Code, f.switchInput)
			}
		})
	}
	for _, pair := range []string{`"expectedRevision":7,"expected\u0052evision":8`, `"expected\u0052evision":8,"expectedRevision":7`} {
		f := accountControlsFixture()
		body := `{"operationId":"switch-a",` + pair + `,"mode":"native","policy":"drain"}`
		res := accountControlRequest(t, f, http.MethodPost, "/sessions/session-a/account-switches", body)
		assertAccountControlError(t, res, http.StatusBadRequest)
		if len(f.calls) != 0 {
			t.Fatal("repeated decoded canonical field reached admission")
		}
	}
}

func TestAccountControlRejectsNullableFields(t *testing.T) {
	for _, resource := range []struct {
		path   string
		body   string
		fields []string
	}{
		{"/sessions/session-a/account-switches", `{"operationId":"switch-a","expectedRevision":7,"mode":"managed","accountId":"amc_b","policy":"interrupt","newConversation":false}`, []string{"operationId", "expectedRevision", "mode", "accountId", "policy", "newConversation"}},
		{"/accounts-manager/accounts/amc_a/removals", `{"operationId":"removal-a","expectedRevision":0,"confirmed":true}`, []string{"operationId", "expectedRevision", "confirmed"}},
	} {
		for _, field := range resource.fields {
			t.Run(resource.path+"/"+field, func(t *testing.T) {
				var body map[string]any
				if err := json.Unmarshal([]byte(resource.body), &body); err != nil {
					t.Fatal(err)
				}
				body[field] = nil
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				f := accountControlsFixture()
				res := accountControlRequest(t, f, http.MethodPost, resource.path, string(encoded))
				assertAccountControlError(t, res, http.StatusBadRequest)
				if len(f.calls) != 0 {
					t.Fatal("null reached admission")
				}
			})
		}
	}
}

func TestAccountControlExactBodyLimit(t *testing.T) {
	for _, resource := range []struct {
		path     string
		body     string
		preReads int
	}{
		{"/sessions/session-a/account-switches", `{"operationId":"switch-a","expectedRevision":7,"mode":"managed","accountId":"amc_b","policy":"drain"}`, 0},
		{"/accounts-manager/accounts/amc_a/removals", `{"operationId":"removal-a","expectedRevision":0,"confirmed":true}`, 0},
		{"/sessions/session-a/account-switches/switch-a/retry", `{}`, 1},
		{"/sessions/session-a/account-switches/switch-a/cancel", `{}`, 1},
		{"/accounts-manager/removals/removal-a/retry", `{}`, 1},
		{"/accounts-manager/removals/removal-a/cancel", `{}`, 1},
	} {
		for _, size := range []int{4095, 4096, 4097} {
			t.Run(fmt.Sprintf("%s/%d", resource.path, size), func(t *testing.T) {
				f := accountControlsFixture()
				body := resource.body + strings.Repeat(" ", size-len(resource.body))
				res := accountControlRequest(t, f, http.MethodPost, resource.path, body)
				wantCalls := resource.preReads
				if size <= 4096 {
					wantCalls++
					if res.Code != http.StatusAccepted {
						t.Errorf("valid bounded body rejected: %d %s", res.Code, res.Body.String())
					}
				} else {
					assertAccountControlError(t, res, http.StatusRequestEntityTooLarge)
				}
				if len(f.calls) != wantCalls {
					t.Fatalf("body size=%d calls=%v", size, f.calls)
				}
			})
		}
	}
}
