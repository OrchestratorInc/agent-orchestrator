package specgen

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

func accountsManagerControlOperations() []operation {
	session := []any{controllers.SessionIDParam{}}
	switchID := []any{controllers.SessionIDParam{}, controllers.AccountsManagerControlOperationIDParam{}}
	account := []any{controllers.AccountsManagerAccountIDParam{}}
	removalID := []any{controllers.AccountsManagerControlOperationIDParam{}}
	ops := []operation{
		{method: http.MethodGet, path: "/api/v1/sessions/account-selection", id: "getInitialAccountSelection", tag: "sessions", summary: "Read initial account selection support", resps: []respUnit{{http.StatusOK, controllers.InitialAccountSelectionResponse{}}}},
		{method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/account", id: "getSessionAccount", tag: "sessions", summary: "Read the selected session account and current switch", pathParams: session, resps: []respUnit{{http.StatusOK, controllers.AccountsManagerSessionResponse{}}}},
		{method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/account-switches", id: "startSessionAccountSwitch", tag: "sessions", summary: "Request an explicit session account switch", pathParams: session, reqBody: controllers.AccountsManagerSwitchRequest{}, resps: []respUnit{{http.StatusAccepted, controllers.AccountsManagerSwitchResponse{}}}},
		{method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/account-switches/{operationId}", id: "getSessionAccountSwitch", tag: "sessions", summary: "Read a session account switch", pathParams: switchID, resps: []respUnit{{http.StatusOK, controllers.AccountsManagerSwitchResponse{}}}},
		{method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/account-switches/{operationId}/retry", id: "retrySessionAccountSwitch", tag: "sessions", summary: "Retry the same session account switch", pathParams: switchID, resps: []respUnit{{http.StatusAccepted, controllers.AccountsManagerSwitchResponse{}}}},
		{method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/account-switches/{operationId}/cancel", id: "cancelSessionAccountSwitch", tag: "sessions", summary: "Cancel a session account switch before stopping", pathParams: switchID, resps: []respUnit{{http.StatusAccepted, controllers.AccountsManagerSwitchResponse{}}}},
		{method: http.MethodGet, path: "/api/v1/accounts-manager/accounts/{accountId}/removal-impact", id: "getAccountRemovalImpact", tag: "system", summary: "Preview every binding affected by account removal", pathParams: account, resps: []respUnit{{http.StatusOK, controllers.AccountsManagerRemovalImpactResponse{}}}},
		{method: http.MethodPost, path: "/api/v1/accounts-manager/accounts/{accountId}/removals", id: "startAccountRemoval", tag: "system", summary: "Confirm removal against the observed binding revision", pathParams: account, reqBody: controllers.AccountsManagerRemovalRequest{}, resps: []respUnit{{http.StatusAccepted, controllers.AccountsManagerRemovalResponse{}}}},
		{method: http.MethodGet, path: "/api/v1/accounts-manager/removals/{operationId}", id: "getAccountRemoval", tag: "system", summary: "Read coordinated account removal", pathParams: removalID, resps: []respUnit{{http.StatusOK, controllers.AccountsManagerRemovalResponse{}}}},
		{method: http.MethodPost, path: "/api/v1/accounts-manager/removals/{operationId}/retry", id: "retryAccountRemoval", tag: "system", summary: "Retry the same coordinated account removal", pathParams: removalID, resps: []respUnit{{http.StatusAccepted, controllers.AccountsManagerRemovalResponse{}}}},
		{method: http.MethodPost, path: "/api/v1/accounts-manager/removals/{operationId}/cancel", id: "cancelAccountRemoval", tag: "system", summary: "Cancel account removal before stopping", pathParams: removalID, resps: []respUnit{{http.StatusAccepted, controllers.AccountsManagerRemovalResponse{}}}},
	}
	for i := range ops {
		for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable} {
			ops[i].resps = append(ops[i].resps, respUnit{status, envelope.APIError{}})
		}
	}
	return ops
}
