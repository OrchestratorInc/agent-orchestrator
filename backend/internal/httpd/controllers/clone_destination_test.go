package controllers_test

import (
	"net/http"
	"testing"
)

func TestCloneDestinationInvalidInputUsesErrorEnvelope(t *testing.T) {
	srv := newTestServer(t)
	for _, tt := range []struct {
		body string
		code string
	}{
		{`{"remoteUrl":"not-a-url","destinationParent":"/clones"}`, "INVALID_GIT_URL"},
		{`{"remoteUrl":"file:///source","destinationParent":""}`, "PATH_REQUIRED"},
		{`{"remoteUrl":"file:///source","destinationParent":"/clones","unknown":true}`, "INVALID_JSON"},
	} {
		body, status, headers := doRequest(t, srv, http.MethodPost, "/api/v1/projects/clone/check", tt.body)
		assertJSON(t, headers)
		assertErrorCode(t, body, status, http.StatusBadRequest, tt.code)
	}
}
