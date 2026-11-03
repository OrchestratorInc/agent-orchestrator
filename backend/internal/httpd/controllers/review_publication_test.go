package controllers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewsSubmitPublicationWire(t *testing.T) {
	for _, body := range []string{
		`{"runId":"gl","verdict":"approved","publication":{"provider":"gitlab","status":"published","prUrl":"https://gitlab.example/g/r/-/merge_requests/39","targetSha":"head","refs":[{"kind":"note","id":"101"}]}}`,
		`{"reviews":[{"runId":"gl","verdict":"approved","publication":{"provider":"gitlab","status":"published","prUrl":"https://gitlab.example/g/r/-/merge_requests/39","targetSha":"head","refs":[{"kind":"note","id":"101"}]}}]}`,
	} {
		t.Run(body[:10], func(t *testing.T) {
			// PRURL is inert receipt data; all requests use the loopback test server.
			svc := &fakeReviewService{}
			srv := newReviewTestServer(t, svc)

			response, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/worker/reviews/submit", body)

			require.Equal(t, http.StatusOK, status)
			require.Len(t, svc.submitted, 1)
			require.NotNil(t, svc.submitted[0].Publication)
			assert.Equal(t, "101", svc.submitted[0].Publication.Refs[0].ID)
			assert.Empty(t, svc.submitted[0].GithubReviewID)
			var result controllers.ReviewRunResponse
			require.NoError(t, json.Unmarshal(response, &result))
			assert.Equal(t, svc.submitted[0].Publication, result.Review.Publication)
		})
	}
}
