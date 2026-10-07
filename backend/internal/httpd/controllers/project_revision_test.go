package controllers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestProjectsAPI_ConfigRevision(t *testing.T) {
	srv := newTestServer(t)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "revision"))+`,"projectId":"revision"}`)
	if status != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", status, body)
	}
	readRevision := func(body []byte) int64 {
		t.Helper()
		var response struct {
			Project struct {
				Revision *int64 `json:"revision"`
			} `json:"project"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.Project.Revision == nil || *response.Project.Revision < 0 {
			t.Fatalf("missing or negative revision: %s", body)
		}
		return *response.Project.Revision
	}
	initial := readRevision(body)
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/projects/revision", "")
	if status != http.StatusOK || readRevision(body) != initial {
		t.Fatalf("GET: status=%d body=%s", status, body)
	}
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/revision/config", fmt.Sprintf(`{"config":{"defaultBranch":"develop"},"expectedRevision":%d}`, initial))
	if status != http.StatusOK {
		t.Fatalf("CAS: status=%d body=%s", status, body)
	}
	committed := readRevision(body)
	if committed != initial+1 {
		t.Fatalf("committed revision=%d want=%d", committed, initial+1)
	}
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/revision/config", fmt.Sprintf(`{"config":{},"expectedRevision":%d}`, initial))
	assertErrorCode(t, body, status, http.StatusConflict, "PROJECT_REVISION_CONFLICT")
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/revision/config", `{"config":{}}`)
	if status != http.StatusOK || readRevision(body) != committed+1 {
		t.Fatalf("legacy omission: status=%d body=%s", status, body)
	}
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/revision", `{"displayName":"Renamed","config":{}}`)
	if status != http.StatusOK || readRevision(body) != committed+2 {
		t.Fatalf("settings revision: status=%d body=%s", status, body)
	}
	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/projects/revision/permissions", `{"permissions":"accept-edits"}`)
	if status != http.StatusOK || readRevision(body) != committed+3 {
		t.Fatalf("permissions revision: status=%d body=%s", status, body)
	}
}

func TestProjectsAPI_ConfigRejectsMalformedPreconditions(t *testing.T) {
	srv := newTestServer(t)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "invalid-revision"))+`,"projectId":"invalid-revision"}`)
	if status != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", status, body)
	}
	before, status, _ := doRequest(t, srv, "GET", "/api/v1/projects/invalid-revision", "")
	if status != http.StatusOK {
		t.Fatalf("before malformed PUTs: status=%d body=%s", status, before)
	}
	for _, value := range []string{`null`, `true`, `false`, `-1`, `9223372036854775808`, `-9223372036854775809`, `1.5`, `1e0`, `"0"`, `{}`, `[]`} {
		t.Run(value, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, "PUT", "/api/v1/projects/invalid-revision/config", `{"config":{},"expectedRevision":`+value+`}`)
			assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
		})
	}
	for _, request := range []string{
		`{"config":{},"unexpected":0}`,
		`{"config":{"unexpected":0}}`,
		`{"config":{}} {}`,
		`{"config":{}} garbage`,
	} {
		t.Run(request, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, "PUT", "/api/v1/projects/invalid-revision/config", request)
			assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
		})
	}
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/invalid-revision/config", `{"config":{},"expectedRevision":9223372036854775807}`)
	assertErrorCode(t, body, status, http.StatusConflict, "PROJECT_REVISION_CONFLICT")
	after, status, _ := doRequest(t, srv, "GET", "/api/v1/projects/invalid-revision", "")
	if status != http.StatusOK || string(before) != string(after) {
		t.Fatalf("malformed/stale PUT changed row: before=%s after=%s status=%d", before, after, status)
	}
}
